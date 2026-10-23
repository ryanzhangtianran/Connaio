package profile

import (
	"fmt"
	"maps"
	"net"
	"os"
	"runtime"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// appDefaults are what Connaio needs; a profile and the override may change them.
var appDefaults = map[string]any{
	"mixed-port":        7890,
	"mode":              "rule",
	"log-level":         "info",
	"find-process-mode": "always", // the activity page ranks traffic by process
	"capture":           map[string]any{"mode": "system-proxy"},
	"unified-delay":     true, // latency tests time a request on an open connection, like Surge, not the handshakes
}

// emptyProfile is used until the first profile is added. A fresh copy each time: Merge writes into
// the profile's maps.
func emptyProfile() map[string]any {
	return map[string]any{"rules": []any{"MATCH,DIRECT"}}
}

// controlKeys are dropped from profiles: the GUI reaches the core over its own Unix socket.
var controlKeys = []string{"external-controller", "external-controller-tls", "external-controller-unix",
	"external-controller-pipe", "external-controller-cors", "secret", "external-ui", "external-ui-url", "external-ui-name"}

// Build returns the config for the core: app defaults, then the current profile, then the override.
// enhanced is the GUI's enhanced-mode switch: TUN capture needs the root daemon.
func (s *Store) Build(enhanced bool) ([]byte, error) {
	cfg, err := s.merged(enhanced)
	if err != nil {
		return nil, err
	}
	// process-tun with no processes picked yet captures nothing, and the core refuses it: run without
	// TUN until some are picked. Value still reads process-tun, so the GUI keeps showing the choice.
	if capture, _ := cfg["capture"].(map[string]any); capture != nil && capture["mode"] == "process-tun" {
		if ps, _ := capture["processes"].([]any); len(ps) == 0 {
			capture["mode"] = "system-proxy"
		}
	}
	tunnelDirect(cfg)
	// an FRP or SSH tunnel from before (support removed) is left out, or the core would refuse the config
	if tunnels, ok := cfg["reverse-tunnels"].([]any); ok {
		cfg["reverse-tunnels"] = slices.DeleteFunc(slices.Clone(tunnels), func(t any) bool { m, _ := t.(map[string]any); return m["type"] != "wireguard" })
	}
	return yaml.Marshal(cfg)
}

// TunnelDirect is the DIRECT of its own that what reverse tunnels bring in goes out through.
const TunnelDirect = "TUNNEL-DIRECT"

// tunnelDirect points IN-NAME rules at DIRECT (the GUI's, taking a tunnel to the proxy port) to
// TunnelDirect instead. A DIRECT rejects a connection from one it dialed itself as a loop, so with
// the tunnel's hop to the proxy port dialed by the same DIRECT, the requests it carried couldn't go
// out DIRECT: on a network where the rules pick DIRECT, nothing got through the tunnel.
func tunnelDirect(cfg map[string]any) {
	rules, _ := cfg["rules"].([]any)
	found := false
	for i, r := range rules {
		s, _ := r.(string)
		if f := strings.Split(s, ","); len(f) >= 3 && strings.TrimSpace(f[0]) == "IN-NAME" && strings.TrimSpace(f[2]) == "DIRECT" {
			f[2] = TunnelDirect
			rules[i], found = strings.Join(f, ","), true
		}
	}
	if proxies, _ := cfg["proxies"].([]any); found && !slices.ContainsFunc(proxies, func(p any) bool { return groupName(p) == TunnelDirect }) {
		cfg["proxies"] = append(proxies, map[string]any{"name": TunnelDirect, "type": "direct"})
	}
}

// merged is the config as the user set it: defaults, profile and override, aligned with enhanced mode.
func (s *Store) merged(enhanced bool) (map[string]any, error) {
	s.mu.Lock()
	current := s.index.Current
	s.mu.Unlock()

	profile := emptyProfile()
	if current != "" {
		b, err := os.ReadFile(s.path(current + ".yaml"))
		if err != nil {
			return nil, err
		}
		if profile, err = parseMap(b); err != nil {
			return nil, fmt.Errorf("配置文件无效: %w", err)
		}
	}
	override := map[string]any{}
	if text, err := s.Override(); err != nil {
		return nil, err
	} else if text != "" {
		if override, err = parseMap([]byte(text)); err != nil {
			return nil, fmt.Errorf("覆写无效: %w", err)
		}
	}
	cfg := Merge(profile, override)
	Capture(cfg, enhanced)
	return cfg, nil
}

// tunDNS is used when TUN hijacks DNS and the profile brings no DNS section of its own.
var tunDNS = map[string]any{
	"enable":             true,
	"enhanced-mode":      "fake-ip",
	"default-nameserver": []any{"223.5.5.5", "119.29.29.29"},
	"nameserver":         []any{"https://223.5.5.5/dns-query", "https://1.12.12.12/dns-query"},
}

// connectivityChecks are the hosts the OSes probe to tell whether they are online (NetworkManager,
// Windows NCSI, macOS). The probe goes out the physical interface, past the TUN, so a fake IP for
// one reads as offline: GNOME puts a "?" on the network icon.
var connectivityChecks = []any{"connectivity-check.ubuntu.com", "nmcheck.gnome.org", "network-test.debian.org", "+.msftconnecttest.com", "+.msftncsi.com", "captive.apple.com"}

// Capture aligns capture.mode with enhanced mode. On: system-proxy (or none) becomes tun, an
// explicit process-tun is kept, and DNS is switched on for the hijack. Off: TUN modes fall back
// to system-proxy, since only the daemon can create a TUN.
func Capture(cfg map[string]any, enhanced bool) {
	capture, _ := cfg["capture"].(map[string]any)
	if capture == nil {
		capture = map[string]any{}
		cfg["capture"] = capture
	}
	mode, _ := capture["mode"].(string)
	if !enhanced {
		if mode == "tun" || mode == "process-tun" {
			capture["mode"] = "system-proxy"
		}
		return
	}
	if mode == "" || mode == "system-proxy" {
		capture["mode"] = "tun"
	}
	dns, _ := cfg["dns"].(map[string]any)
	if dns == nil || dns["enable"] != true {
		cfg["dns"] = merge(clone(tunDNS), dns)
		cfg["dns"].(map[string]any)["enable"] = true
	}
	dns = cfg["dns"].(map[string]any)
	if m, _ := dns["fake-ip-filter-mode"].(string); m == "" || m == "blacklist" { // whitelist and rule modes list what does get a fake IP
		filter, _ := dns["fake-ip-filter"].([]any)
		dns["fake-ip-filter"] = append(slices.Clone(filter), connectivityChecks...)
	}
	if runtime.GOOS == "linux" || runtime.GOOS == "windows" {
		upstream := upstreamDNS("/run/systemd/resolve/resolv.conf") // none on Windows: the plain resolvers stand in
		if len(upstream) == 0 {
			upstream, _ = dns["default-nameserver"].([]any)
		}
		if len(upstream) == 0 {
			upstream = tunDNS["default-nameserver"].([]any)
		}
		replaceSystemDNS(dns, upstream)
	}
}

// tunNet holds the TUN's addresses and its fake IPs (mihomo's default 198.18.0.1/16).
var _, tunNet, _ = net.ParseCIDR("198.18.0.0/15")

// upstreamDNS lists the resolvers systemd-resolved forwards to, from its resolv.conf: the network's
// own (the router's, which also answers .lan names), the TUN's and loopback left out.
func upstreamDNS(path string) []any {
	b, _ := os.ReadFile(path)
	var out []any
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] != "nameserver" {
			continue
		}
		if ip := net.ParseIP(f[1]); ip != nil && !ip.IsLoopback() && !tunNet.Contains(ip) { // a scoped fe80::…%2 doesn't parse: skipped
			out = append(out, f[1])
		}
	}
	return out
}

