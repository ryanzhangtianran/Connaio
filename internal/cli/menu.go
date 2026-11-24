package cli

import (
	"bufio"
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// installURL is install.sh on the download site: run again, it updates Connaio.
const installURL = "https://connaio-dl.ryanzhangtianran.top/install.sh"

// entry is a line of the menu: the key that opens it and its Nerd Font icon.
type entry struct{ id, label, note, key, icon string }

// entries in the order the work goes: the state, the routing, the profiles, the VPNs and tunnels,
// TUN, then the service.
var entries = []entry{
	{"status", "状态", "内核、出站模式、配置与隧道", "s", "\uf0e4"},
	{"mode", "出站模式", "规则、全局或直连", "m", "\uf074"},
	{"nodes", "节点", "切换节点、测试延迟", "n", "\uf0ac"},
	{"profiles", "配置", "使用、更新或添加订阅", "p", "\uf15c"},
	{"vpn", "VPN", "登录或断开 VPN", "v", "\uf132"},
	{"tun", "TUN", "接管全部或选中程序的流量", "t", "\uf0ec"},
	{"tunnels", "反向隧道", "启动或停止隧道", "r", "\uf079"},
	{"connections", "连接", "经 Connaio 的连接", "c", "\uf1e6"},
	{"service", "服务", "启动、停止、日志与更新 Connaio", "d", "\uf013"},
	{"help", "帮助", "命令行的全部命令", "?", "\uf059"},
	{"quit", "退出", "", "q", "\uf011"},
}

const connectionsRefreshInterval = time.Second

// plainIcons is CONNAIO_ICONS=plain, for a terminal without a Nerd Font: no icons, no rounded edges
// on the status line.
var plainIcons = os.Getenv("CONNAIO_ICONS") == "plain"

// glyph is a Nerd Font glyph, or nothing under plainIcons.
func glyph(s string) string {
	if plainIcons {
		return ""
	}
	return s
}

// the palette: catppuccin frappé, as homie's
var (
	cMuted, cBorder, cAccent, cTitle = lipgloss.Color("#a5adce"), lipgloss.Color("#737994"), lipgloss.Color("#85c1dc"), lipgloss.Color("#ca9ee6")
	cOK, cWarn, cErr, cText          = lipgloss.Color("#a6d189"), lipgloss.Color("#e5c890"), lipgloss.Color("#e78284"), lipgloss.Color("#c6d0f5")
	cBase, cSurface                  = lipgloss.Color("#232634"), lipgloss.Color("#414559")
	modeColor                        = map[string]lipgloss.Color{"rule": cAccent, "global": cTitle, "direct": cOK}

	dim   = lipgloss.NewStyle().Foreground(cMuted)
	theme = formTheme()
)

func fg(c lipgloss.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

// formTheme is the forms' look from the palette: the whole form framed, no side bar on the fields.
func formTheme() *huh.Theme {
	t := huh.ThemeBase()
	t.Form.Base = lipgloss.NewStyle() // inside the content pane, framed already
	f := &t.Focused
	f.Base, f.Card = lipgloss.NewStyle(), lipgloss.NewStyle()
	f.Title = fg(cTitle).Bold(true)
	f.Description = fg(cMuted)
	f.SelectSelector = fg(cAccent).SetString("› ")
	f.Option, f.UnselectedOption = fg(cText), fg(cText)
	f.SelectedOption = fg(cAccent).Bold(true)
	f.ErrorIndicator, f.ErrorMessage = fg(cErr).SetString(" *"), fg(cErr)
	f.FocusedButton = lipgloss.NewStyle().Padding(0, 2).MarginRight(1).Foreground(cBase).Background(cAccent)
	f.BlurredButton = lipgloss.NewStyle().Padding(0, 2).MarginRight(1).Foreground(cText).Background(cBorder)
	f.Next = f.FocusedButton
	f.TextInput.Cursor, f.TextInput.Prompt = fg(cAccent), fg(cAccent)
	f.TextInput.Placeholder, f.TextInput.Text = fg(cMuted), fg(cText)
	t.Blurred = t.Focused
	t.Blurred.Title = fg(cMuted)
	t.Blurred.SelectSelector = lipgloss.NewStyle().SetString("  ")
	t.Help.ShortKey, t.Help.FullKey = fg(cText), fg(cText)
	t.Help.ShortDesc, t.Help.FullDesc = fg(cMuted), fg(cMuted)
	t.Help.ShortSeparator, t.Help.FullSeparator = fg(cBorder), fg(cBorder)
	return t
}

// Menu is connaio run bare in a terminal: a full-screen menu, as homie's, the result of each entry
// beside it, a status line under both; what runs the service (systemctl, sudo) gets the plain terminal while it runs.
func Menu(in io.Reader, out io.Writer) error {
	last, err := tea.NewProgram(newModel(), tea.WithAltScreen(), tea.WithInput(in), tea.WithOutput(out)).Run()
	if m, ok := last.(model); ok && m.updated && err == nil {
		// the binary on disk is the new version now: it takes over, else this one would go on as the old
		return syscall.Exec(exe(), os.Args, os.Environ())
	}
	return err
}

type model struct {
	w, h    int
	cursor  int
	content bool   // narrow layout: the content pane is showing instead of the menu
	title   string // the content pane's title
	vp      viewport.Model
	form    *huh.Form
	submit  func() tea.Cmd
	head    head
	busy    bool
	updated bool // 更新 Connaio succeeded: the menu quits and starts again as the new version

	watchingConnections bool
	connectionsSession  uint64 // identifies the current visit so late results cannot replace another pane
}

type (
	contentMsg         struct{ title, body string }
	headMsg            head // the status line, read off the loop: it asks the service and the core
	updatedMsg         struct{}
	connectionsTickMsg uint64
	connectionsMsg     struct {
		session uint64
		body    string
	}
	doneMsg struct {
		title string
		err   error
	}
	// formMsg opens a form: a step that needs the answer to the one before (the group, then its node)
	formMsg struct {
		title  string
		submit func() tea.Cmd
		fields []huh.Field
	}
)

// head is the status line: its first block (the outbound mode, or what keeps the core from one),
// then the service's and the core's state and the profile.
type head struct {
	mode  string
	color lipgloss.Color
	parts []part
}

type part struct {
	k, v string
	c    lipgloss.Color
}

func newModel() model {
	return model{head: head{mode: "读取中", color: cBorder}, vp: viewport.New(0, 0), title: "状态", busy: true}
}

func (m model) Init() tea.Cmd { return tea.Batch(refreshHead, capture("状态", "status")) }

func refreshHead() tea.Msg { return headMsg(summary()) }

func readConnections(session uint64) tea.Cmd {
	return func() tea.Msg {
		var out bytes.Buffer
		c, err := client0()
		if err == nil {
			err = run(c, "connections", nil, false, "", &out)
		}
		if err != nil {
			return connectionsMsg{session, errLine(err)}
		}
		return connectionsMsg{session, unruled(out.String())}
	}
}

const wideMin = 90 // below this the menu and the content take turns on screen

func (m model) wide() bool { return m.w >= wideMin }

// iconWidth is an entry's icon and the gap after it, kept under plainIcons: the menu as wide either way.
const iconWidth = 3

// menuWidth fits the longest label, its icon, its key and the frame.
func menuWidth() int {
	w := 0
	for _, e := range entries {
		w = max(w, lipgloss.Width(e.label))
	}
	return w + iconWidth + 8 // a space, the label, three before the key, the key, a space; the frame
}

func (m model) contentWidth() int {
	if m.wide() {
		return m.w - menuWidth() - 1
	}
	return m.w
}

func (m model) bodyHeight() int { return max(m.h-1, 3) } // the status line under it

// paneWidth and paneHeight are what the content pane's frame holds: its border and padding off.
func (m model) paneWidth() int  { return max(m.contentWidth()-4, 20) }
func (m model) paneHeight() int { return max(m.bodyHeight()-2, 1) }

func (m *model) layout() {
	m.vp.Width, m.vp.Height = m.paneWidth(), m.paneHeight()
	if m.form != nil {
		m.form = m.form.WithWidth(m.paneWidth())
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.layout()
		return m, nil
	case headMsg:
		m.head = head(msg)
		return m, nil
	case contentMsg:
		m.busy, m.title = false, msg.title
		m.vp.SetContent(msg.body)
		m.vp.GotoTop()
		return m, refreshHead // whatever just ran may have changed the mode, the profile or the service
	case connectionsTickMsg:
		if m.watchingConnections && uint64(msg) == m.connectionsSession {
			return m, readConnections(m.connectionsSession)
		}
		return m, nil
	case connectionsMsg:
		if !m.watchingConnections || msg.session != m.connectionsSession {
			return m, nil
		}
		m.vp.SetContent(msg.body)
		m.vp.SetYOffset(m.vp.YOffset)
		// Wait until the current request finishes before scheduling another one.
		return m, tea.Tick(connectionsRefreshInterval, func(time.Time) tea.Msg {
			return connectionsTickMsg(msg.session)
		})
	case doneMsg:
		body := fg(cOK).Render("✓ " + msg.title + "完成")
		if msg.err != nil {
			body = errLine(msg.err)
		}
		return m, show(msg.title, body+"\n\n"+dim.Render("输出已显示在终端中"))
	case updatedMsg:
		m.updated = true
		return m, tea.Quit
	case formMsg:
		m.busy = false
		return m, m.openForm(msg.title, msg.submit, msg.fields...)
	}
	if m.form != nil {
		return m.updateForm(msg)
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		s := k.String()
		menu := !m.content || m.wide() // the menu on screen: it takes the arrows and the letters
		switch {
		case s == "ctrl+c" || s == "q":
			return m, tea.Quit
		case s == "esc" || s == "left" || s == "h":
			// In the wide layout the connections pane stays visible.
			if !m.wide() || !m.watchingConnections {
				m.content, m.watchingConnections = false, false
			}
			return m, nil
		case !menu: // the rest scroll the content pane
		case s == "up" || s == "k":
			m.cursor = (m.cursor + len(entries) - 1) % len(entries)
			return m, nil
		case s == "down" || s == "j":
			m.cursor = (m.cursor + 1) % len(entries)
			return m, nil
		case s == "enter" || s == "right" || s == "l":
			return m.open(m.cursor)
		default:
			if i := slices.IndexFunc(entries, func(e entry) bool { return e.key == s }); i >= 0 {
				m.cursor = i
				return m.open(i)
			}
		}
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg) // scrolling: pgup/pgdn, and arrows in the narrow content pane
	return m, cmd
}

// open opens entries[i] in the content pane, unless a step is still running; 退出 quits even then.
func (m model) open(i int) (tea.Model, tea.Cmd) {
	if entries[i].id == "quit" {
		return m, tea.Quit
	}
	if m.busy {
		return m, nil
	}
	m.content = true
	cmd := m.choose(entries[i].id)
	return m, cmd
}

func (m model) updateForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && (k.String() == "esc" || k.String() == "left" && picking(m.form)) {
		m.form, m.content = nil, false
		return m, nil
	}
	f, cmd := m.form.Update(msg)
	m.form = f.(*huh.Form)
	switch m.form.State {
	case huh.StateCompleted:
		submit := m.submit
		m.form, m.busy = nil, true
		m.vp.SetContent(dim.Render("处理中…"))
		return m, tea.Batch(cmd, bg(submit)) // what it does next may call the core: off the loop
	case huh.StateAborted:
		m.form, m.content = nil, false
	}
	return m, cmd
}

// picking says the form's field is a list to pick from: ← there goes back to the menu, as esc does;
// in a text field it moves the cursor, in a yes/no one it picks yes.
func picking(f *huh.Form) bool {
	switch f.GetFocusedField().(type) {
	case *huh.Input, *huh.Confirm:
		return false
	}
	return true
}

// openForm shows fields in the content pane; submit runs once they are filled in. submit must not
// touch m: Update works on copies, so what it finds comes back as a message.
func (m *model) openForm(title string, submit func() tea.Cmd, fields ...huh.Field) tea.Cmd {
	m.title, m.submit = title, submit
	m.form = huh.NewForm(huh.NewGroup(fields...)).WithTheme(theme).WithShowHelp(false) // the keys are on the bottom line
	m.layout()
	return m.form.Init()
}

// bg runs step off the loop, which no call to the core may hold up, then what it returns: the
// form it asks for (ask) or what it shows.
func bg(step func() tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		if c := step(); c != nil {
			return c()
		}
		return nil
	}
}

