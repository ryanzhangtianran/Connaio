// Package cli is the headless build's command line: commands that drive a running instance through
// its web front, calling the services the web UI calls (POST /wails/runtime, by method name).
package cli

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Usage is the command line's help, printed by connaio help.
const Usage = `用法：connaio [命令] [选项] [参数]

在终端中不带参数时打开管理菜单；带选项时运行 Connaio（--listen 127.0.0.1:9090 --data-dir 目录 --token 令牌）。
以下命令控制运行中的 Connaio，选项写在参数之前：--addr 地址（默认 127.0.0.1:9090）、
--token 令牌（默认读服务数据目录或 ~/.config/Connaio 中的 web-token）、--data-dir 目录。

  status                       运行状态
  mode [rule|global|direct]    查看或切换出站模式
  groups [-a]                  策略组及当前节点（-a 列出全部节点）
  select <策略组> <节点>        切换节点
  test <策略组>                 测试策略组延迟
  profiles                     配置列表
  profile use <名称>            使用配置
  profile update <名称>         更新订阅
  profile add <URL> [名称]      添加订阅
  tunnels                      反向隧道
  tunnel start|stop <名称>      启动或停止隧道
  vpns                         VPN 状态
  vpn reconnect <名称>          重新连接 VPN
  enhanced [on|off]            查看、开启或关闭 TUN 模式
  connections                  当前连接
  close-all                    断开全部连接
  restart                      重启内核
  url                          网页界面地址（含令牌）
  run <命令> [参数]             经代理运行命令，无需 export
`

// svc prefixes a service method's name as Wails binds it.
const svc = "github.com/ryanzhangtianran/Connaio/internal/app."

// client calls a running instance's services through its web front at base with token.
type client struct {
	base, token string
	http        *http.Client
}

// call calls svc+method with args into out: a string result comes as text, the rest as JSON.
func (c *client) call(out any, method string, args ...any) error {
	if args == nil {
		args = []any{}
	}
	body, err := json.Marshal(map[string]any{"object": 0, "method": 0, "args": map[string]any{"call-id": "cli", "methodName": svc + method, "args": args}})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, c.base+"/wails/runtime", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-wails-client-id", "cli")
	req.AddCookie(&http.Cookie{Name: "connaio_token", Value: c.token})
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("连不上 Connaio（%s）：%w", c.base, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return errors.New("访问令牌不正确：用 sudo 运行，或用 --token 指定")
	case resp.StatusCode != http.StatusOK:
		var e struct{ Message string }
		if json.Unmarshal(b, &e) == nil && e.Message != "" {
			return errors.New(e.Message)
		}
		return fmt.Errorf("%s：%s", method, strings.TrimSpace(string(b)))
	}
	if s, ok := out.(*string); ok && strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain") {
		*s = string(b)
		return nil
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(b, out)
}

// token is the given one, else the one in the data directory: dir's, else the system service's,
// else the user's.
func token(given, dir string) (string, error) {
	if given != "" {
		return given, nil
	}
	var dirs []string
	if dir != "" {
		dirs = []string{dir}
	} else {
		dirs = []string{"/var/lib/connaio-web"}
		if c, err := os.UserConfigDir(); err == nil {
			dirs = append(dirs, filepath.Join(c, "Connaio"))
		}
	}
	var errs []error
	for _, d := range dirs {
		b, err := os.ReadFile(filepath.Join(d, "web-token"))
		if err == nil {
			return strings.TrimSpace(string(b)), nil
		}
		errs = append(errs, err)
	}
	return "", fmt.Errorf("读不到访问令牌（试试 sudo，或用 --token 指定）：%w", errors.Join(errs...))
}

// Main runs one command, args after the command's name included, writing to out.
func Main(args []string, out io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(out, Usage)
		return nil
	}
	if args[0] == "run" { // what follows is the command's own, flags too: nothing parsed here
		return runProxied(args[1:])
	}
	name := args[0]
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	addr := fs.String("addr", defaultAddr(), "")
	given := fs.String("token", os.Getenv("CONNAIO_TOKEN"), "")
	dir := fs.String("data-dir", "", "")
	all := fs.Bool("a", false, "")
	// flags anywhere: before, between or after the arguments
	var positional []string
	for rest := args[1:]; ; {
		if err := fs.Parse(rest); err != nil {
			return fmt.Errorf("%w\n\n%s", err, Usage)
		}
		if rest = fs.Args(); len(rest) == 0 {
			break
		}
		positional, rest = append(positional, rest[0]), rest[1:]
	}
	c, err := connect(*addr, *given, *dir)
	if err != nil {
		return err
	}
	return run(c, name, positional, *all, *addr, out)
}

