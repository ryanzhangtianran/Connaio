package app

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/ryanzhangtianran/Connaio/internal/profile"
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
	// State is up, connecting, login or down: for EasyConnect the tunnel's, for Tailscale its
	// login's (login: waiting to be signed in at AuthURL), else whether it is active.
	State   string   `json:"state"`
	AuthURL string   `json:"authURL,omitempty"`
	IPs     []string `json:"ips,omitempty"` // a Tailscale node's addresses in the tailnet
	// Routes are the subnets the tailnet's nodes advertise, for the page to pick from
	Routes []TailscaleRoute `json:"routes,omitempty"`
	// Kicked says the EasyConnect login is paused: the gateway kept ending the session, the
	// account probably signed in elsewhere; ReconnectVPN resumes it.
	Kicked bool `json:"kicked"`
	// Kicks is how often the gateway ended the session in the last 10 minutes.
	Kicks int `json:"kicks"`
	// Profile says the node comes from the profile, not the override: 断开 and 删除 are the
	// subscription's to do; 保存 puts an override copy in its place.
	Profile bool `json:"profile"`
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
	var tails []TailscaleStatus
	if slices.ContainsFunc(active, func(v VPN) bool { return v.Proxy["type"] == "tailscale" }) {
		tails, _ = tailscales(s.core)
	}
	for i := range list {
		list[i].State = "up"
		if list[i].Proxy["type"] == "tailscale" {
			list[i].State = "connecting"
			for _, ts := range tails {
				if ts.Name == list[i].name() {
					list[i].State, list[i].AuthURL, list[i].IPs, list[i].Routes = tailscaleState(ts), ts.AuthURL, ts.IPs, ts.Routes
				}
			}
		}
		if list[i].Proxy["type"] == "easyconnect" {
			list[i].State = "down"
			for _, tn := range tunnels {
				if tn.Name == list[i].name() {
					list[i].State, list[i].Kicked, list[i].Kicks = tn.State, tn.Kicked, tn.Kicks
				}
			}
		}
	}
	drafts := s.settings.Get().VPNs
	for _, name := range slices.Sorted(maps.Keys(drafts)) {
		if !slices.ContainsFunc(active, func(v VPN) bool { return v.name() == name }) {
			d := drafts[name]
			d.Active, d.State = false, "down"
			list = append(list, d)
		}
	}
	return list, nil
}

// tailscaleState is the page's state for a Tailscale node's: in the tailnet, waiting to be signed in
// (or approved by the tailnet's admin), failed, or still starting.
func tailscaleState(ts TailscaleStatus) string {
	switch {
	case ts.State == "Running":
		return "up"
	case ts.State == "NeedsLogin" || ts.State == "NeedsMachineAuth":
		return "login"
	case ts.Error != "" || ts.State == "Stopped":
		return "down"
	}
	return "connecting"
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
			if t := profile.RuleTarget(r); t == name || t == groupOf(name) {
				v.Rules = append(v.Rules, r)
			}
		}
		list = append(list, v)
	}
	// the profile's VPN nodes too: a subscription can carry one; its rules stay where the profile has them
	proxies, err := s.store.Value(false, []string{"proxies"})
	if err != nil {
		return nil, err
	}
	ps, _ := proxies.([]any)
	for _, x := range ps {
		p, _ := x.(map[string]any)
		if p == nil || !isVPN(p) || slices.ContainsFunc(list, func(v VPN) bool { return v.name() == p["name"] }) {
			continue
		}
		list = append(list, VPN{Proxy: maps.Clone(p), Active: true, Profile: true})
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
	draft := v
	draft.Active, draft.State, draft.Kicked, draft.Kicks, draft.AuthURL, draft.IPs, draft.Routes = false, "", false, 0, "", nil, nil
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
	return s.SetVPN(v, name) // a profile's VPN is still there: replaced, not taken
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
	return s.settings.Update(func(st *Settings) { delete(st.VPNs, name) })
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
	if i < 0 || active[i].Profile { // the profile's stays; an override copy replaces it at Merge
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