// ask opens a form from a step's result, as a message: the step runs off the model.
func ask(title string, submit func() tea.Cmd, fields ...huh.Field) tea.Cmd {
	return func() tea.Msg { return formMsg{title, submit, fields} }
}

func show(title, body string) tea.Cmd {
	return func() tea.Msg { return contentMsg{title, body} }
}

// failed shows err in the content pane.
func failed(title string, err error) tea.Cmd { return show(title, errLine(err)) }

// errLine is err after a ✗, its further lines (an error joined from two tries) under its text.
func errLine(err error) string {
	return fg(cErr).Render("✗ " + strings.ReplaceAll(err.Error(), "\n", "\n  "))
}

// capture runs a command as typed on the command line, in the background, and shows what it printed.
func capture(title string, args ...string) tea.Cmd {
	return captureFn(title, func(out io.Writer) error { return Main(args, out) })
}

func captureFn(title string, fn func(io.Writer) error) tea.Cmd {
	return func() tea.Msg {
		var b bytes.Buffer
		if err := fn(&b); err != nil {
			b.WriteString("\n" + errLine(err))
		}
		body := unruled(b.String())
		if body == "" {
			body = fg(cOK).Render("✓ 完成")
		}
		return contentMsg{title, body}
	}
}

// unruled is a command's output for the content pane: the table's rules, which part its sections
// on a terminal, left out at the ends and a blank line between sections; the pane frames it already.
func unruled(out string) string {
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.Trim(l, "─") == "" && l != "" {
			l = ""
		}
		if l == "" && (len(lines) == 0 || lines[len(lines)-1] == "") {
			continue // none at the start, nor two in a row
		}
		lines = append(lines, l)
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// termRun hands the plain terminal to fn (sudo asks for a password, the installer draws its own
// screen), then waits for Enter so its output can be read before the menu comes back.
type termRun func() error

func (f termRun) Run() error {
	err := f()
	if err != nil {
		fmt.Println(errLine(err))
	}
	fmt.Print(dim.Render("\n↩ 按回车返回菜单 "))
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	return err
}
func (termRun) SetStdin(io.Reader)  {}
func (termRun) SetStdout(io.Writer) {}
func (termRun) SetStderr(io.Writer) {}

func runTerm(title string, fn func() error) tea.Cmd {
	return tea.Exec(termRun(fn), func(err error) tea.Msg { return doneMsg{title, err} })
}

// choose runs a menu entry: reports render in the content pane, choices open a form there under
// the state they change, and what runs the service gets the plain terminal for a moment.
func (m *model) choose(id string) tea.Cmd {
	if id == "connections" && m.watchingConnections {
		return nil
	}
	m.watchingConnections = false
	label := id
	for _, e := range entries {
		if e.id == id {
			label = e.label
		}
	}
	switch id {
	case "connections":
		m.watchingConnections = true
		m.connectionsSession++
		m.title = label
		m.vp.SetContent(dim.Render("读取中…"))
		m.vp.GotoTop()
		return readConnections(m.connectionsSession)
	case "quit":
		return tea.Quit
	case "help":
		return show(label, strings.TrimRight(Usage, "\n"))
	case "mode":
		mode := "rule"
		return m.openForm(label, func() tea.Cmd { return capture(label, "mode", mode) },
			huh.NewSelect[string]().Options(huh.NewOption("规则", "rule"), huh.NewOption("全局", "global"), huh.NewOption("直连", "direct")).Value(&mode))
	case "service":
		action := "restart"
		return m.openForm(label, func() tea.Cmd {
			switch action {
			case "logs":
				return runTerm("查看日志", logs)
			case "self-update":
				// -E under sudo: a proxy in the environment reaches install.sh's downloads too
				return tea.Exec(termRun(func() error { return system("sh", "-c", "curl -fsSL "+installURL+" | sh") }), func(err error) tea.Msg {
					if err != nil {
						return doneMsg{"更新 Connaio", err}
					}
					return updatedMsg{}
				})
			}
			return runTerm(map[string]string{"start": "启动服务", "stop": "停止服务", "restart": "重启服务"}[action], func() error { return service(action) })
		}, huh.NewSelect[string]().Description("在终端中运行，完成后按回车返回").Options(
			huh.NewOption("重启服务", "restart"), huh.NewOption("启动服务", "start"), huh.NewOption("停止服务", "stop"),
			huh.NewOption("查看日志", "logs"), huh.NewOption("更新 Connaio", "self-update")).Value(&action))
	}
	// the rest read the core: off the loop
	m.busy, m.title = true, label
	m.vp.SetContent(dim.Render("读取中…"))
	switch id {
	case "status":
		return capture(label, id)
	case "nodes":
		return bg(func() tea.Cmd { return nodesForm(label) })
	case "profiles":
		return bg(func() tea.Cmd {
			action := "use"
			return ask(label, func() tea.Cmd {
				if action == "add" {
					return addForm(label)
				}
				return pickProfile(label, action)
			}, huh.NewSelect[string]().Description(report("profiles")+"\n").
				Options(huh.NewOption("使用配置", "use"), huh.NewOption("更新订阅", "update"), huh.NewOption("添加订阅", "add"),
					huh.NewOption("修改订阅地址", "edit"), huh.NewOption("删除配置", "remove")).Value(&action))
		})
	case "vpn":
		return bg(func() tea.Cmd {
			action := "login"
			return ask(label, func() tea.Cmd { return vpnForm(action, label) },
				huh.NewSelect[string]().Description(report("vpns")+"\n").
					Options(huh.NewOption("登录 VPN", "login"), huh.NewOption("断开 VPN", "logout")).Value(&action))
		})
	case "tun":
		return bg(func() tea.Cmd { return tunForm(label) })
	case "tunnels":
		return bg(func() tea.Cmd { return pickTunnel(label) })
	}
	return nil
}

// nodesForm asks for a strategy group, then for one of its nodes, or to test them all.
func nodesForm(label string) tea.Cmd {
	return pickGroup(label, func(g group) tea.Cmd {
		const test = "\x00test" // no node's name
		node, desc := g.Now, "当前 "+cmp.Or(g.Now, "—")
		opts := []huh.Option[string]{huh.NewOption(fg(cAccent).Render("测试延迟"), test)}
		if g.Type == "Selector" {
			for _, p := range g.All {
				opts = append(opts, huh.NewOption(p.Name+"  "+dim.Render(delay(p.Delay)), p.Name))
			}
		} else { // a subnet group picks by the network, an url-test by delay: the core takes no pick
			node, desc = test, "自动选择，当前 "+cmp.Or(g.Now, "—")+"；不能手动切换"
		}
		return ask(label, func() tea.Cmd {
			if node == test {
				return capture(label, "test", g.Name)
			}
			return capture(label, "select", g.Name, node)
		}, huh.NewSelect[string]().Title(g.Name).Description(desc).Options(opts...).Value(&node))
	})
}

// addForm asks for a subscription's link and name, then adds it.
func addForm(label string) tea.Cmd {
	link, name := "", ""
	return ask(label, func() tea.Cmd { return capture(label, "profile", "add", link, name) },
		huh.NewInput().Title("订阅地址").Placeholder("https://…").Value(&link).Validate(func(s string) error {
			if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
				return errors.New("以 http:// 或 https:// 开头")
			}
			return nil
		}),
		huh.NewInput().Title("名称").Placeholder("留空用域名").Value(&name))
}

