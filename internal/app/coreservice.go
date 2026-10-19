package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ryanzhangtianran/Connaio/internal/core"
	"github.com/ryanzhangtianran/Connaio/internal/daemon"
	"github.com/ryanzhangtianran/Connaio/internal/run"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type Traffic struct {
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
}

type Status struct {
	Running bool   `json:"running"`
	Version string `json:"version"`
	Error   string `json:"error"`
}

type Proxy struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Delay int    `json:"delay"` // ms of the last test, 0 if untested or failed
}

type Group struct {
	Name    string            `json:"name"`
	Type    string            `json:"type"`
	Now     string            `json:"now"`
	All     []Proxy           `json:"all"`
	Default string            `json:"default,omitempty"` // Subnet: the member when no rule matches
	Rules   map[string]string `json:"rules,omitempty"`   // Subnet: "SSID:…", "ROUTER:…", CIDR, "TYPE:…" -> member
}

func init() {
	application.RegisterEvent[Traffic]("traffic")
	application.RegisterEvent[Status]("status")
	application.RegisterEvent[string]("mode")
}

// emit sends an event to the frontend; a no-op without a running app (tests).
func init() {
	application.RegisterEvent[Event]("event")
	application.RegisterEvent[string]("navigate") // page key the window should show, e.g. from the menu bar
}

// Event is something worth telling the user, listed under the topbar's events button.
type Event struct {
	Time  string `json:"time"`  // 2026/10/8 19:27:19
	Level string `json:"level"` // info, warning or error
	Text  string `json:"text"`
}

var eventLog struct {
	sync.Mutex
	list []Event // newest last, at most 50
}

// notify logs an event and adds it to the window's event list.
func notify(level, format string, args ...any) {
	e := Event{Time: time.Now().Format("2006/1/2 15:04:05"), Level: level, Text: fmt.Sprintf(format, args...)}
	eventLog.Lock()
	if n := len(eventLog.list); n > 0 && eventLog.list[n-1].Level == level && eventLog.list[n-1].Text == e.Text {
		eventLog.Unlock()
		return // the same thing again, e.g. a poll failing every few seconds while the core is down
	}
	log.Printf("%s: %s", level, e.Text)
	eventLog.list = append(eventLog.list[max(0, len(eventLog.list)-49):], e)
	eventLog.Unlock()
	emit("event", e)
}

// Notify lists something the window has to tell under the events button.
func (s *CoreService) Notify(level, text string) { notify(level, "%s", text) }

func (s *CoreService) Events() []Event {
	eventLog.Lock()
	defer eventLog.Unlock()
	return slices.Clone(eventLog.list)
}

func (s *CoreService) ClearEvents() {
	eventLog.Lock()
	eventLog.list = nil
	eventLog.Unlock()
}

func emit(name string, data any) {
	if app := application.Get(); app != nil {
		app.Event.Emit(name, data)
	}
}

// CoreService owns the connaio-core child process and exposes its API to the frontend.
type CoreService struct {
	core      *core.Core
	ledger    *ledger                                     // today's and this month's traffic
	onReady   func() error                                // pushes the GUI's config once a core answers
	onTraffic func(Traffic)                               // menu bar speed
	onMode    func(string)                                // menu bar mode radio
	onConns   func(conns []Conn, deltas map[string]int64) // menu bar process rows, from the ledger's count

	mu     sync.Mutex
	status Status

	connMu sync.Mutex // the last connection snapshot, see connections
	conns  []Conn
	connAt time.Time

	netMu   sync.Mutex // networkName's cache
	netName string
	netAt   time.Time

	latMu sync.Mutex // Latency's cache, and its one-at-a-time measurement
	lat   Latency
	latAt time.Time
}

// restarting counts GUI-initiated core restarts in progress (Restart, moving onto the daemon): the
// traffic stream breaks during one, and must not push the config a second time when it is back.
var restarting atomic.Int32

