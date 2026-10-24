package app

import (
	"testing"
)

func TestSettingsApplied(t *testing.T) {
	cs, ps, _ := startCore(t, "")
	for path, value := range map[string]any{
		"log-level":                "warning",
		"dns.enable":               true,
		"dns.enhanced-mode":        "redir-host",
		"dns.nameserver":           []string{"223.5.5.5"},
		"dns.use-system-resolvers": true,
		"capture.processes":        []string{"ghostty"},
	} {
		if err := ps.Set(path, value); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	if v, _ := ps.Value("dns.enhanced-mode"); v != "redir-host" {
		t.Fatalf("value %v", v)
	}
	if err := ps.Set("dns.enhanced-mode", "nonsense"); err == nil {
		t.Fatal("the core must reject an unknown enhanced-mode")
	}
	if v, _ := ps.Value("dns.enhanced-mode"); v != "redir-host" {
		t.Fatalf("rejected setting must roll back, got %v", v)
	}
	var cfg struct {
		LogLevel string `json:"log-level"`
	}
	if err := cs.core.Do(t.Context(), "GET", "/configs", nil, &cfg); err != nil || cfg.LogLevel != "warning" {
		t.Fatalf("log-level applied: %q %v", cfg.LogLevel, err)
	}
}

func TestRate(t *testing.T) {
	for b, want := range map[int64]string{0: "0 KB/s", 999_999: "999 KB/s", 1_000_000: "1.0 MB/s", 99_949_999: "99.9 MB/s", 99_950_000: "100 MB/s"} {
		if got := rate(b); got != want {
			t.Errorf("rate(%d) = %q, want %q", b, got, want)
		}
	}
}

// Partial enhancement doesn't offer Connaio itself, browsers or the listed Chinese apps, on any platform.
func TestLeftOut(t *testing.T) {
	for _, exec := range []string{"Connaio", "Google Chrome", "chrome.exe", "WeChat", "Feishu", "企业微信"} {
		if !leftOut(exec, "Connaio") {
			t.Errorf("%s offered", exec)
		}
	}
	for _, exec := range []string{"ghostty", "Code", "curl", "Telegram.exe"} {
		if leftOut(exec, "Connaio") {
			t.Errorf("%s left out", exec)
		}
	}
}

// 测速地址 takes an http(s) address; the default and "" both mean the default.
func TestSetTestURL(t *testing.T) {
	s := &SettingsService{settings: loadSettings(t.TempDir())}
	if err := s.SetTestURL("ftp://a.com"); err == nil {
		t.Error("ftp accepted")
	}
	if err := s.SetTestURL(" https://cp.cloudflare.com/generate_204 "); err != nil || s.TestURL() != "https://cp.cloudflare.com/generate_204" {
		t.Errorf("got %q, %v", s.TestURL(), err)
	}
	if err := s.SetTestURL(""); err != nil || s.TestURL() != defaultTestURL {
		t.Errorf("reset: got %q, %v", s.TestURL(), err)
	}
}

// 不经代理 keeps plain host and range entries; one that could break a platform's list is refused.
func TestSetBypass(t *testing.T) {
	s := &SystemProxyService{settings: loadSettings(t.TempDir())}
	if err := s.SetBypass([]string{"*.corp.example", "10.1.0.0/16"}); err != nil || len(s.Bypass()) != 2 {
		t.Fatalf("got %v, %v", s.Bypass(), err)
	}
	for _, bad := range []string{"a b", "x';rm", "a;b", ""} {
		if err := s.SetBypass([]string{bad}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
