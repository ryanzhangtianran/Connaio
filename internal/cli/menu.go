package cli

import (
	"bufio"
	"cmp"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// installURL is install.sh on the download site: run again, it updates Connaio.
const installURL = "https://connaio-dl.ryanzhangtianran.top/install.sh"

// item is a line of the menu: a rule between sections when run is nil.
type item struct {
	label string
	run   func(m *menu) error
}

var items = []item{
	{},
	{"启动服务", func(m *menu) error { return m.service("start") }},
	{"停止服务", func(m *menu) error { return m.service("stop") }},
	{"重启服务", func(m *menu) error { return m.service("restart") }},
	{"查看日志", func(m *menu) error {
		if !systemd() {
			return m.system("tail", "-n", "50", initLog)
		}
		return m.system("journalctl", "-u", "connaio", "-n", "50", "--no-pager")
	}},
	// -E under sudo: a proxy in the environment reaches install.sh's downloads too
	{"更新 Connaio", func(m *menu) error { return m.system("sh", "-c", "curl -fsSL "+installURL+" | sh") }},
	{},
	{"运行状态", func(m *menu) error { return m.cmd("status") }},
	{"出站模式", (*menu).mode},
	{"切换节点", (*menu).selectNode},
	{"测试延迟", (*menu).test},
	{},
	{"配置列表", func(m *menu) error { return m.cmd("profiles") }},
	{"添加订阅", (*menu).addProfile},
	{"使用配置", func(m *menu) error { return m.profile("use") }},
	{"更新订阅", func(m *menu) error { return m.profile("update") }},
	{},
	{"反向隧道", (*menu).tunnels},
	{"增强模式", (*menu).enhanced},
	{"当前连接", func(m *menu) error { return m.cmd("connections") }},
	{"网页界面地址", func(m *menu) error { return m.cmd("url") }},
}

type menu struct {
	in  *bufio.Scanner
	out io.Writer
}

// Menu is connaio run bare in a terminal, as x-ui's: the commands as a numbered menu, the
// service's own (systemctl, or /etc/init.d/connaio) besides, until 0 or the input ends.
func Menu(in io.Reader, out io.Writer) error {
	m := &menu{in: bufio.NewScanner(in), out: out}
	var actions []item
	for _, it := range items {
		if it.run != nil {
			actions = append(actions, it)
		}
	}
	for {
		m.draw()
		s, ok := m.ask(fmt.Sprintf("请选择 [0-%d]：", len(actions)))
		if !ok || s == "0" {
			return nil
		}
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > len(actions) {
			fmt.Fprintln(out, red("无效的选项"))
			continue
		}
		fmt.Fprintln(out)
		if err := actions[n-1].run(m); err != nil {
			fmt.Fprintln(out, red("错误："+err.Error()))
		}
		if _, ok := m.ask("\n按回车返回菜单"); !ok {
			return nil
		}
	}
}

func (m *menu) draw() {
	const width = 36
	line := func(colored, plain string) {
		fmt.Fprintf(m.out, "│  %s%s│\n", colored, strings.Repeat(" ", max(width-displayWidth(plain), 0)))
	}
	rule := strings.Repeat("─", width+2)
	fmt.Fprintf(m.out, "\n╔%s╗\n", rule)
	const title = "Connaio 管理菜单"
	pad := width + 2 - displayWidth(title)
	fmt.Fprintf(m.out, "│%s%s%s│\n", strings.Repeat(" ", pad/2), title, strings.Repeat(" ", pad-pad/2))
	line(green(" 0.")+" 退出", " 0. 退出")
	n := 0
	for _, it := range items {
		if it.run == nil {
			fmt.Fprintf(m.out, "│%s│\n", rule)
			continue
		}
		n++
		num := fmt.Sprintf("%2d.", n)
		line(green(num)+" "+it.label, num+" "+it.label)
	}
	fmt.Fprintf(m.out, "╚%s╝\n\n", rule)
	if systemd() {
		active, _ := exec.Command("systemctl", "is-active", "connaio").Output()
		enabled, _ := exec.Command("systemctl", "is-enabled", "connaio").Output()
		state := map[string]string{"active": green("运行中"), "inactive": yellow("未运行"), "failed": red("出错")}
		fmt.Fprintln(m.out, "服务状态："+cmp.Or(state[strings.TrimSpace(string(active))], yellow("未安装")))
		fmt.Fprintln(m.out, "开机启动："+map[bool]string{true: green("是"), false: red("否")}[strings.TrimSpace(string(enabled)) == "enabled"])
	} else if _, err := os.Stat(initScript); err == nil {
		state := green("运行中")
		if exec.Command(initScript, "status").Run() != nil {
			state = yellow("未运行")
		}
		fmt.Fprintln(m.out, "服务状态："+state)
	}
	core := red("未运行")
	if c, err := m.client(); err != nil {
		core = yellow("读不到访问令牌")
	} else {
		var st struct {
			Running bool
			Version string
		}
		if c.call(&st, "CoreService.Status") == nil && st.Running {
			core = green("运行中（" + st.Version + "）")
		}
	}
	fmt.Fprintln(m.out, "内核状态："+core)
	fmt.Fprintln(m.out)
}

// ask prints prompt and reads a line; false when the input has ended.
func (m *menu) ask(prompt string) (string, bool) {
	fmt.Fprint(m.out, prompt)
	if !m.in.Scan() {
		fmt.Fprintln(m.out)
		return "", false
	}
	return strings.TrimSpace(m.in.Text()), true
}

// pick lists rows numbered, their columns aligned, and returns the index chosen, -1 for none.
func (m *menu) pick(what string, rows [][]string) int {
	if len(rows) == 0 {
		fmt.Fprintln(m.out, "暂无"+what)
		return -1
	}
	tw := &table{out: m.out}
	for i, r := range rows {
		tw.row(append([]string{green(fmt.Sprintf("%3d.", i+1))}, r...)...) // the same colour codes on every row: still aligned
	}
	tw.flush()
	s, _ := m.ask("选择" + what + "（回车取消）：")
	i, err := strconv.Atoi(s)
	if err != nil || i < 1 || i > len(rows) {
		return -1
	}
	return i - 1
}

// cmd runs a command as typed on the command line.
func (m *menu) cmd(args ...string) error { return Main(args, m.out) }

func (m *menu) client() (*client, error) {
	return connect(defaultAddr(), os.Getenv("CONNAIO_TOKEN"), "")
}

// initScript and initLog are the service where there is no systemd, as install.sh puts it.
const (
	initScript = "/etc/init.d/connaio"
	initLog    = "/var/log/connaio.log"
)

// systemd says systemd runs this system: not so in most containers.
func systemd() bool {
	_, err := os.Stat("/run/systemd/system")
	return err == nil
}

// service starts, stops or restarts the service, through systemd or else its init script.
func (m *menu) service(action string) error {
	if !systemd() {
		return m.system(initScript, action)
	}
	return m.system("systemctl", action, "connaio")
}

// system runs a system command on the terminal, through sudo unless root: the service is root's.
func (m *menu) system(name string, args ...string) error {
	if os.Geteuid() != 0 {
		name, args = "sudo", append([]string{"-E", name}, args...)
	}
	c := exec.Command(name, args...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, m.out, m.out
	return c.Run()
}

func (m *menu) mode() error {
	keys := []string{"rule", "global", "direct"}
	if i := m.pick("出站模式", [][]string{{"规则"}, {"全局"}, {"直连"}}); i >= 0 {
		return m.cmd("mode", keys[i])
	}
	return nil
}

type group struct {
	Name, Type, Now string
	All             []struct {
		Name  string
		Delay int
	}
}

// group lists the strategy groups to pick one.
func (m *menu) group() (*group, error) {
	c, err := m.client()
	if err != nil {
		return nil, err
	}
	var all, groups []group
	if err := c.call(&all, "CoreService.Groups"); err != nil {
		return nil, err
	}
	var rows [][]string
	for _, g := range all {
		if g.Name != "GLOBAL" {
			groups = append(groups, g)
			rows = append(rows, []string{g.Name, "→ " + cmp.Or(g.Now, "—")})
		}
	}
	if i := m.pick("策略组", rows); i >= 0 {
		return &groups[i], nil
	}
	return nil, nil
}

func (m *menu) selectNode() error {
	g, err := m.group()
	if g == nil {
		return err
	}
	var rows [][]string
	for _, p := range g.All {
		rows = append(rows, []string{mark(p.Name == g.Now), p.Name, delay(p.Delay)})
	}
	fmt.Fprintln(m.out)
	if i := m.pick("节点", rows); i >= 0 {
		return m.cmd("select", g.Name, g.All[i].Name)
	}
	return nil
}

func (m *menu) test() error {
	g, err := m.group()
	if g == nil {
		return err
	}
	fmt.Fprintln(m.out, "\n测试中…")
	return m.cmd("test", g.Name)
}

func (m *menu) addProfile() error {
	link, _ := m.ask("订阅地址：")
	if link == "" {
		return nil
	}
	name, _ := m.ask("名称（回车用域名）：")
	return m.cmd("profile", "add", link, name)
}

func (m *menu) profile(action string) error {
	c, err := m.client()
	if err != nil {
		return err
	}
	var profiles struct {
		Current  string
		Profiles []struct{ ID, Name string }
	}
	if err := c.call(&profiles, "ProfileService.Profiles"); err != nil {
		return err
	}
	var rows [][]string
	for _, p := range profiles.Profiles {
		rows = append(rows, []string{mark(p.ID == profiles.Current), p.Name})
	}
	if i := m.pick("配置", rows); i >= 0 {
		return m.cmd("profile", action, profiles.Profiles[i].ID)
	}
	return nil
}

// tunnels lists the reverse tunnels; the one picked is stopped, or started if stopped.
func (m *menu) tunnels() error {
	c, err := m.client()
	if err != nil {
		return err
	}
	var tunnels []struct{ Name, State string }
	if err := c.call(&tunnels, "CoreService.ReverseTunnels"); err != nil {
		return err
	}
	var rows [][]string
	for _, t := range tunnels {
		rows = append(rows, []string{t.Name, cmp.Or(stateLabel[t.State], t.State)})
	}
	i := m.pick("要启动或停止的隧道", rows)
	if i < 0 {
		return nil
	}
	action := "stop"
	if tunnels[i].State == "stopped" {
		action = "start"
	}
	return m.cmd("tunnel", action, tunnels[i].Name)
}

func (m *menu) enhanced() error {
	if err := m.cmd("enhanced"); err != nil {
		return err
	}
	if i := m.pick("操作", [][]string{{"开启"}, {"关闭"}}); i >= 0 {
		return m.cmd("enhanced", []string{"on", "off"}[i])
	}
	return nil
}

// mark is * for the one in use.
func mark(current bool) string {
	if current {
		return "*"
	}
	return " "
}

func green(s string) string  { return "\033[32m" + s + "\033[0m" }
func yellow(s string) string { return "\033[33m" + s + "\033[0m" }
func red(s string) string    { return "\033[31m" + s + "\033[0m" }