// listenFile is where install.sh keeps the service's listen address, read by its unit and init script.
const listenFile = "/etc/default/connaio"

// defaultAddr is the running instance's address: CONNAIO_ADDR, else the service's.
func defaultAddr() string { return cmp.Or(os.Getenv("CONNAIO_ADDR"), listenAddr(listenFile)) }

// listenAddr is where to reach the address file's CONNAIO_LISTEN: on loopback when it is every
// address (0.0.0.0, [::] or no host); 127.0.0.1:9090 without one.
func listenAddr(file string) string {
	b, _ := os.ReadFile(file)
	for _, l := range strings.Split(string(b), "\n") {
		v, ok := strings.CutPrefix(strings.TrimSpace(l), "CONNAIO_LISTEN=")
		if !ok {
			continue
		}
		host, port, err := net.SplitHostPort(strings.Trim(v, `"'`))
		if err != nil {
			break
		}
		if ip := net.ParseIP(host); host == "" || ip != nil && ip.IsUnspecified() {
			host = "127.0.0.1"
		}
		return net.JoinHostPort(host, port)
	}
	return "127.0.0.1:9090"
}

// connect is a client of the instance at addr, with the token given or the data directory's.
func connect(addr, given, dir string) (*client, error) {
	tok, err := token(given, dir)
	if err != nil {
		return nil, err
	}
	// minutes: adding a subscription downloads it, checks it and has the core load it (core.Reload)
	return &client{base: "http://" + addr, token: tok, http: &http.Client{Timeout: 3 * time.Minute}}, nil
}

var (
	modeLabel  = map[string]string{"rule": "规则", "global": "全局", "direct": "直连"}
	stateLabel = map[string]string{"running": "运行中", "connecting": "连接中", "retrying": "重试中", "stopped": "已停止"}
)

func need(args []string, n int, form string) error {
	if len(args) < n {
		return fmt.Errorf("用法：connaio %s", form)
	}
	return nil
}

