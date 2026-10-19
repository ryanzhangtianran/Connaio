package app

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
)

// VPN is one VPN as the page sees it: the node, the rules that send traffic to it, and the subnet
// group that bypasses it on chosen networks. Connected ones live in the override as ordinary user
// items, so the rules and proxies pages show the same things; every one is also kept as a draft in
// the settings, so 断开 and 连接 need no retyping.
type VPN struct {
	Proxy map[string]any `json:"proxy"`
	Rules []string       `json:"rules"` // whole rule lines, in order
	Group map[string]any `json:"group"` // the subnet group, or nil when the VPN is always used
	// Active says the node is in the override (connected); false for a draft.
	Active bool `json:"active"`
	// State is up, connecting or down: for EasyConnect the tunnel's, else whether it is active.
	State string `json:"state"`
	// Kicked says the EasyConnect login is paused: the gateway kept ending the session, the
	// account probably signed in elsewhere; ReconnectVPN resumes it.
	Kicked bool `json:"kicked"`
}

func (v VPN) name() string { n, _ := v.Proxy["name"].(string); return n }

// vpnTypes are the node types the VPN page manages; besides them, an http node marked connaio-lan:
// another Connaio on the LAN that is connected to the VPN, used through its proxy port.
var vpnTypes = []string{"easyconnect", "wireguard", "openvpn", "tailscale"}

// isVPN says whether an override node is one of the VPN page's.
func isVPN(p map[string]any) bool {
	t, _ := p["type"].(string)
	lan, _ := p["connaio-lan"].(bool)
	return slices.Contains(vpnTypes, t) || lan
}

// groupOf names the subnet group that stands in for the node when networks bypass it.
func groupOf(name string) string { return name + " 路由" }

// VPNs lists every VPN: the connected ones from the override, then the drafts.
func (s *ProfileService) VPNs() ([]VPN, error) {
	active, err := s.activeVPNs()
	if err != nil {
		return nil, err
	}
	list := active
	var tunnels []EasyConnectTunnel
	if slices.ContainsFunc(active, func(v VPN) bool { return v.Proxy["type"] == "easyconnect" }) {
		tunnels, _ = easyConnects(s.core)
	}
	for i := range list {
		list[i].State = "up"
		if list[i].Proxy["type"] == "easyconnect" {
			list[i].State = "down"
			for _, tn := range tunnels {
				if tn.Name == list[i].name() {
					list[i].State, list[i].Kicked = tn.State, tn.Kicked
				}
			}
		}
	}
	if s.settings != nil {
		drafts := s.settings.Get().VPNs
		names := make([]string, 0, len(drafts))
		for name := range drafts {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			if !slices.ContainsFunc(active, func(v VPN) bool { return v.name() == name }) {
				d := drafts[name]
				d.Active, d.State = false, "down"
				list = append(list, d)
			}
		}
	}
	return list, nil
}

func (s *ProfileService) activeVPNs() ([]VPN, error) {
	items, err := s.store.UserItems()
	if err != nil {
		return nil, err
	}
	var list []VPN
	for _, p := range items.Proxies {
		if !isVPN(p) {
			continue
		}
		v := VPN{Proxy: p, Active: true}
		name := v.name()
		if j := slices.IndexFunc(items.Groups, func(g map[string]any) bool { return g["name"] == groupOf(name) }); j >= 0 {
			v.Group = items.Groups[j]
		}
		for _, r := range items.Rules {
			if t := ruleTarget(r); t == name || t == groupOf(name) {
				v.Rules = append(v.Rules, r)
			}
		}
		list = append(list, v)
	}
	return list, nil
}

// SetVPN connects v: its node, rules and group go into the override in one edit, replacing the
// VPN named replace (its old name, when renamed; "" for a new one), and the draft is kept.
func (s *ProfileService) SetVPN(v VPN, replace string) error {
	name := v.name()
	if name == "" {
		return errors.New("VPN 需要一个名字")
	}
	active, err := s.activeVPNs()
	if err != nil {
		return err
	}
	if err := nameTaken(active, name, replace); err != nil {
		return err
	}
	s.saveDraft(v, replace)
	return s.edit(func() error {
		for _, old := range []string{replace, name} {
			if err := s.clearVPN(old); err != nil {
				return err
			}
		}
		if err := s.store.AddProxy(v.Proxy, nil); err != nil {
			return err
		}
		if v.Group != nil {
			v.Group["name"] = groupOf(name)
			if err := s.store.AddGroup(v.Group); err != nil {
				return err
			}
		}
		for i := len(v.Rules) - 1; i >= 0; i-- { // prepending in reverse keeps the order
			if err := s.store.PrependRule(v.Rules[i]); err != nil {
				return err
			}
		}
		return nil
	})
}