// replaceSystemDNS swaps a "system" nameserver, in the lists and in nameserver-policy, for upstream.
// On Linux, TUN's auto-route points systemd-resolved at the TUN's own DNS, so "system" answers the
// core's own lookups with fake IPs and every DIRECT connection it resolves itself goes nowhere.
// Windows' DNS client sends its queries into the TUN the same way.
// ponytail: on Windows the plain resolvers stand in, so .lan names the router alone knows don't
// resolve under TUN; read the physical adapter's DNS (Get-DnsClientServerAddress) if that matters.
func replaceSystemDNS(dns map[string]any, upstream []any) {
	isSystem := func(v any) bool { return v == "system" || v == "system://" }
	swap := func(v any) any {
		if isSystem(v) {
			return slices.Clone(upstream)
		}
		list, ok := v.([]any)
		if !ok || !slices.ContainsFunc(list, isSystem) {
			return v
		}
		var out []any
		for _, s := range list {
			if isSystem(s) {
				out = append(out, upstream...)
			} else {
				out = append(out, s)
			}
		}
		return out
	}
	for _, key := range []string{"nameserver", "direct-nameserver", "proxy-server-nameserver", "fallback"} {
		if v, ok := dns[key]; ok {
			dns[key] = swap(v)
		}
	}
	if policy, ok := dns["nameserver-policy"].(map[string]any); ok {
		for k, v := range policy {
			policy[k] = swap(v)
		}
	}
}