// run runs command name with its arguments through c.
func run(c *client, name string, args []string, all bool, addr string, out io.Writer) error {
	tw := &table{out: out}
	defer tw.flush()
	switch name {
	case "status":
		var st struct {
			Running bool
			Version string
			Error   string
		}
		var mode, enhanced string
		var profiles struct {
			Current  string
			Profiles []struct{ ID, Name string }
		}
		var tunnels []struct{ Name, State string }
		if err := c.call(&st, "CoreService.Status"); err != nil {
			return err
		}
		if st.Running { // the rest asks the core
			for _, e := range []error{c.call(&mode, "CoreService.Mode"), c.call(&enhanced, "EnhancedService.Mode"),
				c.call(&profiles, "ProfileService.Profiles"), c.call(&tunnels, "CoreService.ReverseTunnels")} {
				if e != nil {
					return e
				}
			}
		}
		core := "未运行"
		if st.Running {
			core = "运行中（" + st.Version + "）"
		} else if st.Error != "" {
			core = "未运行：" + st.Error
		}
		profile := "—"
		for _, p := range profiles.Profiles {
			if p.ID == profiles.Current {
				profile = p.Name
			}
		}
		tw.rule()
		tw.row("内核", core)
		tw.row("出站模式", cmp.Or(modeLabel[mode], mode, "—"))
		tw.row("TUN 模式", enhancedLabel(enhanced))
		tw.row("配置", profile)
		tw.rule()
		for _, t := range tunnels {
			tw.row("隧道 "+t.Name, cmp.Or(stateLabel[t.State], t.State))
		}
		if len(tunnels) > 0 {
			tw.rule()
		}
		tw.row("网页界面", "http://"+addr)
		tw.rule()
	case "mode":
		if len(args) == 0 {
			var mode string
			if err := c.call(&mode, "CoreService.Mode"); err != nil {
				return err
			}
			fmt.Fprintln(out, cmp.Or(modeLabel[mode], mode))
			return nil
		}
		mode := args[0]
		for k, v := range modeLabel {
			if v == mode {
				mode = k
			}
		}
		if modeLabel[mode] == "" {
			return errors.New("用法：connaio mode rule|global|direct")
		}
		return c.call(nil, "CoreService.SetMode", mode)
	case "groups":
		var groups []struct {
			Name, Type, Now string
			All             []struct {
				Name  string
				Delay int
			}
		}
		if err := c.call(&groups, "CoreService.Groups"); err != nil {
			return err
		}
		for _, g := range groups {
			if g.Name == "GLOBAL" {
				continue
			}
			tw.row(g.Name, g.Type, "→ "+cmp.Or(g.Now, "—"))
			if all {
				for _, p := range g.All {
					mark := " "
					if p.Name == g.Now {
						mark = "*"
					}
					tw.row("  "+mark+" "+p.Name, delay(p.Delay))
				}
			}
		}
	case "select":
		if err := need(args, 2, "select <策略组> <节点>"); err != nil {
			return err
		}
		return c.call(nil, "CoreService.Select", args[0], args[1])
	case "test":
		if err := need(args, 1, "test <策略组>"); err != nil {
			return err
		}
		var delays map[string]int
		if err := c.call(&delays, "CoreService.TestGroup", args[0]); err != nil {
			return err
		}
		names := make([]string, 0, len(delays))
		for n := range delays {
			names = append(names, n)
		}
		slices.SortFunc(names, func(a, b string) int {
			return cmp.Or(cmp.Compare(rank(delays[a]), rank(delays[b])), strings.Compare(a, b))
		})
		for _, n := range names {
			tw.row(n, delay(delays[n]))
		}
	case "profiles", "profile":
		var profiles struct {
			Current  string
			Profiles []struct {
				ID, Name, URL string
				UpdatedAt     time.Time
			}
		}
		if err := c.call(&profiles, "ProfileService.Profiles"); err != nil {
			return err
		}
		if name == "profiles" {
			for _, p := range profiles.Profiles {
				mark, source := " ", cmp.Or(p.URL, "本地")
				if p.ID == profiles.Current {
					mark = "*"
				}
				tw.row(mark+" "+p.Name, source, p.UpdatedAt.Local().Format("2006-01-02 15:04"))
			}
			return nil
		}
		if len(args) >= 2 && args[0] == "add" {
			n := cmp.Or(strings.Join(args[2:], " "), hostOf(args[1]))
			return c.call(nil, "ProfileService.Add", n, args[1])
		}
		if err := need(args, 2, "profile use|update <名称>"); err != nil {
			return err
		}
		id := ""
		for _, p := range profiles.Profiles {
			if p.ID == args[1] || p.Name == args[1] {
				id = p.ID
			}
		}
		if id == "" {
			return fmt.Errorf("没有名为 %s 的配置", args[1])
		}
		switch args[0] {
		case "use":
			return c.call(nil, "ProfileService.Select", id)
		case "update":
			return c.call(nil, "ProfileService.Update", id)
		}
		return errors.New("用法：connaio profile use|update <名称>，或 profile add <URL> [名称]")
	case "tunnels":
		var tunnels []struct {
			Name, State, Error string
			Forwards           []struct{ Remote, Local string }
		}
		if err := c.call(&tunnels, "CoreService.ReverseTunnels"); err != nil {
			return err
		}
		if len(tunnels) == 0 {
			fmt.Fprintln(out, "暂无隧道")
		}
		for _, t := range tunnels {
			tw.row(t.Name, cmp.Or(stateLabel[t.State], t.State), t.Error)
			for _, f := range t.Forwards {
				tw.row("  " + f.Remote + " → " + f.Local)
			}
		}
	case "tunnel":
		if err := need(args, 2, "tunnel start|stop <名称>"); err != nil {
			return err
		}
		if args[0] != "start" && args[0] != "stop" {
			return errors.New("用法：connaio tunnel start|stop <名称>")
		}
		return c.call(nil, "ProfileService.SetTunnelDisabled", args[1], args[0] == "stop")
	case "vpns":
		var vpns []struct {
			Proxy  map[string]any
			State  string
			Active bool
		}
		if err := c.call(&vpns, "ProfileService.VPNs"); err != nil {
			return err
		}
		if len(vpns) == 0 {
			fmt.Fprintln(out, "暂无 VPN")
		}
		for _, v := range vpns {
			tw.row(fmt.Sprint(v.Proxy["name"]), fmt.Sprint(v.Proxy["type"]), cmp.Or(v.State, "—"))
		}
	case "vpn":
		if err := need(args, 2, "vpn reconnect <名称>"); err != nil {
			return err
		}
		if args[0] != "reconnect" {
			return errors.New("用法：connaio vpn reconnect <名称>")
		}
		return c.call(nil, "ProfileService.ReconnectVPN", args[1])
	case "enhanced":
		if len(args) == 0 {
			var mode string
			if err := c.call(&mode, "EnhancedService.Mode"); err != nil {
				return err
			}
			fmt.Fprintln(out, enhancedLabel(mode))
			return nil
		}
		if args[0] != "on" && args[0] != "off" {
			return errors.New("用法：connaio enhanced on|off")
		}
		return c.call(nil, "EnhancedService.SetFull", args[0] == "on")
	case "connections":
		var conns []struct {
			Process, App, Source, Host, Port, Rule, Chain string
			Upload, Download                              int64
		}
		if err := c.call(&conns, "CoreService.Connections"); err != nil {
			return err
		}
		if len(conns) == 0 {
			fmt.Fprintln(out, "当前没有连接")
		}
		for _, x := range conns {
			tw.row(cmp.Or(x.App, x.Process, x.Source, "—"), x.Host+":"+x.Port, cmp.Or(x.Rule, "—"), cmp.Or(x.Chain, "DIRECT"), "↑"+size(x.Upload)+" ↓"+size(x.Download))
		}
	case "close-all":
		return c.call(nil, "CoreService.CloseAll")
	case "restart":
		return c.call(nil, "CoreService.Restart")
	case "url":
		fmt.Fprintf(out, "http://%s/?token=%s\n", addr, url.QueryEscape(c.token))
	default:
		return fmt.Errorf("没有 %s 这个命令\n\n%s", name, Usage)
	}
	return nil
}