// pickTunnel asks for a reverse tunnel, then starts it, or stops it when running.
func pickTunnel(label string) tea.Cmd {
	c, err := client0()
	if err != nil {
		return failed(label, err)
	}
	var tunnels []struct{ Name, State string }
	if err := c.call(&tunnels, "CoreService.ReverseTunnels"); err != nil {
		return failed(label, err)
	}
	if len(tunnels) == 0 {
		return show(label, dim.Render("暂无反向隧道"))
	}
	var opts []huh.Option[int]
	for i, t := range tunnels {
		opts = append(opts, huh.NewOption(t.Name+"  "+dim.Render(cmp.Or(stateLabel[t.State], t.State)), i))
	}
	i := 0
	return ask(label, func() tea.Cmd {
		action := "stop"
		if tunnels[i].State == "stopped" {
			action = "start"
		}
		return capture(label, "tunnel", action, tunnels[i].Name)
	}, huh.NewSelect[int]().Title("要启动或停止的隧道").Options(opts...).Value(&i))
}

// tunForm switches TUN, all traffic or the programs picked (partial), or adds or removes a program
// or a user whose traffic TUN takes.
func tunForm(label string) tea.Cmd {
	action := "partial on"
	return ask(label, func() tea.Cmd {
		switch action {
		case "add":
			name := ""
			return ask(label, func() tea.Cmd { return capture(label, "partial", "add", name) },
				huh.NewInput().Title("程序名").Placeholder("如 python、git、uv").Value(&name))
		case "remove":
			return removeProgram(label)
		case "add-user", "remove-user":
			name := ""
			verb, _, _ := strings.Cut(action, "-")
			return ask(label, func() tea.Cmd { return capture(label, "tun-users", verb, name) },
				huh.NewInput().Title("用户名或 UID").Value(&name))
		}
		return capture(label, strings.Fields(action)...)
	}, huh.NewSelect[string]().Description("TUN 模式  "+report("enhanced")+"\n"+report("partial")+"\n走 TUN 的用户  "+report("tun-users")+"\n").Options(
		huh.NewOption("接管全部流量", "enhanced on"), huh.NewOption("关闭全部接管", "enhanced off"),
		huh.NewOption("只接管选中的程序", "partial on"), huh.NewOption("关闭部分接管", "partial off"),
		huh.NewOption("添加程序", "add"), huh.NewOption("移除程序", "remove"),
		huh.NewOption("添加走 TUN 的用户（仅 Linux）", "add-user"), huh.NewOption("移除走 TUN 的用户", "remove-user")).Value(&action))
}

