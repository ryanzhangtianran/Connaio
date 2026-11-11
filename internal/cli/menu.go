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
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// installURL is install.sh on the download site: run again, it updates Connaio.
const installURL = "https://connaio-dl.ryanzhangtianran.top/install.sh"

// entries are the menu's lines: id, label, what it does. In the order the work goes: the state,
// the routing, the profiles, the VPNs and tunnels, TUN, then the service.
var entries = [][3]string{
	{"status", "运行状态", "内核、出站模式、配置与隧道"},
	{"mode", "出站模式", "规则、全局或直连"},
	{"select", "切换节点", "策略组当前使用的节点"},
	{"test", "测试延迟", "测试一个策略组的节点"},
	{"profiles", "配置列表", "订阅与本地配置"},
	{"use", "使用配置", "切换到另一个配置"},
	{"update", "更新订阅", "重新下载一个订阅"},
	{"add", "添加订阅", "订阅地址与名称"},
	{"vpns", "VPN", "各 VPN 的状态"},
	{"login", "登录 VPN", "订阅中待登录的 VPN"},
	{"logout", "断开 VPN", "退出登录，订阅中的回到待登录"},
	{"tunnels", "反向隧道", "启动或停止隧道"},
	{"enhanced", "TUN 模式", "接管全部流量"},
	{"partial", "部分 TUN", "只接管选中的程序"},
	{"connections", "当前连接", "经 Connaio 的连接"},
	{"start", "启动服务", "在终端中运行"},
	{"stop", "停止服务", "在终端中运行"},
	{"restart", "重启服务", "在终端中运行"},
	{"logs", "查看日志", "服务最近 50 行日志"},
	{"self-update", "更新 Connaio", "重新运行安装脚本"},
	{"help", "全部命令", "命令行的用法"},
	{"quit", "退出", ""},
}

const wideMin = 90 // below this the menu and the content take turns on screen

// the palette: catppuccin frappé, as homie's
var (
	cMuted, cBorder, cAccent, cTitle = lipgloss.Color("#a5adce"), lipgloss.Color("#737994"), lipgloss.Color("#85c1dc"), lipgloss.Color("#ca9ee6")
	cOK, cWarn, cErr, cText          = lipgloss.Color("#a6d189"), lipgloss.Color("#e5c890"), lipgloss.Color("#e78284"), lipgloss.Color("#c6d0f5")

	frame = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cBorder).Padding(0, 1)
	dim   = lipgloss.NewStyle().Foreground(cMuted)
	theme = formTheme()
)