// Merge layers profile and override over the app defaults. Maps merge deeply, other values are
// replaced; prepend-/append-{rules,proxies,proxy-groups} in the override extend the profile's lists,
// group-members (name -> proxies) puts proxies at the front of existing groups, and
// remove-proxies / remove-proxy-groups drop the profile's items along with every reference to them.
func Merge(profile, override map[string]any) map[string]any {
	cfg := merge(clone(appDefaults), profile)
	for _, k := range controlKeys {
		delete(cfg, k)
	}
	extend := map[string]any{}
	for k, v := range override {
		if strings.HasPrefix(k, "prepend-") || strings.HasPrefix(k, "append-") {
			extend[k] = v
			delete(override, k)
		}
	}
	members, _ := override["group-members"].(map[string]any)
	delete(override, "group-members")
	patches, _ := override["patch-proxies"].(map[string]any) // name -> fields merged over that proxy
	delete(override, "patch-proxies")
	replaced, _ := override["replace-proxy-groups"].(map[string]any) // name -> the group in full
	delete(override, "replace-proxy-groups")
	hidden, _ := override["remove-rules"].([]any) // profile rules deleted from the GUI
	delete(override, "remove-rules")
	removed := map[string]bool{}
	for _, key := range []string{"remove-proxies", "remove-proxy-groups"} {
		names, _ := override[key].([]any)
		for _, n := range names {
			if name, ok := n.(string); ok {
				removed[name] = true
			}
		}
		delete(override, key)
	}
	cfg = merge(cfg, override)
	for _, key := range []string{"rules", "proxies", "proxy-groups"} {
		list, _ := cfg[key].([]any)
		// an override item replaces the profile's of the same name, which the core would refuse twice
		pre, _ := extend["prepend-"+key].([]any)
		post, _ := extend["append-"+key].([]any)
		ours := map[string]bool{}
		for _, x := range append(slices.Clone(pre), post...) {
			ours[groupName(x)] = true
			delete(removed, groupName(x)) // a GUI item takes the name of a removed profile item back
		}
		delete(ours, "") // rules have no name
		if len(ours) > 0 {
			list = slices.DeleteFunc(slices.Clone(list), func(x any) bool { return ours[groupName(x)] })
		}
		list = append(append(slices.Clone(pre), list...), post...)
		if len(list) > 0 {
			cfg[key] = list
		}
	}
	if len(hidden) > 0 {
		rules, _ := cfg["rules"].([]any)
		cfg["rules"] = slices.DeleteFunc(slices.Clone(rules), func(r any) bool { return slices.Contains(hidden, r) })
	}
	// group-members: the GUI's own key, prepends user proxies into existing groups
	groups, _ := cfg["proxy-groups"].([]any)
	for i, g := range groups {
		if group, ok := replaced[groupName(g)].(map[string]any); ok {
			groups[i] = group
		}
	}
	for _, g := range groups {
		group, _ := g.(map[string]any)
		name, _ := group["name"].(string)
		extra, _ := members[name].([]any)
		if len(extra) == 0 {
			continue
		}
		current, _ := group["proxies"].([]any)
		group["proxies"] = append(slices.Clone(extra), slices.DeleteFunc(slices.Clone(current), func(p any) bool { return slices.Contains(extra, p) })...)
	}
	proxies, _ := cfg["proxies"].([]any)
	for _, p := range proxies {
		proxy, _ := p.(map[string]any)
		name, _ := proxy["name"].(string)
		if patch, ok := patches[name].(map[string]any); ok {
			applyPatch(proxy, patch)
		}
	}
	dropRemoved(cfg, removed)
	dropDangling(cfg)
	return cfg
}

// dropRemoved deletes the named proxies and groups; dropDangling then drops what refers to them.
func dropRemoved(cfg map[string]any, removed map[string]bool) {
	for _, key := range []string{"proxies", "proxy-groups"} {
		if list, ok := cfg[key].([]any); ok {
			cfg[key] = slices.DeleteFunc(slices.Clone(list), func(x any) bool { return removed[groupName(x)] })
		}
	}
}

// builtins are the policies the core defines itself.
var builtins = []string{"DIRECT", "REJECT", "REJECT-DROP", "COMPATIBLE", "PASS", "PASS-RULE", "GLOBAL"}

