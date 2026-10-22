package profile

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

const sub = `mixed-port: 7890
external-controller: 127.0.0.1:9090
secret: abc
dns: {enable: true, nameserver: [223.5.5.5]}
proxies:
  - {name: hk, type: socks5, server: 1.2.3.4, port: 1080}
proxy-groups:
  - {name: 代理, type: select, proxies: [hk]}
rules:
  - MATCH,代理
`

func TestStore(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		if r.URL.Path == "/base64" {
			w.Write([]byte("dm1lc3M6Ly9leGFtcGxl"))
			return
		}
		w.Header().Set("subscription-userinfo", "upload=10; download=20; total=1073741824; expire=1893456000")
		w.Header().Set("profile-update-interval", "12")
		w.Write([]byte(sub))
	}))
	defer srv.Close()

	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRemote("bad", srv.URL+"/base64"); err == nil {
		t.Fatal("a base64 link list must be rejected")
	}
	p, err := s.AddRemote("机场", srv.URL+"/sub")
	if err != nil {
		t.Fatal(err)
	}
	if gotUA != UserAgent || p.Usage == nil || p.Usage.Total != 1<<30 || p.Usage.Expire != 1893456000 || p.Interval != 12 {
		t.Fatalf("subscription metadata: ua=%q %+v usage=%+v", gotUA, p, p.Usage)
	}
	if cur, list := s.List(); cur != p.ID || len(list) != 1 {
		t.Fatalf("first profile should become current: %s %v", cur, list)
	}
	if err := s.Rename(p.ID, "  "); err == nil {
		t.Fatal("a blank name must be rejected")
	}
	if err := s.Rename(p.ID, " 公司 "); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(p.ID); err != nil {
		t.Fatal(err)
	}
	if _, list := s.List(); list[0].Name != "公司" {
		t.Fatalf("renamed, then updated: %q, want 公司", list[0].Name)
	}

	if err := s.SetOverride("not: [valid"); err == nil {
		t.Fatal("broken override accepted")
	}
	if err := s.SetOverride(`
dns: {nameserver: [https://1.1.1.1/dns-query]}
prepend-rules:
  - DOMAIN,corp.example,DIRECT
tun: {stack: system}
`); err != nil {
		t.Fatal(err)
	}
	b, err := s.Build(false)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["external-controller"] != nil || cfg["secret"] != nil {
		t.Fatal("control keys must be stripped")
	}
	if cfg["mixed-port"] != 7890 || cfg["find-process-mode"] != "always" {
		t.Fatalf("profile beats defaults, defaults fill the rest: %v %v", cfg["mixed-port"], cfg["find-process-mode"])
	}
	dns := cfg["dns"].(map[string]any)
	if dns["enable"] != true || dns["nameserver"].([]any)[0] != "https://1.1.1.1/dns-query" {
		t.Fatalf("maps merge deeply, lists are replaced: %v", dns)
	}
	if rules := cfg["rules"].([]any); len(rules) != 2 || rules[0] != "DOMAIN,corp.example,DIRECT" {
		t.Fatalf("prepend-rules: %v", rules)
	}
	if cfg["prepend-rules"] != nil {
		t.Fatal("prepend-rules must not reach the core")
	}

	if due := s.Due(time.Now().Add(13 * time.Hour)); len(due) != 1 {
		t.Fatalf("due after the interval: %v", due)
	}
	if _, err := s.Update(p.ID); err != nil {
		t.Fatal(err)
	}

	local, err := s.AddLocal("本地", []byte(sub))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(p.ID); err != nil {
		t.Fatal(err)
	}
	if cur, _ := s.List(); cur != local.ID {
		t.Fatalf("removing the current profile selects the next one, got %q", cur)
	}

	reopened, err := Open(s.dir[:len(s.dir)-len("/profiles")])
	if err != nil {
		t.Fatal(err)
	}
	if cur, list := reopened.List(); cur != local.ID || len(list) != 1 {
		t.Fatalf("index persists: %s %v", cur, list)
	}
}

func TestBuildWithoutProfile(t *testing.T) {
	s, _ := Open(t.TempDir())
	b, err := s.Build(false)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	_ = yaml.Unmarshal(b, &cfg)
	if cfg["mixed-port"] != 7890 || cfg["rules"].([]any)[0] != "MATCH,DIRECT" || cfg["proxy-groups"] != nil {
		t.Fatalf("empty build: %v", cfg)
	}
}