func (s *CoreService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	go func() { // start in the background so the window shows right away
		// the streams retry on their own, so they run whatever the first start does: a core that
		// comes up later (a restart, the crash recovery in streamTraffic) is picked up by them
		go s.streamLogs(ctx)
		go s.countTraffic(ctx)
		var err error
		if daemon.Installed() {
			if err = s.core.Attach(ctx, daemon.Socket, daemon.LogPath); err != nil {
				// a daemon that doesn't answer (launchd trouble) must not leave the app proxy-less
				notify("warning", "root 内核没有响应（%v），改用 App 自己的内核；增强模式不可用", err)
				err = s.core.Start(ctx)
			}
		} else {
			err = s.core.Start(ctx)
		}
		if err == nil {
			err = s.onReady() // a daemon runs its boot config until the GUI hands it the real one
		}
		if err != nil {
			s.setStatus(Status{Error: err.Error()})
			notify("error", "内核启动失败：%v", err)
		} else {
			s.announce(ctx)
		}
		go s.updateDaemon()
		s.streamTraffic(ctx)
	}()
	return nil
}

// markRunning records the core as running, with its version.
func (s *CoreService) markRunning(ctx context.Context) core.Info {
	info, _ := s.core.Version(ctx)
	s.setStatus(Status{Running: true, Version: info.Version})
	return info
}

// announce marks the core running and tells the user, with the warnings a fresh core brings.
func (s *CoreService) announce(ctx context.Context) {
	info := s.markRunning(ctx)
	notify("info", "内核已启动（%s）", info.Version)
	if info.API < core.API {
		notify("warning", "内核接口版本为 %d，App 需要 %d：部分功能不可用，请用新版 connaio-core 重新打包", info.API, core.API)
	}
	if w := s.portWarning(); w != "" {
		notify("warning", "%s", w)
	}
	if mode, err := s.Mode(); err == nil {
		s.modeChanged(mode)
	}
}

// ServiceShutdown stops the child core, or puts the daemon back on its boot config so it doesn't
// keep capturing traffic (TUN) while the GUI is gone.
// portWarning reports listeners (e.g. mixed-port) the core could not open because another program,
// such as another proxy app, holds the port.
func (s *CoreService) portWarning() string {
	errs := s.core.BindErrors()
	if len(errs) == 0 {
		return ""
	}
	var ports []string
	for _, e := range errs {
		if m := bindPort.FindStringSubmatch(e); m != nil {
			ports = append(ports, m[1])
		}
	}
	if len(ports) == 0 {
		return "代理端口已被其他程序占用"
	}
	return "端口 " + strings.Join(ports, "、") + " 已被其他程序占用，未能监听"
}

var bindPort = regexp.MustCompile(`:(\d+): bind`)

func (s *CoreService) ServiceShutdown() error {
	if err := s.ledger.save(); err != nil {
		log.Printf("save traffic: %v", err)
	}
	if s.core.Daemon() {
		return s.core.Reload(context.Background(), []byte(daemon.BootConfig))
	}
	s.core.Stop()
	return nil
}

func (s *CoreService) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *CoreService) setStatus(st Status) {
	s.mu.Lock()
	s.status = st
	s.mu.Unlock()
	emit("status", st)
}

// Mode is the outbound mode: rule, global or direct.
func (s *CoreService) Mode() (string, error) {
	var cfg struct {
		Mode string `json:"mode"`
	}
	err := s.core.Do(context.Background(), http.MethodGet, "/configs", nil, &cfg)
	return cfg.Mode, err
}

func (s *CoreService) SetMode(mode string) error {
	if err := s.core.Do(context.Background(), http.MethodPatch, "/configs", map[string]string{"mode": mode}, nil); err != nil {
		return err
	}
	s.modeChanged(mode)
	return nil
}

func (s *CoreService) modeChanged(mode string) {
	if s.onMode != nil {
		s.onMode(mode)
	}
	emit("mode", mode)
}

