package profile

import (
	"strings"
	"testing"
)

// The override splits into what any Clash client reads and what only Connaio's core does, and
// joins back with Connaio's items first; an import merges over it.
func TestSplitJoinImport(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetOverride(`dns:
  enable: true
  use-system-resolvers: true
prepend-proxies:
  - name: VPN
    type: easyconnect
    server: a.edu
  - name: N
    type: socks5
    server: b.com
    port: 1080
prepend-rules:
  - DOMAIN,a.com,DIRECT
  - IP-CIDR,10.0.0.0/8,VPN,no-resolve
  - PROCESS-ANCESTOR,ghostty,N
reverse-tunnels:
  - name: t
    type: wireguard
`); err != nil {
		t.Fatal(err)
	}
	clash, connaio, err := s.SplitOverride()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"enable: true", "name: N", "DOMAIN,a.com,DIRECT"} {
		if !strings.Contains(clash, want) || strings.Contains(connaio, want) {
			t.Errorf("%q should be in the Clash part only:\n%s\n---\n%s", want, clash, connaio)
		}
	}
	for _, want := range []string{"use-system-resolvers", "name: VPN", "10.0.0.0/8,VPN", "PROCESS-ANCESTOR", "reverse-tunnels"} {
		if !strings.Contains(connaio, want) || strings.Contains(clash, want) {
			t.Errorf("%q should be in the Connaio part only:\n%s\n---\n%s", want, clash, connaio)
		}
	}

	if err := s.JoinOverride(clash, connaio); err != nil {
		t.Fatal(err)
	}
	joined, _ := s.Override()
	if i, j := strings.Index(joined, "10.0.0.0/8"), strings.Index(joined, "DOMAIN,a.com"); i < 0 || j < 0 || i > j {
		t.Errorf("Connaio's rules first:\n%s", joined)
	}
	if !strings.Contains(joined, "enable: true") || !strings.Contains(joined, "use-system-resolvers: true") {
		t.Errorf("dns merged back:\n%s", joined)
	}

	if err := s.ImportOverride("prepend-rules:\n  - DOMAIN,b.com,DIRECT\nprepend-proxies:\n  - name: N\n    type: socks5\n    server: c.com\n    port: 1080\n"); err != nil {
		t.Fatal(err)
	}
	merged, _ := s.Override()
	if !strings.Contains(merged, "DOMAIN,b.com") || !strings.Contains(merged, "DOMAIN,a.com") {
		t.Errorf("imported rules appended to the old:\n%s", merged)
	}
	if strings.Contains(merged, "b.com\n    port") || !strings.Contains(merged, "c.com") || strings.Count(merged, "name: N") != 1 {
		t.Errorf("the imported N replaces the old one:\n%s", merged)
	}
}