func fg(c lipgloss.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

// formTheme is the forms' look from the palette: the whole form framed, no side bar on the fields.
func formTheme() *huh.Theme {
	t := huh.ThemeBase()
	t.Form.Base = frame
	f := &t.Focused
	f.Base, f.Card = lipgloss.NewStyle(), lipgloss.NewStyle()
	f.Title = fg(cTitle).Bold(true)
	f.Description = fg(cMuted)
	f.SelectSelector = fg(cAccent).SetString("› ")
	f.Option, f.UnselectedOption = fg(cText), fg(cText)
	f.SelectedOption = fg(cAccent).Bold(true)
	f.ErrorIndicator, f.ErrorMessage = fg(cErr).SetString(" *"), fg(cErr)
	f.FocusedButton = lipgloss.NewStyle().Padding(0, 2).MarginRight(1).Foreground(lipgloss.Color("#232634")).Background(cAccent)
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
// beside it; what runs the service (systemctl, sudo) gets the plain terminal while it runs.
func Menu(in io.Reader, out io.Writer) error {
	_, err := tea.NewProgram(newModel(), tea.WithAltScreen(), tea.WithInput(in), tea.WithOutput(out)).Run()
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
	head    string
	busy    bool
}

type (
	contentMsg struct{ title, body string }
	doneMsg    struct {
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

func newModel() model {
	return model{head: summary(), vp: viewport.New(0, 0), title: "运行状态", busy: true}
}

func (m model) Init() tea.Cmd { return capture("运行状态", "status") }

func (m model) wide() bool { return m.w >= wideMin }

// menuWidth fits the longest label, its selector and the frame.
func menuWidth() int {
	w := 0
	for _, e := range entries {
		w = max(w, lipgloss.Width(e[1]))
	}
	return w + 6
}

func (m model) contentWidth() int {
	if m.wide() {
		return m.w - menuWidth() - 1
	}
	return m.w
}

func (m model) bodyHeight() int {
	return max(m.h-lipgloss.Height(m.header())-2, 3) // header, then body, hint line and key line
}

func (m *model) layout() {
	m.vp.Width, m.vp.Height = m.contentWidth(), m.bodyHeight()-1 // one line for the pane title
	if m.form != nil {
		m.form = m.form.WithWidth(max(m.contentWidth()-4, 30))
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.layout()
		return m, nil
	case contentMsg:
		m.head = summary() // whatever just ran may have changed the mode, the profile or the service
		m.busy, m.title = false, msg.title
		m.vp.SetContent(msg.body)
		m.vp.GotoTop()
		return m, nil
	case doneMsg:
		body := fg(cOK).Render("✓ " + msg.title + "完成")
		if msg.err != nil {
			body = fg(cErr).Render("✗ " + msg.err.Error())
		}
		return m, show(msg.title, body+"\n\n"+dim.Render("输出已显示在终端中"))
	case formMsg:
		m.busy = false
		return m, m.openForm(msg.title, msg.submit, msg.fields...)
	}
	if m.form != nil {
		return m.updateForm(msg)
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "up", "k":
			if !m.content || m.wide() {
				m.cursor = (m.cursor + len(entries) - 1) % len(entries)
				return m, nil
			}
		case "down", "j":
			if !m.content || m.wide() {
				m.cursor = (m.cursor + 1) % len(entries)
				return m, nil
			}
		case "esc", "left", "h":
			m.content = false
			return m, nil
		case "enter", "right", "l":
			if m.busy {
				return m, nil
			}
			m.content = true
			return m, m.choose(entries[m.cursor][0])
		}
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg) // scrolling: pgup/pgdn, and arrows in the narrow content pane
	return m, cmd
}

func (m model) updateForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "esc" {
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
		return m, tea.Batch(cmd, submit())
	case huh.StateAborted:
		m.form, m.content = nil, false
	}
	return m, cmd
}

// openForm shows fields in the content pane; submit runs once they are filled in. submit must not
// touch m: Update works on copies, so what it finds comes back as a message.
func (m *model) openForm(title string, submit func() tea.Cmd, fields ...huh.Field) tea.Cmd {
	m.title, m.submit = title, submit
	m.form = huh.NewForm(huh.NewGroup(fields...)).WithTheme(theme).WithShowHelp(false) // the keys are on the bottom line
	m.layout()
	return m.form.Init()
}

// ask opens a form from a step's result, as a message: the step runs off the model.
func ask(title string, submit func() tea.Cmd, fields ...huh.Field) tea.Cmd {
	return func() tea.Msg { return formMsg{title, submit, fields} }
}

func show(title, body string) tea.Cmd {
	return func() tea.Msg { return contentMsg{title, body} }
}

// failed shows err in the content pane.
func failed(title string, err error) tea.Cmd { return show(title, fg(cErr).Render("✗ "+err.Error())) }

// capture runs a command as typed on the command line, in the background, and shows what it printed.
func capture(title string, args ...string) tea.Cmd {
	return captureFn(title, func(out io.Writer) error { return Main(args, out) })
}

func captureFn(title string, fn func(io.Writer) error) tea.Cmd {
	return func() tea.Msg {
		var b bytes.Buffer
		if err := fn(&b); err != nil {
			b.WriteString("\n" + fg(cErr).Render("✗ "+err.Error()))
		}
		body := strings.Trim(b.String(), "\n")
		if body == "" {
			body = fg(cOK).Render("✓ 完成")
		}
		return contentMsg{title, body}
	}
}

// termRun hands the plain terminal to fn (sudo asks for a password, the installer draws its own
// screen), then waits for Enter so its output can be read before the menu comes back.
type termRun func() error

func (f termRun) Run() error {
	err := f()
	if err != nil {
		fmt.Println(fg(cErr).Render("✗ " + err.Error()))
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

// choose runs a menu entry: reports render in the content pane, choices open a form there, and
// what runs the service gets the plain terminal for a moment.
func (m *model) choose(id string) tea.Cmd {
	label := id
	for _, e := range entries {
		if e[0] == id {
			label = e[1]
		}
	}
	switch id {
	case "quit":
		return tea.Quit
	case "status", "profiles", "vpns", "connections":
		m.busy = true
		return capture(label, id)
	case "help":
		return show(label, strings.TrimRight(Usage, "\n"))
	case "mode":
		mode := "rule"
		return m.openForm(label, func() tea.Cmd { return capture(label, "mode", mode) },
			huh.NewSelect[string]().Title("出站模式").Options(huh.NewOption("规则", "rule"), huh.NewOption("全局", "global"), huh.NewOption("直连", "direct")).Value(&mode))
	case "select", "test":
		return pickGroup(label, func(g group) tea.Cmd {
			if id == "test" {
				return capture(label, "test", g.Name)
			}
			node := g.Now
			var opts []huh.Option[string]
			for _, p := range g.All {
				opts = append(opts, huh.NewOption(p.Name+"  "+dim.Render(delay(p.Delay)), p.Name))
			}
			return ask(label, func() tea.Cmd { return capture(label, "select", g.Name, node) },
				huh.NewSelect[string]().Title(g.Name).Description("当前 "+cmp.Or(g.Now, "—")).Options(opts...).Value(&node))
		})
	case "use", "update":
		c, err := client0()
		if err != nil {
			return failed(label, err)
		}
		var ps struct {
			Current  string
			Profiles []struct{ ID, Name string }
		}
		if err := c.call(&ps, "ProfileService.Profiles"); err != nil {
			return failed(label, err)
		}
		if len(ps.Profiles) == 0 {
			return show(label, dim.Render("暂无配置"))
		}
		var opts []huh.Option[string]
		for _, p := range ps.Profiles {
			name := p.Name
			if p.ID == ps.Current {
				name += dim.Render("  使用中")
			}
			opts = append(opts, huh.NewOption(name, p.ID))
		}
		picked := ps.Current
		return m.openForm(label, func() tea.Cmd { return capture(label, "profile", id, picked) },
			huh.NewSelect[string]().Title("配置").Options(opts...).Value(&picked))
	case "add":
		link, name := "", ""
		return m.openForm(label, func() tea.Cmd { return capture(label, "profile", "add", link, name) },
			huh.NewInput().Title("订阅地址").Placeholder("https://…").Value(&link).Validate(func(s string) error {
				if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
					return errors.New("以 http:// 或 https:// 开头")
				}
				return nil
			}),
			huh.NewInput().Title("名称").Placeholder("留空用域名").Value(&name))
	case "login", "logout":
		return m.vpn(id, label)
	case "tunnels":
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
		return m.openForm(label, func() tea.Cmd {
			action := "stop"
			if tunnels[i].State == "stopped" {
				action = "start"
			}
			return capture(label, "tunnel", action, tunnels[i].Name)
		}, huh.NewSelect[int]().Title("要启动或停止的隧道").Options(opts...).Value(&i))
	case "enhanced":
		on := "on"
		return m.openForm(label, func() tea.Cmd { return capture(label, "enhanced", on) },
			huh.NewSelect[string]().Title("TUN 模式").Description(now("enhanced")).Options(huh.NewOption("开启", "on"), huh.NewOption("关闭", "off")).Value(&on))
	case "partial":
		action := "on"
		return m.openForm(label, func() tea.Cmd {
			switch action {
			case "add":
				name := ""
				return ask(label, func() tea.Cmd { return capture(label, "partial", "add", name) },
					huh.NewInput().Title("程序名").Placeholder("如 python、git、uv").Value(&name))
			case "remove":
				return removeProgram(label)
			}
			return capture(label, "partial", action)
		}, huh.NewSelect[string]().Title("部分 TUN").Description(now("partial")).
			Options(huh.NewOption("开启", "on"), huh.NewOption("关闭", "off"), huh.NewOption("添加程序", "add"), huh.NewOption("移除程序", "remove")).Value(&action))
	case "start", "stop", "restart":
		return runTerm(label, func() error { return service(id) })
	case "logs":
		return runTerm(label, logs)
	case "self-update":
		// -E under sudo: a proxy in the environment reaches install.sh's downloads too
		return runTerm(label, func() error { return system("sh", "-c", "curl -fsSL "+installURL+" | sh") })
	}
	return nil
}

// now is what a command prints, on one line, for a form's description: the setting as it is.
func now(args ...string) string {
	var b bytes.Buffer
	if err := Main(args, &b); err != nil {
		return err.Error()
	}
	return "当前：" + strings.Join(strings.Fields(b.String()), " ")
}

func client0() (*client, error) { return connect(defaultAddr(), os.Getenv("CONNAIO_TOKEN"), "") }

type group struct {
	Name, Type, Now string
	All             []struct {
		Name  string
		Delay int
	}
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
func (m *model) vpn(id, title string) tea.Cmd {
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
		if id == "login" && v.NeedsLogin || id == "logout" && v.Active && !v.NeedsLogin {
			opts = append(opts, huh.NewOption(fmt.Sprintf("%v  %s", v.Proxy["name"], dim.Render(fmt.Sprint(v.Proxy["type"]))), i))
		}
	}
	if len(opts) == 0 {
		return show(title, dim.Render(map[string]string{"login": "没有待登录的 VPN", "logout": "没有已连接的 VPN"}[id]))
	}
	i := opts[0].Value
	return m.openForm(title, func() tea.Cmd {
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
	}, huh.NewSelect[int]().Title("VPN").Options(opts...).Value(&i))
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
	hint := entries[m.cursor][2]
	keys := "↑↓ 移动 · 回车 打开 · pgup/pgdn 滚动 · q 退出"
	switch {
	case m.form != nil:
		hint, keys = "esc 取消", "回车 下一步 · shift+tab 上一步 · esc 取消"
	case !m.wide():
		keys = "↑↓ 移动 · 回车 打开 · esc 返回 · q 退出"
	}
	return lipgloss.JoinVertical(lipgloss.Left, m.header(),
		lipgloss.NewStyle().Height(m.bodyHeight()).MaxHeight(m.bodyHeight()).Render(body),
		trim(dim.Render(hint), m.w), trim(dim.Render(keys), m.w))
}

func trim(s string, w int) string { return lipgloss.NewStyle().MaxWidth(w).Render(s) }

func (m model) header() string { return frame.Width(max(m.w-2, 10)).Render(m.head) }

func (m model) menuView() string {
	w := menuWidth()
	if !m.wide() {
		w = m.w
	}
	rows := m.bodyHeight() - 2
	first := max(0, min(m.cursor-rows/2, len(entries)-rows)) // the cursor kept in view on short windows
	var lines []string
	for i := first; i < len(entries) && i < first+rows; i++ {
		label, style := "  "+entries[i][1], fg(cText)
		if i == m.cursor {
			label, style = "› "+entries[i][1], fg(cAccent).Bold(true)
		}
		if !m.wide() && entries[i][2] != "" { // a full-width menu has room for what each does
			label += strings.Repeat(" ", max(16-lipgloss.Width(label), 1)) + dim.Render(entries[i][2])
		}
		lines = append(lines, style.Render(label))
	}
	return frame.Width(w - 2).Height(rows).Render(strings.Join(lines, "\n"))
}

func (m model) contentView() string {
	title := fg(cTitle).Bold(true).Render(m.title)
	if m.form != nil {
		return title + "\n" + m.form.View()
	}
	return title + "\n" + lipgloss.NewStyle().MaxWidth(m.contentWidth()).Render(m.vp.View())
}

// summary is the header: the service's state and the core's, its mode and profile.
func summary() string {
	sep := dim.Render("  ·  ")
	label := func(k, v string) string { return dim.Render(k+" ") + v }
	parts := []string{fg(cAccent).Bold(true).Render("Connaio")}
	if systemd() {
		active, _ := exec.Command("systemctl", systemctl("is-active", "connaio")...).Output()
		enabled, _ := exec.Command("systemctl", systemctl("is-enabled", "connaio")...).Output()
		state := map[string]string{"active": fg(cOK).Render("运行中"), "inactive": fg(cWarn).Render("未运行"), "failed": fg(cErr).Render("出错")}
		boot := fg(cWarn).Render("否")
		if strings.TrimSpace(string(enabled)) == "enabled" {
			boot = "是"
		}
		parts = append(parts, label("服务", cmp.Or(state[strings.TrimSpace(string(active))], fg(cWarn).Render("未安装"))), label("开机启动", boot))
	} else if _, err := os.Stat(initScript); err == nil {
		state := fg(cOK).Render("运行中")
		if exec.Command(initScript, "status").Run() != nil {
			state = fg(cWarn).Render("未运行")
		}
		parts = append(parts, label("服务", state))
	}
	c, err := client0()
	if err != nil {
		return strings.Join(append(parts, label("内核", fg(cWarn).Render("读不到访问令牌"))), sep)
	}
	var st struct {
		Running bool
		Version string
	}
	if c.call(&st, "CoreService.Status") != nil || !st.Running {
		return strings.Join(append(parts, label("内核", fg(cErr).Render("未运行"))), sep)
	}
	parts = append(parts, label("内核", fg(cOK).Render("运行中")+dim.Render(" "+st.Version)))
	var mode string
	if c.call(&mode, "CoreService.Mode") == nil {
		parts = append(parts, label("出站", cmp.Or(modeLabel[mode], mode)))
	}
	var ps struct {
		Current  string
		Profiles []struct{ ID, Name string }
	}
	if c.call(&ps, "ProfileService.Profiles") == nil {
		for _, p := range ps.Profiles {
			if p.ID == ps.Current {
				parts = append(parts, label("配置", p.Name))
			}
		}
	}
	return strings.Join(parts, sep)
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