// Groups lists proxy groups in config order (GLOBAL's member order), GLOBAL last.
func (s *CoreService) Groups() ([]Group, error) {
	var resp struct {
		Proxies map[string]struct {
			Type    string            `json:"type"`
			Now     string            `json:"now"`
			All     []string          `json:"all"`
			Default string            `json:"default"`
			Rules   map[string]string `json:"rules"`
			History []struct {
				Delay int `json:"delay"`
			} `json:"history"`
		} `json:"proxies"`
	}
	if err := s.core.Do(context.Background(), http.MethodGet, "/proxies", nil, &resp); err != nil {
		return nil, err
	}
	toGroup := func(name string) (Group, bool) {
		p, ok := resp.Proxies[name]
		if !ok || len(p.All) == 0 {
			return Group{}, false
		}
		g := Group{Name: name, Type: p.Type, Now: p.Now, Default: p.Default, Rules: p.Rules}
		for _, member := range p.All {
			m := resp.Proxies[member]
			delay := 0
			if n := len(m.History); n > 0 {
				delay = m.History[n-1].Delay
			}
			g.All = append(g.All, Proxy{Name: member, Type: m.Type, Delay: delay})
		}
		return g, true
	}
	var groups []Group
	for _, name := range resp.Proxies["GLOBAL"].All {
		if g, ok := toGroup(name); ok {
			groups = append(groups, g)
		}
	}
	if g, ok := toGroup("GLOBAL"); ok {
		groups = append(groups, g)
	}
	return groups, nil
}