// report is what a command prints, for a form's description: the state its choices change, above
// them with a blank line between.
func report(args ...string) string {
	var b bytes.Buffer
	if err := Main(args, &b); err != nil {
		return errLine(err)
	}
	return unruled(b.String())
}

// client0 is the menu's client: a call that takes long (the core busy) fails in 10s, not 3 minutes.
func client0() (*client, error) {
	c, err := connect(defaultAddr(), os.Getenv("CONNAIO_TOKEN"), "")
	if err == nil {
		c.http.Timeout = 10 * time.Second
	}
	return c, err
}

type group struct {
	Name, Type, Now string
	All             []struct {
		Name  string
		Delay int
	}
}

// pickProfile asks for a profile, then uses, updates or removes it (asking first), or asks for its
// new link (action edit; subscriptions only).
func pickProfile(label, action string) tea.Cmd {
	c, err := client0()
	if err != nil {
		return failed(label, err)
	}
	var ps struct {
		Current  string
		Profiles []profileEntry
	}
	if err := c.call(&ps, "ProfileService.Profiles"); err != nil {
		return failed(label, err)
	}
	var opts []huh.Option[int]
	for i, p := range ps.Profiles {
		if (action == "update" || action == "edit") && p.URL == "" {
			continue // a local profile has no link
		}
		name := p.Name
		if p.ID == ps.Current {
			name += dim.Render("  使用中")
		}
		opts = append(opts, huh.NewOption(name, i))
	}
	if len(opts) == 0 {
		return show(label, dim.Render(map[bool]string{true: "暂无订阅", false: "暂无配置"}[action == "update" || action == "edit"]))
	}
	i := opts[0].Value
	for _, o := range opts {
		if ps.Profiles[o.Value].ID == ps.Current {
			i = o.Value // the one in use, first
		}
	}
	return ask(label, func() tea.Cmd {
		p := ps.Profiles[i]
		switch action {
		case "edit":
			link := p.URL
			return ask(label, func() tea.Cmd { return capture(label, "profile", "edit", p.ID, link) },
				huh.NewInput().Title(p.Name+" 的订阅地址").Value(&link).Validate(func(s string) error {
					if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
						return errors.New("以 http:// 或 https:// 开头")
					}
					return nil
				}))
		case "remove":
			sure := false
			return ask(label, func() tea.Cmd {
				if !sure {
					return show(label, dim.Render("未删除"))
				}
				return capture(label, "profile", "remove", p.ID)
			}, huh.NewConfirm().Title("删除 "+p.Name+"？").Description("配置文件一并删除，无法恢复").Affirmative("删除").Negative("取消").Value(&sure))
		}
		return capture(label, "profile", action, p.ID)
	}, huh.NewSelect[int]().Title(map[string]string{"use": "要使用的配置", "update": "要更新的订阅", "edit": "要修改地址的订阅", "remove": "要删除的配置"}[action]).Options(opts...).Value(&i))
}

