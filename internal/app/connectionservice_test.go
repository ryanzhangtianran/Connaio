package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"
)

// Needs a built core: CONNAIO_CORE=/path/to/connaio-core go test -run TestConnectionService .
func TestConnectionService(t *testing.T) {
	s, _, port := startCore(t, "")

	hold := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-hold
	}))
	defer srv.Close()
	defer close(hold)
	proxy, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}}
	go client.Get(srv.URL) // stays open until hold closes

	var conns []Conn
	for i := 0; i < 20 && len(conns) == 0; i++ {
		time.Sleep(100 * time.Millisecond)
		conns, _ = s.Connections()
	}
	if len(conns) == 0 || conns[0].Host != "127.0.0.1" || conns[0].Chain == "" || conns[0].Rule == "" {
		t.Fatalf("connection: %+v", conns)
	}
	go client.Get(srv.URL) // a second one, for CloseAll
	time.Sleep(200 * time.Millisecond)
	if err := s.CloseAll(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if left, _ := s.Connections(); len(left) != 0 {
		t.Fatalf("CloseAll left %+v", left)
	}
	go client.Get(srv.URL)
	for i := 0; i < 20; i++ {
		if conns, _ = s.Connections(); len(conns) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := s.CloseHost("127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if conns, _ = s.Connections(); len(conns) != 0 {
		t.Fatalf("CloseHost left %+v", conns)
	}

	policies, err := s.Policies()
	if err != nil || !slices.Contains(policies, "DIRECT") || slices.Contains(policies, "GLOBAL") {
		t.Fatalf("policies %v %v", policies, err)
	}
	if _, err := s.AddTempRule("DOMAIN,a.com,NoSuchPolicy"); err == nil {
		t.Fatal("unknown policy accepted")
	}
	id, err := s.AddTempRule("DOMAIN-SUFFIX,example.com,DIRECT")
	if err != nil {
		t.Fatal(err)
	}
	rules, _ := s.TempRules()
	if len(rules) != 1 || rules[0].ID != id || rules[0].Payload != "example.com" || rules[0].Proxy != "DIRECT" {
		t.Fatalf("temp rules %+v", rules)
	}
	if err := s.RemoveTempRule(id); err != nil {
		t.Fatal(err)
	}
	if rules, _ = s.TempRules(); len(rules) != 0 {
		t.Fatalf("not removed: %+v", rules)
	}
}

func TestTrayProcessRows(t *testing.T) {
	l := openLedger(filepath.Join(t.TempDir(), "traffic.json"))
	chrome := "/Applications/Google Chrome.app/Contents/Frameworks/Helper.app/Contents/MacOS/Helper"
	conns := []Conn{
		{ID: "1", Path: chrome, Upload: 100},
		{ID: "2", Process: "curl", Path: "/usr/bin/curl", Download: 50},
		{ID: "3", Source: "192.168.1.9", Download: 10},
	}
	active, now := map[string]time.Time{}, time.Now()
	if rows := processRows(conns, l.add(conns, now), active, now); len(rows) != 0 {
		t.Fatalf("first tick has no baseline, so nothing moved yet: got %+v", rows)
	}
	conns[0].Upload, conns[1].Download = 400, 60
	conns = append(conns, Conn{ID: "4", Path: chrome, Download: 1000}) // new since the last tick: counts in full
	rows := processRows(conns, l.add(conns, now), active, now)
	curl := procRow{name: "curl", icon: "/usr/bin/curl", rate: 10, total: 60}
	if runtime.GOOS == "darwin" { // a bare command with no app above it is macOS's
		curl.name, curl.icon = "macOS", "/System/Applications/System Settings.app"
	}
	want := []procRow{ // the idle device stays out
		{name: "Google Chrome", icon: "/Applications/Google Chrome.app", rate: 1300, total: 1400},
		curl,
	}
	if !slices.Equal(rows, want) {
		t.Fatalf("got %+v\nwant %+v", rows, want)
	}
	if rows := processRows(conns, map[string]int64{}, active, now.Add(5*time.Second)); len(rows) != 2 {
		t.Fatalf("recently active rows stay listed while idle: %+v", rows)
	}
	if rows := processRows(conns, map[string]int64{}, active, now.Add(11*time.Second)); len(rows) != 0 {
		t.Fatalf("rows idle for over %v drop out: %+v", trayRecent, rows)
	}
}

// A desktop session's own daemons count as the system; the user's programs, under /usr/lib too, don't.
func TestSystemPath(t *testing.T) {
	saved := systemDirs
	defer func() { systemDirs = saved }()
	systemDirs = []string{"/usr/libexec/", "/usr/lib/systemd/", "/usr/lib/x86_64-linux-gnu/libexec/", "/usr/lib/gnome-", "/usr/bin/gnome-shell"}
	for path, want := range map[string]bool{
		"/usr/libexec/gsd-housekeeping":      true,
		"/usr/lib/systemd/systemd":           true,
		"/usr/lib/gnome-online-accounts/goa": true,
		"/usr/bin/gnome-shell":               true,
		"/usr/lib/slack/slack":               false,
		"/usr/bin/ssh":                       false,
		"/usr/lib/x86_64-linux-gnu/webkit2gtk-4.1/WebKitNetworkProcess": false, // an app's web view: its traffic is the app's
	} {
		if systemPath(path) != want {
			t.Errorf("systemPath(%s) = %v", path, !want)
		}
	}
}