// table writes rows with their columns aligned by display width: tabwriter counts a Chinese
// character as one column where the terminal shows two.
type table struct {
	out  io.Writer
	rows [][]string
}

func (t *table) row(cells ...string) { t.rows = append(t.rows, cells) }

// rule adds a line of dashes, as wide as the table.
func (t *table) rule() { t.rows = append(t.rows, nil) }

func (t *table) flush() {
	var widths []int
	for _, r := range t.rows {
		for i, c := range r {
			if i == len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], displayWidth(c))
		}
	}
	width := 2 * max(len(widths)-1, 0)
	for _, w := range widths {
		width += w
	}
	for _, r := range t.rows {
		if r == nil {
			fmt.Fprintln(t.out, strings.Repeat("-", max(width, 32)))
			continue
		}
		var b strings.Builder
		for i, c := range r {
			b.WriteString(c)
			if i < len(r)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-displayWidth(c)+2))
			}
		}
		fmt.Fprintln(t.out, strings.TrimRight(b.String(), " "))
	}
	t.rows = nil
}

// displayWidth is how many columns s takes in a terminal: two for East Asian wide characters.
func displayWidth(s string) int {
	n := 0
	for _, r := range s {
		switch {
		case r >= 0x1100 && r <= 0x115F, r >= 0x2E80 && r <= 0xA4CF, r >= 0xAC00 && r <= 0xD7A3, r >= 0xF900 && r <= 0xFAFF,
			r >= 0xFE30 && r <= 0xFE4F, r >= 0xFF00 && r <= 0xFF60, r >= 0xFFE0 && r <= 0xFFE6, r >= 0x1F300 && r <= 0x1FAFF, r >= 0x20000:
			n += 2
		default:
			n++
		}
	}
	return n
}

func enhancedLabel(mode string) string {
	switch mode {
	case "tun":
		return "开启（全部流量）"
	case "process-tun":
		return "部分应用"
	}
	return "关闭"
}

// rank puts failed tests (0) last.
func rank(ms int) int {
	if ms <= 0 {
		return 1 << 30
	}
	return ms
}

func delay(ms int) string {
	if ms <= 0 {
		return "—"
	}
	return fmt.Sprintf("%d ms", ms)
}

func size(b int64) string {
	switch {
	case b < 1000:
		return fmt.Sprintf("%d B", b)
	case b < 1000*1000:
		return fmt.Sprintf("%.0f KB", float64(b)/1000)
	case b < 1000*1000*1000:
		return fmt.Sprintf("%.1f MB", float64(b)/1e6)
	}
	return fmt.Sprintf("%.2f GB", float64(b)/1e9)
}

// hostOf names a subscription after its link's host.
func hostOf(link string) string {
	if u, err := url.Parse(link); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return "订阅"
}
