package app

import (
	"cmp"
	"context"
	"encoding/json"
	"github.com/ryanzhangtianran/Connaio/internal/run"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ryanzhangtianran/Connaio/internal/profile"
)

// Usage is a client, host or policy's traffic today; Icon is a file whose Finder icon stands for it.
type Usage struct {
	Name string `json:"name"`
	Icon string `json:"icon,omitempty"`
	Split
}

type Activity struct {
	Today       Split     `json:"today"` // traffic counted by the ledger
	Month       Split     `json:"month"`
	Network     string    `json:"network"`
	Mode        string    `json:"mode"`
	Tun         bool      `json:"tun"` // enhanced mode
	Connections int       `json:"connections"`
	Processes   int       `json:"processes"`
	Devices     int       `json:"devices"`
	Hosts       int       `json:"hosts"`     // distinct domains (or IPs) with live connections
	Hours       [24]Split `json:"hours"`     // today, by hour of day
	ByProcess   []Usage   `json:"byProcess"` // today's top clients, hosts and policies
	ByHost      []Usage   `json:"byHost"`
	ByPolicy    []Usage   `json:"byPolicy"`
}

type Latency struct {
	Internet int    `json:"internet"` // direct HTTP round trip, ms; 0 = failed
	Router   int    `json:"router"`   // ping to the default gateway, ms
	DNS      int    `json:"dns"`      // system resolver lookup, ms
	Proxy    int    `json:"proxy"`    // current node of ProxyGroup, ms
	Group    string `json:"group"`
}

const testURL = "https://www.gstatic.com/generate_204"

// directURL is what the direct Internet probe fetches: reachable without a proxy from anywhere,
// China included, where Google's is reset.
const directURL = "http://captive.apple.com/hotspot-detect.html"

// Activity summarizes the live connections for the activity page; polled by the frontend.
func (s *CoreService) Activity() (Activity, error) {
	ctx := context.Background()
	var cfg struct {
		Mode string `json:"mode"`
		Tun  struct {
			Enable bool `json:"enable"`
		} `json:"tun"`
	}
	if err := s.core.Do(ctx, http.MethodGet, "/configs", nil, &cfg); err != nil {
		return Activity{}, err
	}
	conns, err := s.Connections()
	if err != nil {
		return Activity{}, err
	}
	a := Activity{Network: s.networkName(ctx), Mode: cfg.Mode, Tun: cfg.Tun.Enable, Connections: len(conns)}
	processes, hosts, devices := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, c := range conns {
		name, _ := clientOf(c)
		processes[name], hosts[c.Host] = true, true
		if c.Process == "" && c.Path == "" && net.ParseIP(c.Source) != nil && !fromHere(c.Source) { // a LAN device using us as a proxy or gateway
			devices[c.Source] = true
		}
	}
	a.Processes, a.Devices, a.Hosts = len(processes), len(devices), len(hosts)
	a.Today, a.Month = s.ledger.totals(time.Now())
	a.Hours, a.ByProcess, a.ByHost, a.ByPolicy = s.ledger.today(time.Now())
	return a, nil
}

// networkName is the Wi-Fi SSID, or the default route's interface when it is unknown. macOS redacts
// the SSID from users, so only the core running as the root daemon can tell. Cached for 10s: the
// fallback runs two commands, and the activity page asks every second.
func (s *CoreService) networkName(ctx context.Context) string {
	s.netMu.Lock()
	defer s.netMu.Unlock()
	if time.Since(s.netAt) < 10*time.Second {
		return s.netName
	}
	s.netName, s.netAt = s.lookupNetworkName(ctx), time.Now()
	return s.netName
}

func (s *CoreService) lookupNetworkName(ctx context.Context) string {
	var n struct {
		SSID string `json:"ssid"`
	}
	if s.core.Do(ctx, http.MethodGet, "/network", nil, &n) == nil && n.SSID != "" {
		return n.SSID
	}
	iface := defaultRoute()["interface"]
	if iface == "" {
		return "未连接"
	}
	return cmp.Or(interfaceLabel(iface), iface) // what the system calls it, e.g. 有线连接 1, not enp3s0
}

// defaultRoute is the IPv4 default route as key -> value, e.g. interface, gateway.
func defaultRoute() map[string]string { return routeOf("-inet") }