func TestPrependRule(t *testing.T) {
	s, _ := Open(t.TempDir())
	if err := s.PrependRule("DOMAIN,a.com,DIRECT"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOverride("# mine\nmode: rule # keep\nprepend-rules:\n  - DOMAIN,a.com,DIRECT\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.PrependRule("DOMAIN-SUFFIX,b.com,代理"); err != nil {
		t.Fatal(err)
	}
	text, _ := s.Override()
	var m map[string]any
	_ = yaml.Unmarshal([]byte(text), &m)
	rules := m["prepend-rules"].([]any)
	if len(rules) != 2 || rules[0] != "DOMAIN-SUFFIX,b.com,代理" || m["mode"] != "rule" {
		t.Fatalf("prepend: %v", m)
	}
	if !strings.Contains(text, "# mine") || !strings.Contains(text, "# keep") {
		t.Fatalf("comments lost:\n%s", text)
	}
}

func TestCapture(t *testing.T) {
	cfg := map[string]any{"capture": map[string]any{"mode": "system-proxy"}}
	Capture(cfg, true)
	if cfg["capture"].(map[string]any)["mode"] != "tun" || cfg["dns"].(map[string]any)["enable"] != true {
		t.Fatalf("enhanced: %v", cfg)
	}
	if !slices.Contains(cfg["dns"].(map[string]any)["fake-ip-filter"].([]any), any("connectivity-check.ubuntu.com")) {
		t.Fatalf("connectivity checks get real IPs: %v", cfg["dns"])
	}
	cfg = map[string]any{"capture": map[string]any{"mode": "process-tun"}, "dns": map[string]any{"enable": true, "nameserver": []any{"1.1.1.1"}}}
	Capture(cfg, true)
	if cfg["capture"].(map[string]any)["mode"] != "process-tun" || cfg["dns"].(map[string]any)["nameserver"].([]any)[0] != "1.1.1.1" {
		t.Fatalf("explicit process-tun and the profile's DNS are kept: %v", cfg)
	}
	Capture(cfg, false)
	if cfg["capture"].(map[string]any)["mode"] != "system-proxy" {
		t.Fatalf("no daemon, no TUN: %v", cfg)
	}
}

func TestReplaceSystemDNS(t *testing.T) {
	dns := map[string]any{
		"direct-nameserver": []any{"system"},
		"nameserver":        []any{"https://1.1.1.1/dns-query"},
		"nameserver-policy": map[string]any{"+.lan": []any{"system"}, "+.cn": "system", "geosite:x": []any{"https://doh.pub/dns-query"}},
	}
	replaceSystemDNS(dns, []any{"192.168.8.1"})
	if got := dns["direct-nameserver"].([]any); len(got) != 1 || got[0] != "192.168.8.1" {
		t.Fatalf("system kept: %v", got)
	}
	if got := dns["nameserver"].([]any); got[0] != "https://1.1.1.1/dns-query" {
		t.Fatalf("others changed: %v", got)
	}
	policy := dns["nameserver-policy"].(map[string]any)
	if policy["+.lan"].([]any)[0] != "192.168.8.1" || policy["+.cn"].([]any)[0] != "192.168.8.1" || policy["geosite:x"].([]any)[0] != "https://doh.pub/dns-query" {
		t.Fatalf("policy: %v", policy)
	}
}

func TestUpstreamDNS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resolv.conf")
	_ = os.WriteFile(path, []byte("# resolved\nnameserver 198.18.0.2\nnameserver 192.168.8.1\nnameserver fe80::1%2\nnameserver 127.0.0.53\nsearch lan\n"), 0o644)
	if got := upstreamDNS(path); len(got) != 1 || got[0] != "192.168.8.1" {
		t.Fatalf("upstream %v, want the router only", got)
	}
}