// ProxyNames lists every node and group a member list or dialer-proxy can name (GLOBAL excluded).
func (s *CoreService) ProxyNames() ([]string, error) {
	groups, err := s.Groups()
	if err != nil {
		return nil, err
	}
	var names []string
	seen := map[string]bool{"GLOBAL": true}
	add := func(n string) {
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	for _, g := range groups {
		add(g.Name)
		for _, p := range g.All {
			add(p.Name)
		}
	}
	return names, nil
}

// TestProxy measures one proxy and returns its delay in ms.
func (s *CoreService) TestProxy(name string) (int, error) {
	return s.testProxy(context.Background(), name)
}

func (s *CoreService) testProxy(ctx context.Context, name string) (int, error) {
	var p struct {
		Type string `json:"type"`
	}
	_ = s.core.Do(ctx, http.MethodGet, "/proxies/"+url.PathEscape(name), nil, &p)
	return s.delay(ctx, name, testURLFor(p.Type))
}

// vpnTestURL is what a VPN node is measured against in place of testURL: behind a campus gateway
// in mainland China gstatic never answers, while a mainland 204 page answers on either side.
const vpnTestURL = "http://connect.rom.miui.com/generate_204"

// testURLFor picks the test URL for a node of the core's type.
func testURLFor(coreType string) string {
	if slices.Contains([]string{"EasyConnect", "WireGuard", "OpenVPN", "Tailscale"}, coreType) {
		return vpnTestURL
	}
	return testURL
}

func (s *CoreService) delay(ctx context.Context, name, u string) (int, error) {
	var d struct {
		Delay int `json:"delay"`
	}
	q := url.Values{"url": {u}, "timeout": {"5000"}}
	err := s.core.Do(ctx, http.MethodGet, "/proxies/"+url.PathEscape(name)+"/delay?"+q.Encode(), nil, &d)
	return d.Delay, err
}

func (s *CoreService) Select(group, proxy string) error {
	return s.core.Do(context.Background(), http.MethodPut, "/proxies/"+url.PathEscape(group), map[string]string{"name": proxy}, nil)
}

// TestGroup measures every member of group and returns name -> delay in ms (missing = failed).
// VPN members are measured again on their own against vpnTestURL. A member that fails is measured
// again on its own, twice, 3s apart: just after the app starts, while the core rebuilds the TUN
// under the config it was just given, every test can fail at once, and the page's tiles would stay
// blank until clicked.
func (s *CoreService) TestGroup(group string) (map[string]int, error) {
	q := url.Values{"url": {testURL}, "timeout": {"5000"}}
	delays := map[string]int{}
	ctx := context.Background()
	if err := s.core.Do(ctx, http.MethodGet, "/group/"+url.PathEscape(group)+"/delay?"+q.Encode(), nil, &delays); err != nil {
		return delays, err
	}
	var resp struct {
		Proxies map[string]struct {
			Type string   `json:"type"`
			All  []string `json:"all"`
		} `json:"proxies"`
	}
	if s.core.Do(ctx, http.MethodGet, "/proxies", nil, &resp) != nil {
		return delays, nil
	}
	all := resp.Proxies[group].All
	for _, m := range all {
		if u := testURLFor(resp.Proxies[m].Type); u != testURL {
			delete(delays, m)
			if d, err := s.delay(ctx, m, u); err == nil && d > 0 {
				delays[m] = d
			}
		}
	}
	for range 2 {
		var failed []string
		for _, m := range all {
			if _, ok := delays[m]; !ok && m != "REJECT" && m != "REJECT-DROP" {
				failed = append(failed, m)
			}
		}
		if len(failed) == 0 {
			break
		}
		time.Sleep(3 * time.Second)
		for _, m := range failed {
			if d, err := s.testProxy(ctx, m); err == nil && d > 0 {
				delays[m] = d
			}
		}
	}
	return delays, nil
}

// streamTraffic forwards the core's per-second traffic to the frontend and menu bar, reconnecting on errors.
// updateDaemon brings the root daemon up to the app's core after an update: it keeps running the
// one it started with until then. A daemon that runs a copy (Linux, Windows, and macOS's older
// LaunchDaemon) is installed again; one that runs the app's own core (macOS) is restarted when the
// versions differ, which carry the core's commit. It runs once the window is up and the old core
// attached, since an administrator prompt holds the main thread; the heartbeat below re-pushes the
// config to the new one.
func (s *CoreService) updateDaemon() {
	bin, err := core.Binary()
	if err != nil || !s.core.Daemon() {
		return
	}
	if daemon.Stale(bin) {
		if err := daemon.Install(bin); err != nil {
			notify("warning", "root 内核未更新，仍在运行旧版：%v", err)
		}
		return
	}
	if runtime.GOOS != "darwin" { // elsewhere the daemon runs a copy, replaced by Install when Stale
		return
	}
	out, err := run.Command(bin, "-v").Output() // "Mihomo Meta <version> darwin arm64 with …"
	f := strings.Fields(string(out))
	var running struct {
		Version string `json:"version"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err != nil || len(f) < 3 || s.core.Do(ctx, http.MethodGet, "/version", nil, &running) != nil || running.Version == f[2] {
		return
	}
	if err := s.core.Do(ctx, http.MethodPost, "/restart", nil, nil); err != nil {
		notify("warning", "root 内核未更新，仍在运行旧版：%v", err)
	}
}

// The stream doubles as the core's heartbeat: when it breaks the core went away (a crash, launchd
// restarting the root daemon, an update), and a core that comes back runs its boot config, so the
// GUI's config is pushed again once the stream is back.
// A child core that has exited (a crash) is started again here, with backoff.
func (s *CoreService) streamTraffic(ctx context.Context) {
	warned := false
	s.retryStream(ctx, "traffic stream", "/traffic", func(line []byte) {
		var t Traffic
		if json.Unmarshal(line, &t) != nil {
			return
		}
		if s.onTraffic != nil {
			s.onTraffic(t)
		}
		emit("traffic", t)
	}, func(error) {
		if restarting.Load() > 0 {
			return
		}
		if !warned {
			warned = true
			notify("warning", "与内核的连接中断，正在重连")
		}
		if !s.core.Daemon() && s.core.Exited() {
			s.setStatus(Status{Error: "内核已退出，正在重启"})
			if err := s.core.Start(ctx); err != nil {
				notify("error", "内核重启失败：%v", err)
			}
		}
	}, func() { warned = false; go s.reconnected(ctx) })
}

// reconnected pushes the config to a core that came back on its own (a crash, launchd restarting
// the daemon, an update); a restart the GUI made has done that itself.
func (s *CoreService) reconnected(ctx context.Context) {
	if restarting.Load() > 0 {
		return
	}
	if err := s.onReady(); err != nil {
		s.setStatus(Status{Error: err.Error()})
		notify("error", "内核已重新连接，但载入配置失败：%v", err)
		return
	}
	s.markRunning(ctx)
	notify("info", "内核已重新连接")
}

// ReportError records a frontend error (render crash, unhandled rejection) in gui-errors.log.
func (s *CoreService) ReportError(message string) {
	f, err := os.OpenFile(filepath.Join(s.core.Dir, "gui-errors.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n\n", time.Now().Format(time.RFC3339), message)
}