// pingMs is one ICMP round trip to host via ping (no root needed), 0 if it fails.
func pingMs(ctx context.Context, host string) int {
	out, err := run.CommandContext(ctx, "ping", append(pingArgs, host)...).Output()
	if err != nil {
		return 0
	}
	_, rest, ok := strings.Cut(string(out), "time=")
	ms, _, _ := strings.Cut(rest, " ")
	f, err := strconv.ParseFloat(ms, 64)
	if !ok || err != nil {
		return 0
	}
	return max(1, int(f+0.5))
}

// Latency measures the router, the direct internet path, the system DNS and the policy of the
// final MATCH rule (where unmatched traffic goes); a DIRECT one is the internet path already.
// One measurement at a time, and its result serves the menu bar and the activity page for a minute.
func (s *CoreService) Latency() Latency {
	s.latMu.Lock()
	defer s.latMu.Unlock()
	if time.Since(s.latAt) < time.Minute && s.lat.Internet > 0 { // a failure isn't kept: the next ask measures again
		return s.lat
	}
	s.lat, s.latAt = s.measureLatency(), time.Now()
	return s.lat
}

func (s *CoreService) measureLatency() Latency {
	l := Latency{Group: cmp.Or(s.matchPolicy(), "代理")}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if gw := defaultRoute()["gateway"]; gw != "" {
		l.Router = pingMs(ctx, gw)
	}
	start := time.Now()
	if _, err := net.DefaultResolver.LookupHost(ctx, "www.apple.com"); err == nil {
		l.DNS = ms(start)
	}
	direct := oneShotClient(0, 5*time.Second)
	start = time.Now()
	if resp, err := direct.Head(directURL); err == nil {
		resp.Body.Close()
		l.Internet = ms(start)
	} else {
		log.Printf("网络质量探测失败: %v", err)
	}
	if node := s.exitOf(l.Group); node != "DIRECT" && node != "REJECT" {
		l.Proxy, _ = s.TestProxy(node)
	}
	return l
}

// matchPolicy is the policy of the final MATCH rule, where unmatched traffic goes; "" without one.
func (s *CoreService) matchPolicy() string {
	rules, _ := s.Rules()
	if n := len(rules); n > 0 && rules[n-1].Type == "Match" {
		return rules[n-1].Proxy
	}
	return ""
}

// exitOf follows policy through the groups' current choices to the node it uses now.
func (s *CoreService) exitOf(policy string) string {
	groups, _ := s.Groups()
	for range 10 { // ponytail: depth cap instead of cycle detection; groups nest a few levels at most
		i := slices.IndexFunc(groups, func(g Group) bool { return g.Name == policy })
		if i < 0 {
			break
		}
		policy = groups[i].Now
	}
	return policy
}

// ExternalIP is the address the internet sees for traffic through the core's mixed-port.
func (s *CoreService) ExternalIP() (string, error) {
	port, err := mixedPort(s.core)
	if err != nil {
		return "", err
	}
	resp, err := oneShotClient(port, 8*time.Second).Get("https://api.ipify.org")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	return strings.TrimSpace(string(b)), err
}

func ms(start time.Time) int { return max(1, int(time.Since(start).Milliseconds())) }

// Split is traffic in bytes, by whether it left through DIRECT or a proxy.
type Split struct {
	Direct int64 `json:"direct"`
	Proxy  int64 `json:"proxy"`
}

