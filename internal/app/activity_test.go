package app

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Needs a built core: CONNAIO_CORE=/path/to/connaio-core go test -run TestActivity .
func TestActivity(t *testing.T) {
	s, _, port := startCore(t, "")

	// keep one connection open through the mixed-port so there is something to summarize
	ip, err := s.ExternalInfo()
	t.Logf("external ip %+v err=%v", ip, err)
	go http.Get(fmt.Sprintf("http://127.0.0.1:%d", port)) // the core answers on mixed-port; harmless if it errors
	time.Sleep(300 * time.Millisecond)

	a, err := s.Activity()
	if err != nil {
		t.Fatal(err)
	}
	if a.Mode != "rule" || a.Network == "" {
		t.Fatalf("activity header %+v", a)
	}
	t.Logf("activity: conns=%d procs=%d devices=%d byProcess=%v", a.Connections, a.Processes, a.Devices, a.ByProcess)

	l := s.Latency()
	t.Logf("latency %+v", l)
	if l.Group != "DIRECT" { // the empty profile's catch-all
		t.Fatalf("latency group %q", l.Group)
	}
}

func TestPortWarning(t *testing.T) {
	s, _, port := startCore(t, "")
	if w := s.portWarning(); w != "" {
		t.Fatalf("port %d is open, got warning %q", port, w)
	}
	// a second core told to use the first core's port can't open it, yet keeps running
	cs, _, _ := startCore(t, "")
	if err := cs.core.Reload(t.Context(), []byte(fmt.Sprintf("mixed-port: %d\nrules:\n  - MATCH,DIRECT\n", port))); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	w := cs.portWarning()
	if !strings.Contains(w, strconv.Itoa(port)) {
		t.Fatalf("expected a warning naming port %d, got %q", port, w)
	}
	t.Logf("warning: %s", w)
}

func TestLedger(t *testing.T) {
	l := openLedger(filepath.Join(t.TempDir(), "traffic.json"))
	day1 := time.Date(2026, 10, 9, 23, 0, 0, 0, time.Local)
	day2 := day1.Add(2 * time.Hour)
	l.add([]Conn{{ID: "old", Chain: "代理 → HK 01", Download: 500}}, day1) // baseline only
	l.add([]Conn{
		{ID: "old", Chain: "代理 → HK 01", Download: 800},
		{ID: "new", Chain: "DIRECT", Upload: 100},
	}, day1)
	l.add([]Conn{{ID: "new", Chain: "DIRECT", Upload: 150}}, day2)
	today, month := l.totals(day2)
	if today != (Split{Direct: 50}) || month != (Split{Direct: 150, Proxy: 300}) {
		t.Fatalf("today %+v month %+v", today, month)
	}
	if err := l.save(); err != nil {
		t.Fatal(err)
	}
	if _, month := openLedger(l.path).totals(day2); month != (Split{Direct: 150, Proxy: 300}) {
		t.Fatalf("reloaded month %+v", month)
	}
	hours, clients, _, policies := l.today(day1)
	if hours[23] != (Split{Direct: 100, Proxy: 300}) {
		t.Fatalf("hour 23 %+v", hours[23])
	}
	if len(clients) != 1 || clients[0].Name != "-" || len(policies) != 2 || policies[0].Name != "代理" {
		t.Fatalf("clients %+v policies %+v", clients, policies)
	}
}

func TestLedgerOldFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traffic.json")
	if err := os.WriteFile(path, []byte(`{"2026-10-09":{"direct":7,"proxy":9}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if today, _ := openLedger(path).totals(time.Date(2026, 10, 9, 12, 0, 0, 0, time.Local)); today != (Split{Direct: 7, Proxy: 9}) {
		t.Fatalf("today %+v", today)
	}
}

func TestClientOf(t *testing.T) {
	if n, i := clientOf(Conn{Path: "/Applications/Google Chrome.app/Contents/Frameworks/x.app/Contents/MacOS/Helper"}); n != "Google Chrome" || i != "/Applications/Google Chrome.app" {
		t.Fatalf("%q %q", n, i)
	}
	if n, i := clientOf(Conn{Source: "192.168.1.9"}); n != "192.168.1.9" || i != "" {
		t.Fatalf("%q %q", n, i)
	}
	if n, i := clientOf(Conn{Inbound: "SSH", InName: "vps", Source: "127.0.0.1", Tunnel: &TunnelHop{Name: "vps"}}); n != "vps" || i != "tunnel" {
		t.Errorf("a connection a reverse tunnel brought in: %q %q", n, i) // not this machine's, though its source is loopback
	}
	// the tunnel's connection to the proxy port and the request it carried there: one connection,
	// the request's, with the tunnel's hop
	hop := &TunnelHop{Name: "vps", Type: "SSH", From: "203.0.113.5:40000", Target: "127.0.0.1:7890"}
	conns := linkTunnels([]Conn{
		{ID: "carrier", Inbound: "SSH", InName: "vps", Tunnel: hop, outLocal: "127.0.0.1:51000"},
		{ID: "request", Inbound: "Mixed", Source: "127.0.0.1", SrcPort: "51000", Host: "example.com"},
		{ID: "other", Inbound: "Mixed", Source: "127.0.0.1", SrcPort: "51001"},
	})
	if len(conns) != 2 || conns[0].ID != "request" || conns[0].Tunnel != hop || conns[1].Tunnel != nil {
		t.Errorf("linked: %+v", conns)
	}
	if n, _ := clientOf(Conn{Inbound: "Inner"}); n != "Connaio" {
		t.Fatalf("the core's own connection is %q", n)
	}
}

func TestPingMs(t *testing.T) {
	if ms := pingMs(t.Context(), "127.0.0.1"); ms < 1 {
		t.Fatalf("ping 127.0.0.1 = %d ms", ms)
	}
	if ms := pingMs(t.Context(), "invalid.invalid"); ms != 0 {
		t.Fatalf("ping of an unresolvable host = %d ms, want 0", ms)
	}
}

func TestDiagnoseNetwork(t *testing.T) {
	sec := diagNetwork(t.Context())
	if len(sec.Lines) != 6 {
		t.Fatalf("lines %v", sec.Lines)
	}
	t.Log(strings.Join(sec.Lines, "\n"), "\n", diagRouter(t.Context()).Lines, diagDNS(t.Context()).Lines, diagDirect(t.Context()).Lines)
}

func TestLeakedServers(t *testing.T) {
	home := LeakServer{IP: "1.2.3.4", Country: "China", ASN: "AS4134 China Telecom"}
	alidns := LeakServer{IP: "47.1.1.1", Country: "China", ASN: "AS37963 Alibaba"}
	exit := LeakServer{IP: "23.0.0.1", Country: "United States of America", ASN: "AS402681 AYIT"}
	cloudflare := LeakServer{IP: "104.22.22.78", Country: "United States of America", ASN: "AS13335 CloudFlare Inc"}
	tencent := LeakServer{IP: "119.29.29.29", Country: "China", ASN: "AS45090 Tencent"}
	if got := leakedServers([]LeakServer{cloudflare}, []LeakServer{alidns}, home, exit); len(got) != 0 {
		t.Fatalf("resolvers abroad flagged: %v", got)
	}
	if got := leakedServers([]LeakServer{cloudflare, alidns}, []LeakServer{alidns}, home, exit); len(got) != 1 || got[0] != alidns {
		t.Fatalf("this network's resolver missed: %v", got)
	}
	if got := leakedServers([]LeakServer{tencent}, []LeakServer{alidns}, home, exit); len(got) != 1 {
		t.Fatalf("a resolver in this country, the proxy abroad, missed: %v", got)
	}
	if got := leakedServers([]LeakServer{tencent}, []LeakServer{alidns}, home, home); len(got) != 0 {
		t.Fatalf("a proxy in the same country: only networks count, got %v", got)
	}
}

func TestLedgerFoldsApps(t *testing.T) {
	l := openLedger(filepath.Join(t.TempDir(), "traffic.json"))
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.Local)
	app := "/Applications/Google Chrome.app/Contents/"
	conns := []Conn{{ID: "a", Path: app + "MacOS/Google Chrome"}, {ID: "b", Path: app + "Frameworks/x.app/Contents/MacOS/Google Chrome Helper"}}
	l.add(conns, now)
	conns[0].Download, conns[1].Download = 100, 300
	l.add(conns, now)
	_, clients, _, _ := l.today(now)
	if len(clients) != 1 || clients[0].Name != "Google Chrome" || clients[0].Direct+clients[0].Proxy != 400 {
		t.Fatalf("%+v", clients)
	}
	if err := l.save(); err != nil {
		t.Fatal(err)
	}
	if _, clients, _, _ := openLedger(l.path).today(now); clients[0].Icon != "/Applications/Google Chrome.app" {
		t.Fatalf("the app's icon must survive a restart: %+v", clients[0])
	}
}

func TestFrpsScript(t *testing.T) {
	script := fmt.Sprintf(frpsScript, 7000, "abc")
	if strings.Contains(script, "%!") || !strings.Contains(script, `bindPort = 7000\nauth.method = "token"\nauth.token = "abc"`) {
		t.Fatalf("script:\n%s", script)
	}
	if !strings.Contains(script, "is-active --quiet frps || {") {
		t.Fatal("a frps that did not start must fail the deploy")
	}
	cmd := exec.Command("sh", "-n")
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sh -n: %v %s", err, out)
	}
}

func TestNotifyCollapsesRepeats(t *testing.T) {
	eventLog.list = nil
	notify("error", "core down")
	notify("error", "core down") // a poll failing again
	notify("info", "core up")
	if len(eventLog.list) != 2 {
		t.Fatalf("events %+v", eventLog.list)
	}
}

// save drops days older than the ledger keeps and trims a day's maps to the busiest entries.
func TestLedgerSavePrunesAndTrims(t *testing.T) {
	l := openLedger(filepath.Join(t.TempDir(), "traffic.json"))
	old := time.Now().AddDate(0, 0, -keepDays-1).Format(time.DateOnly)
	today := time.Now().Format(time.DateOnly)
	l.days[old] = &Day{Split: Split{Direct: 1}}
	d := &Day{Client: map[string]Split{}}
	for i := 0; i < 600; i++ {
		name := fmt.Sprintf("c%d", i)
		d.Client[name] = Split{Direct: int64(i)}
	}
	l.days[today] = d
	l.dirty = true
	if err := l.save(); err != nil {
		t.Fatal(err)
	}
	if l.days[old] != nil {
		t.Error("an old day survived")
	}
	if len(d.Client) != 500 || d.Client["c0"] != (Split{}) || d.Client["c599"].Direct != 599 {
		t.Errorf("trim: %d clients", len(d.Client))
	}
	if _, err := os.Stat(l.path); err != nil {
		t.Error("not saved")
	}
}

func TestLedgerClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traffic.json")
	l := openLedger(path)
	now := time.Now()
	l.add([]Conn{{ID: "1", Download: 100, Chain: "DIRECT"}}, now)
	l.add([]Conn{{ID: "1", Download: 300, Chain: "DIRECT"}}, now) // counted from the second look
	if today, _ := l.totals(now); today.Direct != 200 {
		t.Fatalf("before: %+v", today)
	}
	if err := (&CoreService{ledger: l}).ClearTraffic(); err != nil {
		t.Fatal(err)
	}
	l.add([]Conn{{ID: "1", Download: 350, Chain: "DIRECT"}}, now)
	if today, _ := l.totals(now); today.Direct != 50 {
		t.Fatalf("after: %+v, want only what came since", today)
	}
	if again, _ := openLedger(path).totals(now); again.Direct != 0 {
		t.Fatalf("saved file still has %+v", again)
	}
}

func TestClientOfSystemService(t *testing.T) {
	name, icon := "System", "system"
	if runtime.GOOS == "darwin" {
		name, icon = "macOS", "/System/Applications/System Settings.app"
	}
	if n, i := clientOf(Conn{Process: "NetworkManager", Path: "/usr/sbin/NetworkManager", System: true}); n != name || i != icon {
		t.Fatalf("system service: %q %q", n, i)
	}
	for _, src := range []string{"127.0.0.1", "198.18.0.1"} { // this machine, process unknown: not a device
		if n, _ := clientOf(Conn{Source: src}); n != name {
			t.Errorf("unnamed connection from %s: %q", src, n)
		}
	}
	cmd := "connaio-test-no-such-command" // no bundle, no process to find a parent app for
	if runtime.GOOS != "darwin" {
		name = cmd // Linux keeps a bare command's name
	}
	if n, _ := clientOf(Conn{Process: cmd, Path: "/opt/homebrew/bin/" + cmd}); n != name {
		t.Errorf("a bare command: %q, want %q", n, name)
	}
	ghostty := "/Applications/Ghostty.app/Contents/MacOS/ghostty"
	if n, i := clientOf(Conn{Process: cmd, Path: "/opt/homebrew/bin/" + cmd, Ancestors: []string{"/bin/zsh", ghostty}}); n != "Ghostty" || i != "/Applications/Ghostty.app" {
		t.Errorf("a command exited before it was looked up, its ancestors read by the core: %q %q", n, i)
	}
	if n, _ := clientOf(Conn{Process: cmd, Path: "/opt/homebrew/bin/" + cmd, Ancestors: []string{"/usr/sbin/cron"}}); n != name {
		t.Errorf("a command with no app among its ancestors: %q, want %q", n, name)
	}
	if n, _ := clientOf(Conn{Source: "192.0.2.9"}); n != "192.0.2.9" {
		t.Errorf("a LAN device keeps its address: %q", n)
	}
}

func TestCleanSites(t *testing.T) {
	got, err := cleanSites([]Site{{" Bilibili ", "www.bilibili.com"}, {"X", "http://x.com/path"}})
	if err != nil || got[0] != (Site{"Bilibili", "https://www.bilibili.com"}) || got[1].URL != "http://x.com/path" {
		t.Errorf("%v %v", got, err)
	}
	for _, bad := range [][]Site{
		{{"", "https://a.com"}},
		{{"A", "https://a.com"}, {"a", "https://b.com"}},
		{{"A", "ftp://a.com"}},
		make([]Site, maxSites+1),
	} {
		if _, err := cleanSites(bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	if got, err := cleanSites(nil); err != nil || len(got) != 0 {
		t.Errorf("no sites: %v %v", got, err) // stored as none, which reads as the defaults
	}
}

func TestLedgerMonth(t *testing.T) {
	l := &ledger{days: map[string]*Day{
		"2026-02-01": {Split: Split{Direct: 1}, Client: map[string]Split{"A": {Direct: 1}}},
		"2026-02-28": {Split: Split{Proxy: 2}, Client: map[string]Split{"A": {Proxy: 2}, "B": {Proxy: 5}}, Icons: map[string]string{"B": "b"}},
		"2026-03-01": {Split: Split{Proxy: 9}, Client: map[string]Split{"C": {Proxy: 9}}}, // next month's: left out
	}}
	days, clients, _, _ := l.month(time.Date(2026, 2, 14, 12, 0, 0, 0, time.UTC))
	if len(days) != 28 || days[0].Direct != 1 || days[27].Proxy != 2 {
		t.Errorf("days: %v", days)
	}
	if len(clients) != 2 || clients[0] != (Usage{Name: "B", Icon: "b", Split: Split{Proxy: 5}}) || clients[1].Direct+clients[1].Proxy != 3 {
		t.Errorf("clients: %+v", clients)
	}
}

func TestLedgerTunnelRename(t *testing.T) {
	p := filepath.Join(t.TempDir(), "traffic.json")
	os.WriteFile(p, []byte(`{"2026-10-20":{"client":{"隧道 vps":{"direct":0,"proxy":5},"vps":{"direct":1,"proxy":2},"Ghostty":{"direct":3,"proxy":0}}}}`), 0o644)
	d := openLedger(p).days["2026-10-20"]
	if len(d.Client) != 2 || d.Client["vps"] != (Split{Direct: 1, Proxy: 7}) || d.Client["Ghostty"].Direct != 3 {
		t.Errorf("clients: %v", d.Client)
	}
}