func TestUserItems(t *testing.T) {
	s, _ := Open(t.TempDir())
	if _, err := s.AddLocal("sub", []byte(sub)); err != nil {
		t.Fatal(err)
	}
	_ = s.SetOverride("# my notes\nmode: rule\n")

	node := map[string]any{"name": "my-ss", "type": "ss", "server": "5.6.7.8", "port": 8388, "cipher": "aes-128-gcm", "password": "x"}
	if err := s.AddProxy(node, []string{"代理"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddProxy(node, nil); err == nil {
		t.Fatal("duplicate proxy name accepted")
	}
	if err := s.AddGroup(map[string]any{"name": "自动", "type": "url-test", "proxies": []any{"my-ss", "hk"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddTunnel(map[string]any{"name": "vps", "type": "ssh", "server": "h", "port": 22}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddTunnel(map[string]any{"name": "old", "type": "ssh", "server": "h", "port": 22}); err != nil {
		t.Fatal(err)
	}
	if err := s.EditTunnel("old", map[string]any{"name": "vps", "type": "ssh"}); err == nil {
		t.Fatal("rename onto an existing tunnel accepted")
	}
	if err := s.EditTunnel("old", map[string]any{"name": "new", "type": "ssh", "server": "h2", "port": 22}); err != nil {
		t.Fatal(err)
	}
	if items, _ := s.UserItems(); len(items.Tunnels) != 2 || items.Tunnels[1]["name"] != "new" || items.Tunnels[1]["server"] != "h2" {
		t.Fatalf("edited tunnel: %+v", items.Tunnels)
	}
	if err := s.SetTunnelDisabled("new", true); err != nil {
		t.Fatal(err)
	}
	if items, _ := s.UserItems(); items.Tunnels[1]["disabled"] != true || items.Tunnels[1]["server"] != "h2" {
		t.Fatalf("stopped tunnel: %+v", items.Tunnels[1])
	}
	_ = s.SetTunnelDisabled("new", true) // twice: still one key
	if err := s.SetTunnelDisabled("new", false); err != nil {
		t.Fatal(err)
	}
	if items, _ := s.UserItems(); items.Tunnels[1]["disabled"] != nil {
		t.Fatalf("started again, disabled kept: %+v", items.Tunnels[1])
	}
	if err := s.SetTunnelDisabled("nope", true); err == nil {
		t.Fatal("stopping a tunnel that isn't there accepted")
	}
	_ = s.RemoveTunnel("new")
	if err := s.PrependRule("DOMAIN,a.com,自动"); err != nil {
		t.Fatal(err)
	}

	b, _ := s.Build(false)
	var cfg map[string]any
	_ = yaml.Unmarshal(b, &cfg)
	proxies := cfg["proxies"].([]any)
	groups := cfg["proxy-groups"].([]any)
	if proxies[0].(map[string]any)["name"] != "my-ss" || len(proxies) != 2 {
		t.Fatalf("proxies: %v", proxies)
	}
	if groups[0].(map[string]any)["name"] != "自动" {
		t.Fatalf("user group first: %v", groups)
	}
	if members := groups[1].(map[string]any)["proxies"].([]any); members[0] != "my-ss" || members[1] != "hk" {
		t.Fatalf("group-members prepends into the subscription group: %v", members)
	}
	if cfg["group-members"] != nil || cfg["reverse-tunnels"].([]any)[0].(map[string]any)["name"] != "vps" {
		t.Fatalf("group-members must not reach the core; tunnels kept: %v", cfg)
	}

	items, _ := s.UserItems()
	if len(items.Proxies) != 1 || len(items.Groups) != 1 || len(items.Tunnels) != 1 || len(items.Rules) != 1 || len(items.Members["代理"]) != 1 {
		t.Fatalf("user items: %+v", items)
	}

	_ = s.RemoveProxy("my-ss")
	_ = s.RemoveGroup("自动")
	_ = s.RemoveTunnel("vps")
	_ = s.RemoveRule("DOMAIN,a.com,自动")
	items, _ = s.UserItems()
	if len(items.Proxies)+len(items.Groups)+len(items.Tunnels)+len(items.Rules)+len(items.Members["代理"]) != 0 {
		t.Fatalf("after removal: %+v", items)
	}
	if text, _ := s.Override(); !strings.Contains(text, "# my notes") {
		t.Fatalf("comments lost:\n%s", text)
	}
}

func TestBuildDoesNotLeak(t *testing.T) {
	s, _ := Open(t.TempDir())
	_ = s.AddProxy(map[string]any{"name": "n", "type": "socks5", "server": "1.1.1.1", "port": 1}, []string{"代理"})
	_, _ = s.Build(false)
	_ = s.RemoveProxy("n")
	b, _ := s.Build(false)
	if strings.Contains(string(b), "- n\n") {
		t.Fatalf("a removed proxy leaked into the next build:\n%s", b)
	}
}

func TestSetPath(t *testing.T) {
	s, _ := Open(t.TempDir())
	_ = s.SetOverride("# keep me\ndns:\n  enable: true # on\n  nameserver: [1.1.1.1]\n")
	if err := s.SetPath([]string{"dns", "enhanced-mode"}, "redir-host"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPath([]string{"dns", "nameserver"}, []string{"223.5.5.5", "119.29.29.29"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPath([]string{"capture", "processes"}, []string{"ghostty"}); err != nil {
		t.Fatal(err)
	}
	text, _ := s.Override()
	if !strings.Contains(text, "# keep me") || !strings.Contains(text, "# on") {
		t.Fatalf("comments lost:\n%s", text)
	}
	if v, _ := s.Value(false, []string{"dns", "enhanced-mode"}); v != "redir-host" {
		t.Fatalf("enhanced-mode %v", v)
	}
	if v, _ := s.Value(false, []string{"dns", "enable"}); v != true {
		t.Fatalf("sibling kept: %v", v)
	}
	if v, _ := s.Value(false, []string{"dns", "nameserver"}); len(v.([]any)) != 2 {
		t.Fatalf("nameserver replaced: %v", v)
	}
	if v, _ := s.Value(false, []string{"capture", "mode"}); v != "system-proxy" {
		t.Fatalf("default still there next to a new key: %v", v)
	}
}

func TestMergeRemoved(t *testing.T) {
	profile := map[string]any{
		"proxies": []any{map[string]any{"name": "A"}, map[string]any{"name": "B"}},
		"proxy-groups": []any{
			map[string]any{"name": "G", "proxies": []any{"A", "B"}},
			map[string]any{"name": "H", "proxies": []any{"B"}},
			map[string]any{"name": "K", "proxies": []any{"A"}},
			map[string]any{"name": "S", "type": "subnet", "default": "A", "rules": map[string]any{"SSID:x": "A", "SSID:y": "B"}},
		},
		"rules": []any{"DOMAIN,x.com,A", "IP-CIDR,1.1.1.1/32,H,no-resolve", "DOMAIN,y.com,K", "MATCH,G"},
	}
	override := map[string]any{"remove-proxies": []any{"A"}, "remove-proxy-groups": []any{"H"}}
	cfg := Merge(profile, override)
	names := func(key string) (out []string) {
		for _, v := range cfg[key].([]any) {
			out = append(out, v.(map[string]any)["name"].(string))
		}
		return out
	}
	if got := names("proxies"); !slices.Equal(got, []string{"B"}) {
		t.Fatalf("proxies %v", got)
	}
	if got := names("proxy-groups"); !slices.Equal(got, []string{"G", "K", "S"}) {
		t.Fatalf("groups %v", got)
	}
	groups := cfg["proxy-groups"].([]any)
	if g := groups[0].(map[string]any)["proxies"]; !slices.Equal(g.([]any), []any{"B"}) {
		t.Fatalf("G members %v", g)
	}
	if k := groups[1].(map[string]any)["proxies"]; !slices.Equal(k.([]any), []any{"DIRECT"}) {
		t.Fatalf("emptied K should fall back to DIRECT, got %v", k)
	}
	if s := groups[2].(map[string]any); s["default"] != "DIRECT" || len(s["rules"].(map[string]any)) != 1 {
		t.Fatalf("subnet group should drop rules to A and fall back to DIRECT, got %v", s)
	}
	if r := cfg["rules"].([]any); !slices.Equal(r, []any{"DOMAIN,y.com,K", "MATCH,G"}) {
		t.Fatalf("rules %v", r)
	}
}

func TestBuildProcessTunWithoutProcesses(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetOverride("capture:\n  mode: process-tun\n"); err != nil {
		t.Fatal(err)
	}
	b, err := s.Build(true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "mode: system-proxy") {
		t.Fatalf("process-tun with no processes should build as system-proxy:\n%s", b)
	}
	if v, _ := s.Value(true, []string{"capture", "mode"}); v != "process-tun" {
		t.Fatalf("Value should still read the user's choice, got %v", v)
	}
}

func TestProfileContent(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.AddLocal("mine", []byte("proxies:\n  - {name: a, type: socks5, server: 127.0.0.1, port: 1}\n"))
	if err != nil {
		t.Fatal(err)
	}
	edited := "proxies:\n  - {name: b, type: socks5, server: 127.0.0.1, port: 2}\n"
	if err := s.SetContent(p.ID, edited); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Content(p.ID); got != edited {
		t.Fatalf("content %q", got)
	}
	if err := s.SetContent(p.ID, "proxies: [unclosed"); err == nil {
		t.Fatal("invalid YAML must be refused")
	}
	if got, _ := s.Content(p.ID); got != edited {
		t.Fatalf("a refused edit must not change the file, got %q", got)
	}
}

func TestRuleEdits(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetOverride("prepend-rules:\n  - DOMAIN,a.com,DIRECT\n  - DOMAIN,b.com,DIRECT\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveRule(1, -1); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveRule(1, 1); err == nil {
		t.Fatal("moving a GUI rule past its own list (into the profile's) must be refused")
	}
	if err := s.HideRule("MATCH,DIRECT"); err != nil { // the empty profile's only rule
		t.Fatal(err)
	}
	if v, _ := s.Value(false, []string{"rules"}); !slices.Equal(v.([]any), []any{"DOMAIN,b.com,DIRECT", "DOMAIN,a.com,DIRECT"}) {
		t.Fatalf("rules %v", v)
	}
}

func TestEditProxy(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddLocal("p", []byte("proxies:\n  - {name: A, type: ss, server: 1.1.1.1, port: 1, udp: true, plugin: obfs}\n")); err != nil {
		t.Fatal(err)
	}
	if err := s.AddProxy(map[string]any{"name": "B", "type": "ss", "server": "3.3.3.3", "port": 3, "plugin": "obfs", "plugin-opts": map[string]any{"mode": "tls"}}, nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"A", "B"} {
		if err := s.EditProxy(name, map[string]any{"server": "2.2.2.2", "udp": nil, "plugin-opts": map[string]any{"mode": nil}}); err != nil {
			t.Fatal(err)
		}
	}
	v, _ := s.Value(false, []string{"proxies"})
	for _, p := range v.([]any) {
		m := p.(map[string]any)
		_, udp := m["udp"]
		_, opts := m["plugin-opts"]
		if m["server"] != "2.2.2.2" || udp || opts || m["plugin"] != "obfs" {
			t.Fatalf("edited %v: fields not in the patch must stay, nulls must remove", m)
		}
	}
}

func TestFormat(t *testing.T) {
	got, err := Format("dns:\n        enable: true # on\n        nameserver: [1.1.1.1]\nrules:\n- MATCH,DIRECT\n")
	if err != nil {
		t.Fatal(err)
	}
	want := "dns:\n  enable: true # on\n  nameserver: [1.1.1.1]\nrules:\n  - MATCH,DIRECT\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if _, err := Format("a: [unclosed"); err == nil {
		t.Fatal("invalid YAML must be refused")
	}
	if got, _ := Format("  \n"); got != "" {
		t.Fatalf("blank formats to %q", got)
	}
}

// TestMergeRemovedNameReused: a GUI proxy or group named like a removed profile item is kept, with
// the rules that point at it.
func TestMergeRemovedNameReused(t *testing.T) {
	profile := map[string]any{
		"proxies":      []any{map[string]any{"name": "HK", "server": "old"}},
		"proxy-groups": []any{map[string]any{"name": "G", "proxies": []any{"HK"}}},
		"rules":        []any{"DOMAIN,x.com,HK", "MATCH,G"},
	}
	override := map[string]any{
		"remove-proxies": []any{"HK"}, "remove-proxy-groups": []any{"G"},
		"prepend-proxies":      []any{map[string]any{"name": "HK", "server": "new"}},
		"prepend-proxy-groups": []any{map[string]any{"name": "G", "proxies": []any{"HK"}}},
	}
	cfg := Merge(profile, override)
	if p := cfg["proxies"].([]any); len(p) != 1 || p[0].(map[string]any)["server"] != "new" {
		t.Fatalf("proxies %v", p)
	}
	if g := cfg["proxy-groups"].([]any); len(g) != 1 {
		t.Fatalf("groups %v", g)
	}
	if r := cfg["rules"].([]any); !slices.Equal(r, []any{"DOMAIN,x.com,HK", "MATCH,G"}) {
		t.Fatalf("rules %v", r)
	}
}

func TestMergeRemoveRules(t *testing.T) {
	cfg := Merge(map[string]any{"rules": []any{"DOMAIN,x.com,DIRECT", "MATCH,DIRECT"}}, map[string]any{"remove-rules": []any{"DOMAIN,x.com,DIRECT"}})
	if r := cfg["rules"].([]any); !slices.Equal(r, []any{"MATCH,DIRECT"}) || cfg["remove-rules"] != nil {
		t.Fatalf("%v", cfg)
	}
}

func TestMergePatchProxies(t *testing.T) {
	profile := map[string]any{"proxies": []any{map[string]any{"name": "A", "server": "1.1.1.1", "port": 1, "udp": true}}}
	cfg := Merge(profile, map[string]any{"patch-proxies": map[string]any{"A": map[string]any{"server": "2.2.2.2"}}})
	a := cfg["proxies"].([]any)[0].(map[string]any)
	if a["server"] != "2.2.2.2" || a["udp"] != true || cfg["patch-proxies"] != nil {
		t.Fatalf("%v / %v", a, cfg["patch-proxies"])
	}
}

// A torn index.json is moved aside instead of keeping the app from launching, and a failed
// auto-update waits a full interval before the next try.
func TestTornIndexAndUpdateBackoff(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "profiles"), 0o755)
	os.WriteFile(filepath.Join(dir, "profiles", "index.json"), []byte("{\"current\": \"ab"), 0o644)
	s, err := Open(dir)
	if err != nil || len(s.index.Profiles) != 0 {
		t.Fatalf("Open: %v %+v", err, s.index)
	}
	if _, err := os.Stat(filepath.Join(dir, "profiles", "index.json.bad")); err != nil {
		t.Error("the torn index was not moved aside")
	}
	now := time.Now()
	s.index.Profiles = []Profile{{ID: "a", URL: "http://x", Interval: 1, UpdatedAt: now.Add(-2 * time.Hour)}}
	if got := s.Due(now); len(got) != 1 {
		t.Errorf("due before a failure: %v", got)
	}
	s.index.Profiles[0].LastTried = now.Add(-10 * time.Minute)
	if got := s.Due(now); len(got) != 0 {
		t.Errorf("due right after a failure: %v", got)
	}
	if err := WriteAtomic(filepath.Join(dir, "x"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "x.tmp")); err == nil {
		t.Error("temp file left behind")
	}
}

func TestOverrideIsWrittenInBlockStyle(t *testing.T) {
	s, _ := Open(t.TempDir())
	if err := s.SetOverride("prepend-proxies: [{name: VPN, type: easyconnect}]\nprepend-rules: ['EASYCONNECT,VPN,VPN']\ngroup-members: {}\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddProxy(map[string]any{"name": "b", "type": "ss"}, nil); err != nil {
		t.Fatal(err)
	}
	text, _ := s.Override()
	if !strings.Contains(text, "prepend-proxies:\n    - name: VPN\n") || !strings.Contains(text, "prepend-rules:\n    - 'EASYCONNECT,VPN,VPN'\n") || strings.Contains(text, "group-members") { // emptied: tidied away
		t.Errorf("override:\n%s", text)
	}
}

func TestGUIProxiesAreWrittenNameTypeServerFirst(t *testing.T) {
	s, _ := Open(t.TempDir())
	if err := s.AddProxy(map[string]any{"username": "u", "udp": true, "type": "easyconnect", "server": "g", "port": 443, "name": "VPN", "password": "p"}, nil); err != nil {
		t.Fatal(err)
	}
	text, _ := s.Override()
	want := "prepend-proxies:\n    - name: VPN\n      type: easyconnect\n      server: g\n      port: 443\n      username: u\n      password: p\n      udp: true\n"
	if !strings.Contains(text, want) {
		t.Errorf("override:\n%s", text)
	}
}

func TestRuleTarget(t *testing.T) {
	for rule, want := range map[string]string{"EASYCONNECT,VPN,VPN 路由": "VPN 路由", "IP-CIDR,10.0.0.0/8,VPN,no-resolve": "VPN", "MATCH,DIRECT": "DIRECT", "DOMAIN": ""} {
		if got := RuleTarget(rule); got != want {
			t.Errorf("RuleTarget(%q) = %q, want %q", rule, got, want)
		}
	}
}

// TestEditGroup: a profile group's edit is swapped in whole, a user group's replaced in place,
// both keep their name and place.
func TestEditGroup(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddLocal("p", []byte("proxies:\n  - {name: A, type: ss, server: 1.1.1.1, port: 1}\nproxy-groups:\n  - {name: X, type: select, proxies: [DIRECT]}\n  - {name: G, type: subnet, default: DIRECT, rules: {\"SSID:a\": DIRECT}}\n")); err != nil {
		t.Fatal(err)
	}
	if err := s.AddGroup(map[string]any{"name": "U", "type": "select", "proxies": []any{"DIRECT"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.EditGroup("G", map[string]any{"name": "renamed", "type": "subnet", "default": "X", "rules": map[string]any{"SSID:b": "DIRECT"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.EditGroup("U", map[string]any{"type": "select", "proxies": []any{"X", "DIRECT"}}); err != nil {
		t.Fatal(err)
	}
	v, _ := s.Value(false, []string{"proxy-groups"})
	groups, _ := v.([]any)
	var names []string
	for _, g := range groups {
		names = append(names, groupName(g))
	}
	if !slices.Equal(names, []string{"U", "X", "G"}) {
		t.Fatalf("groups %v", names)
	}
	g, u := groups[2].(map[string]any), groups[0].(map[string]any)
	if g["default"] != "X" || len(g["rules"].(map[string]any)) != 1 || g["rules"].(map[string]any)["SSID:b"] != "DIRECT" {
		t.Fatalf("G = %v", g)
	}
	if len(u["proxies"].([]any)) != 2 {
		t.Fatalf("U = %v", u)
	}
}

// TestTidyOverride: an undone edit leaves no empty or dangling keys; keys the user wrote stay.
func TestTidyOverride(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddLocal("p", []byte("proxies:\n  - {name: A, type: ss, server: 1.1.1.1, port: 1}\nproxy-groups:\n  - {name: G, type: select, proxies: [A]}\n")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOverride("rules: []\ngroup-members: {}\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.EditProxy("A", map[string]any{"udp": true}); err != nil {
		t.Fatal(err)
	}
	if err := s.EditGroup("G", map[string]any{"type": "select", "proxies": []any{"DIRECT"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.PrependRule("DOMAIN,a.com,DIRECT"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveRule("DOMAIN,a.com,DIRECT"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveGroup("G"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveProxy("A"); err != nil {
		t.Fatal(err)
	}
	text, _ := s.Override()
	for _, gone := range []string{"group-members", "patch-proxies", "replace-proxy-groups", "prepend-rules"} {
		if strings.Contains(text, gone) {
			t.Fatalf("%s left behind:\n%s", gone, text)
		}
	}
	if !strings.Contains(text, "rules: []") {
		t.Fatalf("the user's own key went:\n%s", text)
	}
}

// An override proxy or group with a profile item's name replaces it: the core refuses a name twice.
func TestMergeReplacesSameName(t *testing.T) {
	profile := map[string]any{
		"proxies":      []any{map[string]any{"name": "VPN", "server": "old"}, map[string]any{"name": "B"}},
		"proxy-groups": []any{map[string]any{"name": "G", "proxies": []any{"B"}}},
		"rules":        []any{"MATCH,DIRECT"},
	}
	override := map[string]any{
		"prepend-proxies":     []any{map[string]any{"name": "VPN", "server": "new"}},
		"append-proxy-groups": []any{map[string]any{"name": "G", "proxies": []any{"VPN"}}},
		"prepend-rules":       []any{"MATCH,DIRECT"},
	}
	cfg := Merge(profile, override)
	if ps := cfg["proxies"].([]any); len(ps) != 2 || ps[0].(map[string]any)["server"] != "new" {
		t.Fatalf("proxies %v", ps)
	}
	if gs := cfg["proxy-groups"].([]any); len(gs) != 1 || gs[0].(map[string]any)["proxies"].([]any)[0] != "VPN" {
		t.Fatalf("groups %v", gs)
	}
	if rs := cfg["rules"].([]any); len(rs) != 2 { // rules have no name: both kept
		t.Fatalf("rules %v", rs)
	}
}

// TestBuildDropsFRPTunnels: a tunnel of the removed FRP type in an old override doesn't reach the core.
func TestBuildDropsFRPTunnels(t *testing.T) {
	s, _ := Open(t.TempDir())
	if err := s.SetOverride("reverse-tunnels:\n  - {name: old, type: frp, config: {serverAddr: x}}\n  - {name: vps, type: ssh, server: h, port: 22, username: u, private-key: k, forwards: [{remote: '127.0.0.1:1', local: '127.0.0.1:2'}]}\n"); err != nil {
		t.Fatal(err)
	}
	b, err := s.Build(false)
	if err != nil {
		t.Fatal(err)
	}
	if text := string(b); strings.Contains(text, "frp") || !strings.Contains(text, "name: vps") {
		t.Fatalf("built config:\n%s", text)
	}
}

func TestTunnelDirect(t *testing.T) {
	cfg := map[string]any{"rules": []any{"IN-NAME,OLA,DIRECT", "IN-NAME,web,代理", "MATCH,DIRECT"}}
	tunnelDirect(cfg)
	rules, proxies := cfg["rules"].([]any), cfg["proxies"].([]any)
	if rules[0] != "IN-NAME,OLA,"+TunnelDirect || rules[1] != "IN-NAME,web,代理" || rules[2] != "MATCH,DIRECT" || len(proxies) != 1 {
		t.Fatalf("tunnel rules: %v %v", rules, proxies)
	}
	cfg = map[string]any{"rules": []any{"MATCH,DIRECT"}}
	if tunnelDirect(cfg); cfg["proxies"] != nil {
		t.Fatalf("no tunnel, no outbound: %v", cfg)
	}
}

func TestDropDangling(t *testing.T) {
	// a subscription update dropped A, which an edited group and a dialer-proxy still name
	profile := map[string]any{
		"proxies":      []any{map[string]any{"name": "B", "dialer-proxy": "A"}, map[string]any{"name": "C"}},
		"proxy-groups": []any{map[string]any{"name": "G", "proxies": []any{"C"}}, map[string]any{"name": "E", "use": []any{"p"}, "proxies": []any{}}},
		"rules":        []any{"SUB-RULE,(NETWORK,tcp),sub", "MATCH,G"},
	}
	override := map[string]any{"replace-proxy-groups": map[string]any{"G": map[string]any{"name": "G", "proxies": []any{"A", "B"}}}}
	cfg := Merge(profile, override)
	groups := cfg["proxy-groups"].([]any)
	if g := groups[0].(map[string]any)["proxies"]; !slices.Equal(g.([]any), []any{"B"}) {
		t.Fatalf("edited group keeps a dropped node: %v", g)
	}
	if e := groups[1].(map[string]any)["proxies"]; len(e.([]any)) != 0 {
		t.Fatalf("a provider group's empty list filled: %v", e)
	}
	if b := cfg["proxies"].([]any)[0].(map[string]any); b["dialer-proxy"] != nil {
		t.Fatalf("dialer-proxy to a dropped node kept: %v", b)
	}
	if r := cfg["rules"].([]any); len(r) != 2 {
		t.Fatalf("rules %v", r)
	}
}

func TestRemoveUserItems(t *testing.T) {
	s, _ := Open(t.TempDir())
	if _, err := s.AddLocal("sub", []byte(sub)); err != nil {
		t.Fatal(err)
	}
	built := func() map[string]any {
		b, err := s.Build(false)
		if err != nil {
			t.Fatal(err)
		}
		var cfg map[string]any
		_ = yaml.Unmarshal(b, &cfg)
		return cfg
	}
	group := func(cfg map[string]any, name string) map[string]any {
		for _, g := range cfg["proxy-groups"].([]any) {
			if g.(map[string]any)["name"] == name {
				return g.(map[string]any)
			}
		}
		return nil
	}
	_ = s.AddProxy(map[string]any{"name": "P", "type": "direct"}, []string{"代理"})
	_ = s.AddGroup(map[string]any{"name": "UG", "type": "select", "proxies": []any{"P"}})
	_ = s.AddGroup(map[string]any{"name": "S", "type": "subnet", "default": "UG", "rules": map[string]any{"SSID:x": "P"}})
	_ = s.PrependRule("DOMAIN,a.com,UG")
	_ = s.PrependRule("DOMAIN,b.com,P")
	if err := s.AddGroup(map[string]any{"name": "GLOBAL", "type": "select", "proxies": []any{"DIRECT"}}); err == nil {
		t.Fatal("a group named GLOBAL accepted")
	}

	if err := s.EditGroup("代理", map[string]any{"type": "select", "proxies": []any{"hk"}}); err != nil {
		t.Fatal(err)
	}
	if g := group(built(), "代理")["proxies"].([]any); !slices.Equal(g, []any{"hk"}) {
		t.Fatalf("a member unticked in the edit came back: %v", g)
	}

	_ = s.RemoveProxy("P")
	cfg := built()
	if g := group(cfg, "UG")["proxies"].([]any); !slices.Equal(g, []any{"DIRECT"}) {
		t.Fatalf("UG after removing P: %v", g)
	}
	if r := group(cfg, "S")["rules"].(map[string]any); len(r) != 0 {
		t.Fatalf("subnet rule to removed P kept: %v", r)
	}
	_ = s.RemoveGroup("UG")
	cfg = built()
	if group(cfg, "S")["default"] != "DIRECT" || slices.ContainsFunc(cfg["rules"].([]any), func(r any) bool { return strings.Contains(r.(string), ".com,") }) {
		t.Fatalf("references to removed P and UG kept: %v %v", group(cfg, "S"), cfg["rules"])
	}
}

func TestInsertRule(t *testing.T) {
	s, _ := Open(t.TempDir())
	_ = s.SetOverride("prepend-rules:\n  - IP-CIDR,10.0.0.0/8,VPN 路由\n  - DOMAIN,a.com,DIRECT\n  - IP-CIDR,172.16.0.0/12,VPN\n  - DOMAIN,b.com,DIRECT\n")
	vpn := func(r string) bool { t := RuleTarget(r); return t == "VPN" || t == "VPN 路由" }
	if err := s.InsertRule("PROCESS-NAME,ssh,DIRECT", vpn); err != nil {
		t.Fatal(err)
	}
	_ = s.InsertRule("DOMAIN,c.com,DIRECT", func(string) bool { return false }) // no VPN: first
	items, _ := s.UserItems()
	want := []string{"DOMAIN,c.com,DIRECT", "IP-CIDR,10.0.0.0/8,VPN 路由", "DOMAIN,a.com,DIRECT", "IP-CIDR,172.16.0.0/12,VPN", "PROCESS-NAME,ssh,DIRECT", "DOMAIN,b.com,DIRECT"}
	if !slices.Equal(items.Rules, want) {
		t.Fatalf("rules %v", items.Rules)
	}
}