// clientOf names a connection's client, an app folded into its bundle, the LAN device's address,
// or Connaio for the core's own connections (rule sets, health checks), with the file whose
// Finder icon stands for it: a command counts towards the app it was started from (claude run
// in Ghostty is Ghostty), as on Linux; the system's own daemons (trustd's certificate checks,
// nsurlsessiond's background downloads for other apps) fold into macOS; none for a device.
func clientOf(c Conn) (name, icon string) {
	if c.Inbound == "Inner" {
		return "Connaio", cmp.Or(ownBundle, ownExe)
	}
	if (c.Inbound == "SSH" || c.Inbound == "FRP") && c.InName != "" {
		return "隧道 " + c.InName, "" // came in from the server through a reverse tunnel, not this machine's
	}
	if systemDaemon(c.Path) {
		return "macOS", "/System/Applications/System Settings.app"
	}
	if c.System || c.Process == "" && c.Path == "" && c.Inbound != "Inner" && fromHere(c.Source) {
		// Linux's services, as macOS's daemons above, and this machine's connections the core
		// couldn't name; AppIcon draws "system" as the settings gear
		if runtime.GOOS == "darwin" {
			return "macOS", "/System/Applications/System Settings.app"
		}
		return "System", "system"
	}
	name, icon = c.Process, c.Path
	if b := bundleOf(c.Path); b != "" {
		icon, name = b, strings.TrimSuffix(filepath.Base(b), ".app")
	} else if app := appName(c.Path); app != "" {
		name = app // Linux: the installed application the executable belongs to (chrome is Google Chrome)
	} else if p := parentApp(c); p != "" {
		b := bundleOf(p)
		icon = cmp.Or(b, p)
		if b != "" {
			name = strings.TrimSuffix(filepath.Base(b), ".app")
		}
		name = cmp.Or(appName(p), name) // a command counts towards the application it was started from
	} else if runtime.GOOS == "darwin" && name != "" {
		// no bundle and no app above it: a command gone before its parent was looked up (git's
		// git-remote-http, npm's node) or one launchd started; macOS's, as the daemons are
		return "macOS", "/System/Applications/System Settings.app"
	}
	return cmp.Or(name, c.Source, "-"), icon
}

// parentApp is the application c's process was started from: the nearest of the ancestors the core
// read when the connection opened that is one, or else, from a core that reports none, one looked
// up by name now, which misses a command that has already exited.
func parentApp(c Conn) string {
	for _, a := range c.Ancestors {
		if bundleOf(a) != "" && !systemDaemon(a) || appName(a) != "" {
			return a
		}
	}
	if len(c.Ancestors) > 0 {
		return ""
	}
	return parentOf(c.Process)
}

// ownExe is this app's executable: its own connections are Connaio's, as the core's are.
var ownExe, _ = os.Executable()

// ownBundle is this app's bundle, for its icon; empty when not run from one.
var ownBundle = bundleOf(ownExe)

func (s *Split) add(direct bool, n int64) {
	if direct {
		s.Direct += n
	} else {
		s.Proxy += n
	}
}

// Day is one day of traffic: its total (the old file held only that), by hour, and by client, host
// and policy.
type Day struct {
	Split
	Hours  [24]Split         `json:"hours"`
	Client map[string]Split  `json:"client,omitempty"`
	Host   map[string]Split  `json:"host,omitempty"`
	Policy map[string]Split  `json:"policy,omitempty"`
	Icons  map[string]string `json:"icons,omitempty"` // client -> file whose icon stands for it, kept across restarts
}

// ledger keeps per-day traffic in traffic.json.
type ledger struct {
	path  string
	mu    sync.Mutex
	days  map[string]*Day  // "2006-01-02" -> traffic
	seen  map[string]int64 // bytes per live connection at the last count; nil before the first
	dirty bool             // changed since the last save
}

func openLedger(path string) *ledger {
	l := &ledger{path: path, days: map[string]*Day{}}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &l.days)
	}
	return l
}

// add counts what each connection moved since the last call, and returns those deltas by connection
// id; one new since then counts in full, while the first call only takes a baseline (nil: traffic
// from before the GUI started is not ours to count).
// ponytail: bytes moved in a connection's last second before it closes are lost; a stream of closed
// connections from the core would catch them.
func (l *ledger) add(conns []Conn, now time.Time) map[string]int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := now.Format(time.DateOnly)
	d := l.days[key]
	if d == nil {
		d = &Day{}
		l.days[key] = d
	}
	seen := make(map[string]int64, len(conns))
	var deltas map[string]int64
	if l.seen != nil {
		deltas = make(map[string]int64, len(conns))
	}
	for _, c := range conns {
		bytes := c.Upload + c.Download
		seen[c.ID] = bytes
		delta := bytes - l.seen[c.ID]
		if l.seen == nil || delta <= 0 {
			continue
		}
		deltas[c.ID] = delta
		direct := c.Chain[strings.LastIndex(c.Chain, " ")+1:] == "DIRECT"
		name, icon := clientOf(c)
		policy, _, _ := strings.Cut(c.Chain, " → ") // the outermost group the rule chose
		d.Split.add(direct, delta)
		d.Hours[now.Hour()].add(direct, delta)
		bump(&d.Client, name, direct, delta)
		bump(&d.Host, c.Host, direct, delta)
		bump(&d.Policy, policy, direct, delta)
		if icon != "" {
			if d.Icons == nil {
				d.Icons = map[string]string{}
			}
			d.Icons[name] = icon
		}
		l.dirty = true
	}
	l.seen = seen
	return deltas
}

