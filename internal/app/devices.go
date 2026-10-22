package app

import (
	"context"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// Device is another machine on the LAN using this Mac's proxy port (allow-lan).
type Device struct {
	IP          string `json:"ip"`
	Name        string `json:"name"`
	MAC         string `json:"mac"`
	Connections int    `json:"connections"`
	Upload      int64  `json:"upload"`
	Download    int64  `json:"download"`
	Policy      string `json:"policy"`    // set by a SRC-IP-CIDR rule, "" = follows the rules
	Temporary   bool   `json:"temporary"` // policy comes from a temporary rule
	RuleID      int    `json:"ruleId"`    // temporary rule id
	Rule        string `json:"rule"`      // permanent rule line
}

type LanInfo struct {
	AllowLan  bool     `json:"allowLan"`
	Addresses []string `json:"addresses"` // this Mac's LAN IPv4 addresses
	Port      int      `json:"port"`      // mixed-port: HTTP and SOCKS5
}

// DeviceService lists LAN devices and pins them to policies.
type DeviceService struct {
	core     *CoreService
	profiles *ProfileService

	mu    sync.Mutex
	names map[string]dnsName // reverse DNS cache
}

// dnsName is a cached reverse lookup; an empty answer is asked again after a while.
type dnsName struct {
	name string
	at   time.Time
}

const (
	lookupTimeout  = 500 * time.Millisecond
	emptyNameTTL   = 10 * time.Minute
	parallelLookup = 8
)

func (s *DeviceService) Lan() (LanInfo, error) {
	var cfg struct {
		AllowLan  bool `json:"allow-lan"`
		MixedPort int  `json:"mixed-port"`
	}
	if err := s.core.core.Do(context.Background(), http.MethodGet, "/configs", nil, &cfg); err != nil {
		return LanInfo{}, err
	}
	return LanInfo{AllowLan: cfg.AllowLan, Addresses: lanAddresses(), Port: cfg.MixedPort}, nil
}

// SetAllowLan lets other devices use the proxy port; kept in the override.
func (s *DeviceService) SetAllowLan(on bool) error {
	return s.profiles.Set("allow-lan", on)
}

func lanAddresses() []string {
	var out []string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && n.IP.IsPrivate() {
			out = append(out, n.IP.String())
		}
	}
	return out
}

// tunRange holds the TUN's own address and its fake IPs (mihomo's 198.18.0.1/16).
var _, tunRange, _ = net.ParseCIDR("198.18.0.0/15")

// fromHere says whether a source address is this machine's: loopback (the proxy port), the TUN's
// (what it captured), or one of its interfaces'. A connection from here that has no process is one
// the core couldn't name (a process gone before it asked), not a LAN device.
func fromHere(source string) bool {
	ip := net.ParseIP(source)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || tunRange.Contains(ip) {
		return true
	}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.Equal(ip) {
			return true
		}
	}
	return false
}

// Devices groups live connections by source IP, leaving out this machine's own processes and addresses.
func (s *DeviceService) Devices() ([]Device, error) {
	conns, err := s.core.Connections()
	if err != nil {
		return nil, err
	}
	byIP := map[string]*Device{}
	for _, c := range conns {
		if c.Process != "" || c.Tunnel != nil || net.ParseIP(c.Source) == nil || fromHere(c.Source) { // a tunnel's visitor is at its server, not on the LAN
			continue
		}
		d := byIP[c.Source]
		if d == nil {
			d = &Device{IP: c.Source}
			byIP[c.Source] = d
		}
		d.Connections++
		d.Upload += c.Upload
		d.Download += c.Download
	}
	if err := s.attachPolicies(byIP); err != nil {
		return nil, err
	}
	macs := arpTable()
	s.resolve(byIP)
	devices := make([]Device, 0, len(byIP))
	for _, d := range byIP {
		d.MAC = macs[d.IP]
		d.Name = s.name(d.IP)
		devices = append(devices, *d)
	}
	slices.SortFunc(devices, func(a, b Device) int { return int((b.Upload + b.Download) - (a.Upload + a.Download)) })
	return devices, nil
}

// attachPolicies fills each device's policy from SRC-IP-CIDR <ip>/32 rules; a pinned device with
// no live connection is still listed so its policy can be removed.
func (s *DeviceService) attachPolicies(byIP map[string]*Device) error {
	device := func(ip string) *Device {
		if byIP[ip] == nil {
			byIP[ip] = &Device{IP: ip}
		}
		return byIP[ip]
	}
	items, err := s.profiles.UserItems()
	if err != nil {
		return err
	}
	for _, rule := range items.Rules {
		parts := strings.Split(rule, ",")
		if len(parts) >= 3 && parts[0] == "SRC-IP-CIDR" && strings.HasSuffix(parts[1], "/32") {
			d := device(strings.TrimSuffix(parts[1], "/32"))
			d.Policy, d.Rule = parts[2], rule
		}
	}
	temp, err := s.core.TempRules()
	if err != nil {
		return err
	}
	for _, r := range temp { // temporary rules match first, so they win
		if r.Type == "SrcIPCIDR" && strings.HasSuffix(r.Payload, "/32") {
			d := device(strings.TrimSuffix(r.Payload, "/32"))
			d.Policy, d.Temporary, d.RuleID = r.Proxy, true, r.ID
		}
	}
	return nil
}

// SetPolicy pins ip to policy with a temporary or permanent SRC-IP-CIDR rule, replacing an earlier pin.
func (s *DeviceService) SetPolicy(ip, policy string, permanent bool) error {
	if net.ParseIP(ip) == nil {
		return &net.ParseError{Type: "IP address", Text: ip}
	}
	if err := s.ClearPolicy(ip); err != nil {
		return err
	}
	rule := "SRC-IP-CIDR," + ip + "/32," + policy
	if permanent {
		return s.profiles.AddRule(rule)
	}
	_, err := s.core.AddTempRule(rule)
	if err == nil {
		_ = s.core.CloseSource(ip)
	}
	return err
}

// ClearPolicy removes ip's pins, so it follows the rules again.
func (s *DeviceService) ClearPolicy(ip string) error {
	byIP := map[string]*Device{}
	if err := s.attachPolicies(byIP); err != nil {
		return err
	}
	d := byIP[ip]
	if d == nil {
		return nil
	}
	if d.Temporary {
		if err := s.core.RemoveTempRule(d.RuleID); err != nil {
			return err
		}
	}
	if d.Rule != "" {
		return s.profiles.RemoveRule(d.Rule)
	}
	return nil
}

// resolve looks up the devices whose reverse DNS name (often set by the router from DHCP) isn't
// cached yet, a few at a time rather than one after another: each can take its whole timeout.
func (s *DeviceService) resolve(byIP map[string]*Device) {
	var todo []string
	s.mu.Lock()
	for ip := range byIP {
		if n, ok := s.names[ip]; !ok || n.name == "" && time.Since(n.at) > emptyNameTTL {
			todo = append(todo, ip)
		}
	}
	s.mu.Unlock()
	var wg sync.WaitGroup
	sem := make(chan struct{}, parallelLookup)
	for _, ip := range todo {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(context.Background(), lookupTimeout)
			defer cancel()
			var name string
			if names, err := net.DefaultResolver.LookupAddr(ctx, ip); err == nil && len(names) > 0 {
				name = strings.TrimSuffix(names[0], ".")
			}
			s.mu.Lock()
			if s.names == nil {
				s.names = map[string]dnsName{}
			}
			s.names[ip] = dnsName{name, time.Now()}
			s.mu.Unlock()
		})
	}
	wg.Wait()
}

func (s *DeviceService) name(ip string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.names[ip].name
}