// pickGroup asks for a strategy group, then hands it to next.
func pickGroup(title string, next func(group) tea.Cmd) tea.Cmd {
	c, err := client0()
	if err != nil {
		return failed(title, err)
	}
	var all []group
	if err := c.call(&all, "CoreService.Groups"); err != nil {
		return failed(title, err)
	}
	var opts []huh.Option[int]
	for i, g := range all {
		if g.Name != "GLOBAL" {
			opts = append(opts, huh.NewOption(g.Name+"  "+dim.Render("→ "+cmp.Or(g.Now, "—")), i))
		}
	}
	if len(opts) == 0 {
		return show(title, dim.Render("暂无策略组"))
	}
	i := opts[0].Value
	return ask(title, func() tea.Cmd { return next(all[i]) }, huh.NewSelect[int]().Title("策略组").Options(opts...).Value(&i))
}

func removeProgram(title string) tea.Cmd {
	c, err := client0()
	if err != nil {
		return failed(title, err)
	}
	apps, err := c.programs()
	if err != nil {
		return failed(title, err)
	}
	if len(apps) == 0 {
		return show(title, dim.Render("没有已添加的程序"))
	}
	name := apps[0]
	return ask(title, func() tea.Cmd { return capture(title, "partial", "remove", name) },
		huh.NewSelect[string]().Title("要移除的程序").Options(huh.NewOptions(apps...)...).Value(&name))
}

