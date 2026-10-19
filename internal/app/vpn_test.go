package app

import (
	"reflect"
	"testing"
)

// The VPN page's state goes into the override as a node, rules and a subnet group, and comes back
// the same; saving again replaces all three, and removing leaves nothing behind.
func TestVPNRoundTrip(t *testing.T) {
	cs, s, _ := startCore(t, "prepend-rules:\n  - DOMAIN,mine.com,DIRECT\n")
	v := VPN{
		Proxy: map[string]any{"name": "VPN", "type": "easyconnect", "server": "vpn.example.edu.cn", "port": 443, "username": "alice", "password": "secret", "udp": true},
		Rules: []string{"EASYCONNECT,VPN,VPN 路由", "IP-CIDR,10.0.0.0/8,VPN 路由,no-resolve", "DOMAIN-SUFFIX,example.edu.cn,VPN 路由"},
		Group: map[string]any{"type": "subnet", "default": "VPN", "rules": map[string]any{"SSID:Campus": "DIRECT"}},
	}
	if err := s.SetVPN(v, ""); err != nil {
		t.Fatal(err)
	}
	list, err := s.VPNs()
	if err != nil || len(list) != 1 || !list[0].Active {
		t.Fatal(err, list)
	}
	got := &list[0]
	if !reflect.DeepEqual(got.Rules, v.Rules) || got.Group["name"] != "VPN 路由" || got.Proxy["server"] != "vpn.example.edu.cn" {
		t.Errorf("read back %+v", got)
	}
	items, _ := s.UserItems()
	if len(items.Rules) != 4 || items.Rules[3] != "DOMAIN,mine.com,DIRECT" {
		t.Errorf("rules %v", items.Rules)
	}
	if rules, _ := cs.Rules(); len(rules) < 4 {
		t.Errorf("core has %d rules", len(rules))
	}

	v.Group = nil
	v.Rules = []string{"EASYCONNECT,VPN,VPN"}
	if err := s.SetVPN(v, "VPN"); err != nil {
		t.Fatal(err)
	}
	list, _ = s.VPNs()
	got = &list[0]
	if got.Group != nil || !reflect.DeepEqual(got.Rules, v.Rules) {
		t.Errorf("after resave %+v", got)
	}
	items, _ = s.UserItems()
	if len(items.Groups) != 0 || len(items.Proxies) != 1 {
		t.Errorf("leftovers: groups %v proxies %v", items.Groups, items.Proxies)
	}

	if err := s.RemoveVPN("VPN"); err != nil {
		t.Fatal(err)
	}
	if list, _ = s.VPNs(); len(list) != 1 || list[0].Active { // disconnected, kept as a draft
		t.Errorf("after disconnect: %+v", list)
	}
	if err := s.DeleteVPN("VPN"); err != nil {
		t.Fatal(err)
	}
	if list, _ = s.VPNs(); len(list) != 0 {
		t.Errorf("still there: %+v", list)
	}
	items, _ = s.UserItems()
	if len(items.Rules) != 1 || len(items.Proxies) != 0 {
		t.Errorf("leftovers: %v %v", items.Rules, items.Proxies)
	}
}

// TestSaveVPN: saving a disconnected VPN connects nothing; a connected one takes the change. A LAN
// Connaio (an http node marked connaio-lan) is a VPN; reconnecting one puts it back.
func TestSaveVPN(t *testing.T) {
	_, s, _ := startCore(t, "")
	lan := VPN{
		Proxy: map[string]any{"name": "宿舍", "type": "http", "server": "192.168.1.20", "port": 7890, "connaio-lan": true},
		Rules: []string{"IP-CIDR,10.0.0.0/8,宿舍,no-resolve"},
	}
	if err := s.SaveVPN(lan, ""); err != nil {
		t.Fatal(err)
	}
	if items, _ := s.UserItems(); len(items.Proxies) != 0 {
		t.Fatalf("saving connected it: %v", items.Proxies)
	}
	if err := s.SetVPN(lan, ""); err != nil {
		t.Fatal(err)
	}
	lan.Proxy["port"] = 7891
	if err := s.SaveVPN(lan, "宿舍"); err != nil {
		t.Fatal(err)
	}
	list, _ := s.VPNs()
	if len(list) != 1 || !list[0].Active || list[0].Proxy["port"] != 7891 {
		t.Fatalf("a connected LAN VPN after saving: %+v", list)
	}
	if err := s.ReconnectVPN("宿舍"); err != nil {
		t.Fatal(err)
	}
	if list, _ = s.VPNs(); len(list) != 1 || !list[0].Active {
		t.Fatalf("after reconnecting: %+v", list)
	}
	if err := s.ReconnectVPN("nope"); err == nil {
		t.Fatal("reconnecting a VPN that isn't connected")
	}
}