// bump adds n to m[k], making m if it is nil.
func bump(m *map[string]Split, k string, direct bool, n int64) {
	if *m == nil {
		*m = map[string]Split{}
	}
	v := (*m)[k]
	v.add(direct, n)
	(*m)[k] = v
}

func (l *ledger) totals(now time.Time) (today, month Split) {
	l.mu.Lock()
	defer l.mu.Unlock()
	prefix := now.Format("2006-01-")
	for day, d := range l.days {
		if strings.HasPrefix(day, prefix) {
			month.Direct += d.Direct
			month.Proxy += d.Proxy
		}
	}
	if d := l.days[now.Format(time.DateOnly)]; d != nil {
		today = d.Split
	}
	return today, month
}

// today is today's traffic by hour, and its top clients, hosts and policies. Ranking the day's
// maps costs a few sorts, so the last answer is served again until the ledger changes.
func (l *ledger) today(now time.Time) (hours [24]Split, clients, hosts, policies []Usage) {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := now.Format(time.DateOnly)
	d := l.days[key]
	if d == nil {
		return
	}
	return d.Hours, topUsage(d.Client, d.Icons, 20), topUsage(d.Host, nil, 20), topUsage(d.Policy, nil, 20)
}

// topUsage is the n largest entries, biggest first (the page can rank by proxied traffic alone).
func topUsage(m map[string]Split, icons map[string]string, n int) []Usage {
	out := make([]Usage, 0, len(m))
	for name, s := range m {
		out = append(out, Usage{Name: name, Icon: icons[name], Split: s})
	}
	slices.SortFunc(out, func(a, b Usage) int {
		return cmp.Or(cmp.Compare(b.Direct+b.Proxy, a.Direct+a.Proxy), strings.Compare(a.Name, b.Name))
	})
	return out[:min(len(out), n)]
}

// keepDays is how long a day's traffic stays in the ledger: a year of months, plus the current one.
const keepDays = 13 * 31

// clear forgets every day's traffic and saves that; live connections go on counting from the bytes
// already seen, so nothing before the clear comes back.
// ClearTraffic clears the traffic statistics: today's, this month's and every day kept.
func (s *CoreService) ClearTraffic() error {
	l := s.ledger
	l.mu.Lock()
	l.days, l.dirty = map[string]*Day{}, true
	l.mu.Unlock()
	return l.save()
}

func (l *ledger) save() error {
	l.mu.Lock()
	if !l.dirty {
		l.mu.Unlock()
		return nil
	}
	cutoff := time.Now().AddDate(0, 0, -keepDays).Format(time.DateOnly)
	for day, d := range l.days {
		if day < cutoff {
			delete(l.days, day)
			continue
		}
		// ponytail: hosts and clients grow without bound in a day; keep the 500 busiest
		d.Host, d.Client = trimUsage(d.Host, 500), trimUsage(d.Client, 500)
	}
	b, err := json.Marshal(l.days)
	l.dirty = err != nil
	l.mu.Unlock() // the write happens outside the lock: counting goes on meanwhile
	if err != nil {
		return err
	}
	return profile.WriteAtomic(l.path, b, 0o644)
}

// trimUsage keeps the n largest entries of m.
func trimUsage(m map[string]Split, n int) map[string]Split {
	if len(m) <= n {
		return m
	}
	top := topUsage(m, nil, n)
	m = make(map[string]Split, len(top))
	for _, u := range top {
		m[u.Name] = u.Split
	}
	return m
}

// countTraffic feeds the ledger every second (and the menu bar's process rows), saving it every minute.
func (s *CoreService) countTraffic(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for i := 1; ; i++ {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if conns, err := s.refreshConnections(); err == nil {
				deltas := s.ledger.add(conns, now)
				if s.onConns != nil {
					s.onConns(conns, deltas)
				}
			}
			if i%60 == 0 {
				if err := s.ledger.save(); err != nil {
					log.Printf("save traffic: %v", err)
				}
			}
		}
	}
}