// vpn signs a subscription's VPN in (EasyConnect with the account typed here, Tailscale with its
// login link shown), or disconnects one.
func vpnForm(id, title string) tea.Cmd {
	c, err := client0()
	if err != nil {
		return failed(title, err)
	}
	vpns, err := listVPNs(c)
	if err != nil {
		return failed(title, err)
	}
	var opts []huh.Option[int]
	for i, v := range vpns {
		if id == "login" && v.NeedsLogin || id == "logout" && v.connected() {
			opts = append(opts, huh.NewOption(fmt.Sprintf("%v  %s", v.Proxy["name"], dim.Render(fmt.Sprint(v.Proxy["type"]))), i))
		}
	}
	if len(opts) == 0 {
		return show(title, dim.Render(map[string]string{"login": "没有待登录的 VPN", "logout": "没有已连接的 VPN"}[id]))
	}
	i := opts[0].Value
	return ask(title, func() tea.Cmd {
		name := fmt.Sprint(vpns[i].Proxy["name"])
		if id == "logout" {
			return capture(title, "vpn", "logout", name)
		}
		signInWith := func(user, pass, totp string) tea.Cmd {
			return captureFn(title, func(out io.Writer) error { return signIn(c, name, user, pass, totp, out) })
		}
		if vpns[i].Proxy["type"] != "easyconnect" {
			return signInWith("", "", "")
		}
		user, pass, totp := "", "", ""
		return ask(title, func() tea.Cmd { return signInWith(user, pass, totp) },
			huh.NewInput().Title("用户名").Value(&user),
			huh.NewInput().Title("密码").EchoMode(huh.EchoModePassword).Value(&pass),
			huh.NewInput().Title("TOTP 密钥").Description("没有则留空").EchoMode(huh.EchoModePassword).Value(&totp))
	}, huh.NewSelect[int]().Title(map[string]string{"login": "要登录的 VPN", "logout": "要断开的 VPN"}[id]).Options(opts...).Value(&i))
}

func (m model) View() string {
	if m.w == 0 {
		return ""
	}
	body := m.contentView()
	if m.wide() {
		body = lipgloss.JoinHorizontal(lipgloss.Top, m.menuView(), " ", body)
	} else if !m.content {
		body = m.menuView()
	}
	return body + "\n" + m.statusline()
}

func trim(s string, w int) string { return lipgloss.NewStyle().MaxWidth(w).Render(s) }

