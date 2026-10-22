package app

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

const localProfile = `proxies:
  - {name: hk, type: socks5, server: 127.0.0.1, port: 1}
proxy-groups:
  - {name: 代理, type: select, proxies: [hk, DIRECT]}
rules:
  - MATCH,代理
`

// Needs a built core: CONNAIO_CORE=/path/to/connaio-core go test -run TestProfileService .
func TestProfileService(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cs, s, _ := startCore(t, "")
	c := cs.core

	hasProxy := func(name string) bool {
		var resp struct {
			Proxies map[string]any `json:"proxies"`
		}
		_ = c.Do(ctx, http.MethodGet, "/proxies", nil, &resp)
		_, ok := resp.Proxies[name]
		return ok
	}
	if hasProxy("hk") {
		t.Fatal("empty config should not have hk yet")
	}
	if err := s.AddLocal("本地", localProfile); err != nil {
		t.Fatal(err)
	}
	if !hasProxy("hk") {
		t.Fatal("first profile is applied to the running core")
	}

	parts, _ := s.OverrideParts()
	bad := parts
	bad.Clash += "prepend-rules:\n  - NOT-A-RULE,x,DIRECT\n"
	if err := s.SetOverrideParts(bad); err == nil {
		t.Fatal("the core must reject an invalid rule")
	}
	if p, _ := s.OverrideParts(); strings.Contains(p.Clash, "NOT-A-RULE") {
		t.Fatalf("rejected override must be rolled back, got %q", p.Clash)
	}
	if !hasProxy("hk") {
		t.Fatal("running config survives a rejected override")
	}

	parts.Clash += "mode: global\n"
	if err := s.SetOverrideParts(parts); err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Mode string `json:"mode"`
	}
	if err := c.Do(ctx, http.MethodGet, "/configs", nil, &cfg); err != nil || cfg.Mode != "global" {
		t.Fatalf("override applied: %q %v", cfg.Mode, err)
	}
}