// saveDraft keeps v as its draft, in place of the one named replace when renamed.
func (s *ProfileService) saveDraft(v VPN, replace string) {
	if s.settings == nil {
		return
	}
	draft := v
	draft.Active, draft.State, draft.Kicked = false, "", false
	_ = s.settings.Update(func(st *Settings) {
		if st.VPNs == nil {
			st.VPNs = map[string]VPN{}
		}
		if replace != "" && replace != v.name() {
			delete(st.VPNs, replace)
		}
		st.VPNs[v.name()] = draft
	})
}

// SaveVPN keeps v's settings without connecting: a connected VPN (named replace, or v's own name
// for a new one) takes them at once, as SetVPN; a disconnected one only in its draft.
func (s *ProfileService) SaveVPN(v VPN, replace string) error {
	if v.name() == "" {
		return errors.New("VPN 需要一个名字")
	}
	active, err := s.activeVPNs()
	if err != nil {
		return err
	}
	if slices.ContainsFunc(active, func(a VPN) bool { return a.name() == cmp.Or(replace, v.name()) }) {
		return s.SetVPN(v, replace)
	}
	if err := nameTaken(active, v.name(), replace); err != nil {
		return err
	}
	s.saveDraft(v, replace)
	return nil
}

// nameTaken refuses name when another connected VPN has it (replace being the one renamed).
func nameTaken(active []VPN, name, replace string) error {
	if name != replace && slices.ContainsFunc(active, func(a VPN) bool { return a.name() == name }) {
		return errors.New("已有名为 " + name + " 的 VPN")
	}
	return nil
}

// ReconnectVPN connects a connected VPN again: an EasyConnect one drops its tunnel and logs in
// anew (also after a pause for being signed in elsewhere); the others leave the config and come
// back, which rebuilds their outbound.
func (s *ProfileService) ReconnectVPN(name string) error {
	active, err := s.activeVPNs()
	if err != nil {
		return err
	}
	i := slices.IndexFunc(active, func(v VPN) bool { return v.name() == name })
	if i < 0 {
		return errors.New(name + " 未连接")
	}
	if active[i].Proxy["type"] == "easyconnect" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return s.core.Do(ctx, http.MethodPost, "/easyconnect/reconnect", map[string]string{"name": name}, nil)
	}
	v := active[i]
	if err := s.RemoveVPN(name); err != nil {
		return err
	}
	return s.SetVPN(v, "")
}

// RemoveVPN disconnects: the node, rules and group leave the override; the draft stays.
func (s *ProfileService) RemoveVPN(name string) error {
	return s.edit(func() error { return s.clearVPN(name) })
}

// DeleteVPN disconnects and forgets the draft too.
func (s *ProfileService) DeleteVPN(name string) error {
	if err := s.RemoveVPN(name); err != nil {
		return err
	}
	if s.settings != nil {
		return s.settings.Update(func(st *Settings) { delete(st.VPNs, name) })
	}
	return nil
}

// clearVPN removes the named VPN's node, group and rules from the override, if it is there.
func (s *ProfileService) clearVPN(name string) error {
	if name == "" {
		return nil
	}
	active, err := s.activeVPNs()
	if err != nil {
		return err
	}
	i := slices.IndexFunc(active, func(v VPN) bool { return v.name() == name })
	if i < 0 {
		return nil
	}
	for _, r := range active[i].Rules {
		if err := s.store.RemoveRule(r); err != nil {
			return err
		}
	}
	if active[i].Group != nil {
		if err := s.store.RemoveGroup(groupOf(name)); err != nil {
			return err
		}
	}
	return s.store.RemoveProxy(name)
}

// ruleTarget is the policy a rule line names: the third field, or the second for MATCH.
func ruleTarget(rule string) string {
	f := strings.Split(rule, ",")
	if len(f) >= 3 {
		return strings.TrimSpace(f[2])
	}
	if len(f) == 2 && strings.EqualFold(strings.TrimSpace(f[0]), "MATCH") {
		return strings.TrimSpace(f[1])
	}
	return ""
}

// Network is what the core sees of the current network, for subnet group conditions.
type Network struct {
	SSID   string `json:"ssid"`
	Router string `json:"router"`
}

func (s *CoreService) CurrentNetwork() (Network, error) {
	var n Network
	err := s.core.Do(context.Background(), http.MethodGet, "/network", nil, &n)
	return n, err
}