// keys are what the keys do now, on the bottom edge of the pane at the right.
func (m model) keys() string {
	switch {
	case m.form != nil && picking(m.form):
		return "↑↓ 选择 · 回车 确定 · ← 或 esc 返回菜单"
	case m.form != nil:
		return "回车 确定 · shift+tab 上一项 · esc 返回菜单"
	case !m.wide() && m.content:
		return "↑↓ 滚动 · ← 或 esc 返回 · q 退出"
	case !m.wide():
		return "↑↓ 移动 · 回车或字母键 打开 · q 退出"
	}
	return "↑↓ 移动 · 回车或字母键 打开 · pgup/pgdn 滚动 · q 退出"
}

// box frames body w wide and h high, the title on the top edge and the keys on the bottom one, both
// from the left as the text in it is; either left out when it does not fit.
func box(w, h int, title, keys, body string, pad int) string {
	edge := fg(cBorder)
	label := func(s string, st lipgloss.Style, l, r string) string {
		if s == "" || lipgloss.Width(s)+5 > w {
			return edge.Render(l + strings.Repeat("─", w-2) + r)
		}
		return edge.Render(l+"─") + st.Render(" "+s+" ") + edge.Render(strings.Repeat("─", w-5-lipgloss.Width(s))+r)
	}
	body = lipgloss.NewStyle().MaxWidth(w - 2 - 2*pad).Render(body) // cut, not wrapped: a wrapped line would push the rest down
	sides := lipgloss.NewStyle().Border(lipgloss.RoundedBorder(), false, true).BorderForeground(cBorder).
		Padding(0, pad).Width(w - 2).Height(h - 2).MaxHeight(h - 2)
	return label(title, fg(cTitle).Bold(true), "╭", "╮") + "\n" + sides.Render(body) + "\n" + label(keys, dim, "╰", "╯")
}

func (m model) menuView() string {
	w, keys := menuWidth(), ""
	if !m.wide() {
		w, keys = m.w, m.keys()
	}
	rows := m.bodyHeight() - 2
	first := max(0, min(m.cursor-rows/2, len(entries)-rows)) // the cursor kept in view on short windows
	var lines []string
	for i := first; i < len(entries) && i < first+rows; i++ {
		e := entries[i]
		label := e.label
		if g := glyph(e.icon); g != "" {
			label = g + "  " + label
		}
		text, note, key := fg(cText), dim, fg(cAccent).Bold(true)
		if i == m.cursor { // a bar across the pane, no marker
			text = lipgloss.NewStyle().Foreground(cBase).Background(cAccent).Bold(true)
			note, key = text.UnsetBold(), text
		}
		label = " " + label
		if !m.wide() && e.note != "" { // a full-width menu has room for what each does
			label += strings.Repeat(" ", max(iconWidth+12-lipgloss.Width(label), 1))
			label = text.Render(label) + note.Render(e.note)
		} else {
			label = text.Render(label)
		}
		pad := strings.Repeat(" ", max(w-2-lipgloss.Width(label)-lipgloss.Width(e.key)-1, 1))
		lines = append(lines, label+text.Render(pad)+key.Render(e.key)+text.Render(" "))
	}
	return box(w, m.bodyHeight(), "", keys, strings.Join(lines, "\n"), 0)
}

// contentView frames the content as the menu is, the two the same height: the title on its top
// edge, the content from the line the first entry is on.
func (m model) contentView() string {
	body := m.vp.View()
	if m.form != nil {
		body = m.form.View()
	}
	body = lipgloss.NewStyle().MaxWidth(m.paneWidth()).MaxHeight(m.paneHeight()).Render(body)
	keys := ""
	if m.wide() || m.content {
		keys = m.keys()
	}
	return box(m.contentWidth(), m.bodyHeight(), m.title, keys, body, 1)
}

// statusline is the head as lualine draws it: the mode in a colored block, the state on a band
// across the screen, Connaio's name at the right end.
func (m model) statusline() string {
	band := lipgloss.NewStyle().Background(cSurface)
	edge := func(s string, f, b lipgloss.Color) string {
		st := lipgloss.NewStyle().Foreground(f)
		if b != "" {
			st = st.Background(b)
		}
		return st.Render(glyph(s))
	}
	icon := glyph(entries[1].icon)
	if icon != "" {
		icon += " "
	}
	// the ends half a cell in, a half block each: from the middle of the cell, where the panes' edges are drawn
	left := fg(m.head.color).Render("▐") + lipgloss.NewStyle().Foreground(cBase).Background(m.head.color).Bold(true).Render(icon+m.head.mode+" ") +
		edge("\ue0b4", m.head.color, cSurface)
	for i, p := range m.head.parts {
		if i > 0 {
			left += band.Foreground(cBorder).Render(" │")
		}
		left += band.Foreground(cMuted).Render(" "+p.k+" ") + band.Foreground(cmp.Or(p.c, cText)).Render(p.v)
	}
	right := edge("\ue0b6", cAccent, cSurface) + lipgloss.NewStyle().Foreground(cBase).Background(cAccent).Bold(true).Render(" Connaio") + fg(cAccent).Render("▌")
	gap := m.w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 { // no room for the name: the state cut to the screen, the band still to its edge
		left, right = trim(left, m.w-1), fg(cSurface).Render("▌")
		gap = m.w - lipgloss.Width(left) - 1
	}
	return left + band.Render(strings.Repeat(" ", gap)) + right // the band across the screen, as wide as the panes
}