// dropDangling takes out every reference to a proxy or group the config doesn't have: group members
// (a group emptied by it falls back to DIRECT), a subnet group's rules and default, dialer-proxy, and
// the rules targeting one. Removing a proxy or group leaves them behind, as does a subscription
// update dropping a node an edited group still lists, and the core refuses the config over any.
func dropDangling(cfg map[string]any) {
	proxies, _ := cfg["proxies"].([]any)
	groups, _ := cfg["proxy-groups"].([]any)
	items := append(slices.Clone(proxies), groups...)
	names := map[string]bool{}
	for _, n := range builtins {
		names[n] = true
	}
	for _, x := range items {
		names[groupName(x)] = true
	}
	gone := func(v any) bool { s, ok := v.(string); return ok && !names[s] }
	for _, x := range items {
		if m, _ := x.(map[string]any); gone(m["dialer-proxy"]) {
			delete(m, "dialer-proxy")
		}
	}
	for _, g := range groups {
		group, _ := g.(map[string]any)
		if rules, ok := group["rules"].(map[string]any); ok { // subnet group
			maps.DeleteFunc(rules, func(_ string, v any) bool { return gone(v) })
		}
		if gone(group["default"]) {
			group["default"] = "DIRECT"
		}
		list, ok := group["proxies"].([]any)
		if !ok {
			continue
		}
		kept := slices.DeleteFunc(slices.Clone(list), gone)
		if len(kept) == 0 && len(list) > 0 && group["use"] == nil {
			kept = []any{"DIRECT"}
		}
		group["proxies"] = kept
	}
	if rules, ok := cfg["rules"].([]any); ok {
		cfg["rules"] = slices.DeleteFunc(slices.Clone(rules), func(r any) bool {
			s, _ := r.(string)
			typ, _, _ := strings.Cut(s, ",")
			return !strings.EqualFold(strings.TrimSpace(typ), "SUB-RULE") && gone(RuleTarget(s)) // a sub-rule's target is a sub-rules name
		})
	}
}

// RuleTarget is the policy a rule sends traffic to: its last field, past options like no-resolve.
func RuleTarget(rule string) string {
	fields := strings.Split(rule, ",")
	for len(fields) > 1 {
		last := strings.TrimSpace(fields[len(fields)-1])
		if last != "no-resolve" && last != "src" {
			return last
		}
		fields = fields[:len(fields)-1]
	}
	return ""
}

// MergeYAML deep-merges the YAML mapping in text (may be empty) into dst, e.g. a form's
// "more settings" box over the fields it filled in.
func MergeYAML(dst map[string]any, text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	extra, err := parseMap([]byte(text))
	if err != nil {
		return fmt.Errorf("更多设置不是有效的 YAML 字典: %w", err)
	}
	merge(dst, extra)
	return nil
}

func merge(dst, src map[string]any) map[string]any {
	for k, v := range src {
		if sub, ok := v.(map[string]any); ok {
			if cur, ok := dst[k].(map[string]any); ok {
				dst[k] = merge(cur, sub)
				continue
			}
		}
		dst[k] = v
	}
	return dst
}

func clone(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if sub, ok := v.(map[string]any); ok {
			v = clone(sub)
		}
		out[k] = v
	}
	return out
}

// applyPatch merges patch into dst like merge, except that a null removes the key (and a map
// left empty by that goes too).
func applyPatch(dst, patch map[string]any) {
	for k, v := range patch {
		sub, ok := v.(map[string]any)
		switch {
		case v == nil:
			delete(dst, k)
		case ok:
			cur, isMap := dst[k].(map[string]any)
			if !isMap {
				cur = map[string]any{}
			}
			if applyPatch(cur, sub); len(cur) > 0 {
				dst[k] = cur
			} else {
				delete(dst, k)
			}
		default:
			dst[k] = v
		}
	}
}

// Value reads a dotted path from the merged config, e.g. "dns.nameserver". The merge is cached by
// the profile and override files' modification times: settings pages read a dozen values at once.
func (s *Store) Value(enhanced bool, path []string) (any, error) {
	s.mu.Lock()
	current := s.index.Current
	s.mu.Unlock()
	key := fmt.Sprintf("%s|%v|%s|%s", current, enhanced, mtime(s.path(current+".yaml")), mtime(s.path("override.yaml")))
	s.valueMu.Lock()
	defer s.valueMu.Unlock()
	cur := s.valueCache
	if key != s.valueKey {
		cfg, err := s.merged(enhanced)
		if err != nil {
			return nil, err
		}
		b, err := yaml.Marshal(cfg) // round-trip so values come back in the same shapes as the core's YAML
		if err != nil {
			return nil, err
		}
		cur = map[string]any{}
		if err := yaml.Unmarshal(b, &cur); err != nil {
			return nil, err
		}
		s.valueKey, s.valueCache = key, cur
	}
	for _, key := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, nil
		}
		cur = m[key]
	}
	return cur, nil
}

// mtime identifies a file's current content for caching: its size and modification time.
func mtime(path string) string {
	st, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d@%d", st.Size(), st.ModTime().UnixNano())
}

func groupName(g any) string {
	m, _ := g.(map[string]any)
	name, _ := m["name"].(string)
	return name
}