// Needs a built core: CONNAIO_CORE=/path/to/connaio-core go test -run TestUserItemsApplied .
func TestUserItemsApplied(t *testing.T) {
	cs, s, _ := startCore(t, "prepend-proxy-groups:\n  - {name: 代理, type: select, proxies: [DIRECT]}\n")

	bad := map[string]any{"name": "broken", "type": "ss", "server": "1.2.3.4", "port": 8388, "cipher": "no-such-cipher", "password": "x"}
	if err := s.AddProxy(bad, "", nil); err == nil {
		t.Fatal("the core must reject an unknown cipher")
	}
	if items, _ := s.UserItems(); len(items.Proxies) != 0 {
		t.Fatalf("rejected proxy must be rolled back: %+v", items)
	}

	node := map[string]any{"name": "my-socks", "type": "socks5", "server": "127.0.0.1", "port": 1}
	if err := s.AddProxy(node, "udp: true\n", []string{"代理"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddGroup(map[string]any{"name": "自动", "type": "url-test", "proxies": []any{"my-socks", "DIRECT"}, "url": "https://www.gstatic.com/generate_204", "interval": 300}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddRule("DOMAIN-SUFFIX,example.com,自动"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddTunnel(map[string]any{"name": "vps", "type": "ssh", "server": "127.0.0.1", "port": 1, "username": "u", "password": "p",
		"forwards": []any{map[string]any{"remote": "127.0.0.1:18080", "local": "127.0.0.1:80"}}}); err != nil {
		t.Fatal(err)
	}

	groups, _ := cs.Groups()
	byName := map[string]Group{}
	for _, g := range groups {
		byName[g.Name] = g
	}
	if _, ok := byName["自动"]; !ok || len(byName["代理"].All) == 0 || byName["代理"].All[0].Name != "my-socks" {
		t.Fatalf("groups in the core: %+v", groups)
	}
	names, _ := cs.ProxyNames()
	if !slices.Contains(names, "my-socks") || slices.Contains(names, "GLOBAL") {
		t.Fatalf("proxy names %v", names)
	}
	rules, _ := cs.Rules()
	if rules[0].Payload != "example.com" || rules[0].Proxy != "自动" {
		t.Fatalf("rule in the core: %+v", rules[0])
	}
	if tunnels, _ := cs.ReverseTunnels(); len(tunnels) != 1 || tunnels[0].Name != "vps" {
		t.Fatalf("tunnel in the core: %+v", tunnels)
	}

	for _, err := range []error{s.RemoveRule("DOMAIN-SUFFIX,example.com,自动"), s.RemoveGroup("自动"), s.RemoveProxy("my-socks"), s.RemoveTunnel("vps")} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if names, _ = cs.ProxyNames(); slices.Contains(names, "my-socks") || slices.Contains(names, "自动") {
		t.Fatalf("removed items still in the core: %v", names)
	}
}

// The dialogs' output shapes must pass the core's validation.
func TestFormShapes(t *testing.T) {
	_, s, _ := startCore(t, "")
	vless := map[string]any{"name": "v", "type": "vless", "server": "1.2.3.4", "port": 443, "uuid": "b831381d-6324-4d53-ad4f-8cda48b30811",
		"flow": "xtls-rprx-vision", "tls": true, "servername": "www.example.com", "client-fingerprint": "chrome",
		"reality-opts": map[string]any{"public-key": "eOb1IqlNqhHeaJbIG1YnK5m4_V9bM0E8HeWdnmqWVlE", "short-id": "ab"}}
	if err := s.AddProxy(vless, "udp: true\n", []string{"代理"}); err != nil {
		t.Fatalf("vless form: %v", err)
	}
	ws := map[string]any{"name": "t", "type": "trojan", "server": "1.2.3.4", "port": 443, "password": "x", "network": "ws",
		"ws-opts": map[string]any{"path": "/ws", "headers": map[string]any{"Host": "cdn.example.com"}}}
	if err := s.AddProxy(ws, "", nil); err != nil {
		t.Fatalf("trojan ws form: %v", err)
	}
	ssh := map[string]any{"name": "vps", "type": "ssh", "server": "127.0.0.1", "port": 1, "username": "u", "password": "p",
		"forwards": []any{map[string]any{"remote": "0.0.0.0:8080", "local": "127.0.0.1:80"}}}
	if err := s.AddTunnel(ssh); err != nil {
		t.Fatalf("ssh form: %v", err)
	}
	if err := s.AddTunnel(map[string]any{"name": "bad", "type": "ssh", "server": "h", "port": 22, "username": "u"}); err == nil {
		t.Fatal("ssh tunnel without forwards must be rejected")
	}
}

func TestSetText(t *testing.T) {
	_, s, _ := startCore(t, "")
	if err := s.SetText("dns.enable", "true"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetText("dns.nameserver", "[223.5.5.5, 119.29.29.29]"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Value("dns.enable"); v != true {
		t.Fatalf("dns.enable = %v", v)
	}
	if v, _ := s.Value("dns.nameserver"); len(v.([]any)) != 2 {
		t.Fatalf("dns.nameserver = %v", v)
	}
	if err := s.SetText("dns..x", "1"); err == nil {
		t.Fatal("a malformed path must be refused")
	}
}

func TestDeleteRule(t *testing.T) {
	cs, s, _ := startCore(t, "prepend-rules:\n  - DOMAIN,mine.com,DIRECT\n") // then the empty profile's MATCH,DIRECT
	if err := s.DeleteRule(1); err == nil {
		t.Fatal("the catch-all MATCH must not be deletable")
	}
	if err := s.DeleteRule(0); err != nil {
		t.Fatal(err)
	}
	if rules, _ := cs.Rules(); len(rules) != 1 || rules[0].Type != "Match" {
		t.Fatalf("rules %+v", rules)
	}
	if _, err := (&ToolService{cs: cs}).DNSLeakTest(); err == nil {
		t.Fatal("with MATCH,DIRECT there is no proxy whose DNS could leak")
	}
}