// summary is the status line: the service's state and the core's, its mode and profile.
func summary() head {
	h := head{mode: "离线", color: cWarn}
	if systemd() {
		active, _ := exec.Command("systemctl", systemctl("is-active", "connaio")...).Output()
		enabled, _ := exec.Command("systemctl", systemctl("is-enabled", "connaio")...).Output()
		state := map[string]part{"active": {"服务", "运行中", cOK}, "inactive": {"服务", "未运行", cWarn}, "failed": {"服务", "出错", cErr}}
		svc, ok := state[strings.TrimSpace(string(active))]
		if !ok {
			svc = part{"服务", "未安装", cWarn}
		}
		boot := part{"开机启动", "否", cWarn}
		if strings.TrimSpace(string(enabled)) == "enabled" {
			boot = part{"开机启动", "是", ""}
		}
		h.parts = append(h.parts, svc, boot)
	} else if _, err := os.Stat(initScript); err == nil {
		svc := part{"服务", "运行中", cOK}
		if exec.Command(initScript, "status").Run() != nil {
			svc = part{"服务", "未运行", cWarn}
		}
		h.parts = append(h.parts, svc)
	}
	c, err := client0()
	if err != nil {
		h.parts = append(h.parts, part{"内核", "读不到访问令牌", cWarn})
		return h
	}
	var st struct {
		Running bool
		Version string
	}
	if c.call(&st, "CoreService.Status") != nil || !st.Running {
		h.mode, h.color = "内核未运行", cErr
		return h
	}
	h.parts = append(h.parts, part{"内核", st.Version, ""})
	var mode string
	if c.call(&mode, "CoreService.Mode") == nil {
		h.mode, h.color = cmp.Or(modeLabel[mode], mode), cmp.Or(modeColor[mode], cAccent)
	}
	var ps struct {
		Current  string
		Profiles []struct{ ID, Name string }
	}
	if c.call(&ps, "ProfileService.Profiles") == nil {
		for _, p := range ps.Profiles {
			if p.ID == ps.Current {
				h.parts = append(h.parts, part{"配置", p.Name, ""})
			}
		}
	}
	return h
}

// initScript and initLog are the service where there is no systemd, as install.sh puts it.
const (
	initScript = "/etc/init.d/connaio"
	initLog    = "/var/log/connaio.log"
)

// systemd says systemd runs this system: not so in most containers. On a user's own install, that
// the user's service is there: install.sh writes its unit only when systemctl --user works, and
// runs it in the background otherwise.
func systemd() bool {
	if home, err := os.UserHomeDir(); err == nil && own() {
		_, err = os.Stat(filepath.Join(home, ".config/systemd/user/connaio.service"))
		return err == nil
	}
	_, err := os.Stat("/run/systemd/system")
	return err == nil
}

// service starts, stops or restarts the service, through systemd or else its init script.
func service(action string) error {
	switch {
	case own() && !systemd():
		return errors.New("没有 systemd 用户服务：请再次运行安装命令启动 Connaio")
	case !systemd():
		return system(initScript, action)
	}
	return system("systemctl", systemctl(action, "connaio")...)
}

// logs prints the service's last 50 lines of log.
func logs() error {
	switch {
	case own() && systemd():
		return system("journalctl", "--user-unit", "connaio", "-n", "50", "--no-pager")
	case own():
		return system("tail", "-n", "50", filepath.Join(filepath.Dir(filepath.Dir(exe())), "connaio-web", "connaio.log"))
	case !systemd():
		return system("tail", "-n", "50", initLog)
	}
	return system("journalctl", "-u", "connaio", "-n", "50", "--no-pager")
}

// exe is this binary's path, links resolved.
func exe() string {
	p, _ := os.Executable()
	return p
}

// own says this is a user's own install, as install.sh puts one without root in ~/.local/share:
// its service is that user's (systemctl --user), run without sudo.
func own() bool {
	home, err := os.UserHomeDir()
	return err == nil && os.Geteuid() != 0 && strings.HasPrefix(exe(), filepath.Join(home, ".local/share/connaio")+"/")
}

// systemctl is args for systemctl, for the user's own service on a user's own install.
func systemctl(args ...string) []string {
	if own() {
		return append([]string{"--user"}, args...)
	}
	return args
}

// system runs a system command on the plain terminal, through sudo unless root or a user's own
// install: the service is root's.
func system(name string, args ...string) error {
	if os.Geteuid() != 0 && !own() {
		name, args = "sudo", append([]string{"-E", name}, args...)
	}
	c := exec.Command(name, args...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}
