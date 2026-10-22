package app

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func init() {
	application.RegisterEvent[DiagSection]("diagnose")
}

// ToolService runs diagnostics and tests through the core (network diagnosis, DNS leak, speed,
// UDP and NAT checks); it keeps no core state of its own.
type ToolService struct {
	cs       *CoreService
	mu       sync.Mutex
	stopDiag context.CancelFunc // cancels a running Diagnose
}

// DiagSection is one step of the network diagnosis, sent as it finishes.
type DiagSection struct {
	Title string   `json:"title"`
	Lines []string `json:"lines"`
	OK    bool     `json:"ok"`
}

const directTestURL = "http://www.apple.com/library/test/success.html"

// Diagnose checks, step by step as Surge does, the config, this Mac's network, the router, DNS,
// a direct connection and the proxies in use, emitting each section as it is done. CancelDiagnose
// stops it.
func (s *ToolService) Diagnose() error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	s.mu.Lock()
	if s.stopDiag != nil {
		s.stopDiag()
	}
	s.stopDiag = cancel
	s.mu.Unlock()
	defer cancel()

	steps := []func(context.Context) DiagSection{s.diagConfig, diagNetwork, diagRouter, diagDNS, diagDirect, s.diagProxies}
	for _, step := range steps {
		if ctx.Err() != nil {
			return nil
		}
		emit("diagnose", step(ctx))
	}
	return nil
}

func (s *ToolService) CancelDiagnose() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopDiag != nil {
		s.stopDiag()
	}
}

func (s *ToolService) diagConfig(ctx context.Context) DiagSection {
	if err := s.cs.core.Validate(ctx, s.cs.core.ConfigPath()); err != nil {
		return DiagSection{Title: "检查配置", Lines: []string{err.Error()}}
	}
	return DiagSection{Title: "检查配置", Lines: []string{"配置检查成功"}, OK: true}
}

func diagNetwork(context.Context) DiagSection {
	route := defaultRoute()
	v6 := routeOf("-inet6")
	lines := []string{
		"首选网络接口（IPv4）：" + cmp.Or(route["interface"], "无"),
		"首选 IP 地址（IPv4）：" + cmp.Or(ifaceAddr(route["interface"], false), "无"),
		"默认路由（IPv4）：" + cmp.Or(route["gateway"], "无"),
		"首选网络接口（IPv6）：" + cmp.Or(v6["interface"], "不适用"),
		"首选 IP 地址（IPv6）：" + cmp.Or(ifaceAddr(v6["interface"], true), "不适用"),
		"DNS 服务器：" + cmp.Or(strings.Join(dnsServers(), "、"), "无"),
	}
	return DiagSection{Title: "网络配置", Lines: lines, OK: route["interface"] != ""}
}

func diagRouter(ctx context.Context) DiagSection {
	gw := defaultRoute()["gateway"]
	if gw == "" {
		return DiagSection{Title: "路由器", Lines: []string{"没有默认路由"}}
	}
	if ms := pingMs(ctx, gw); ms > 0 {
		return DiagSection{Title: "路由器", Lines: []string{fmt.Sprintf("Ping %s：%d ms", gw, ms)}, OK: true}
	}
	return DiagSection{Title: "路由器", Lines: []string{fmt.Sprintf("Ping %s：无响应", gw)}}
}

func diagDNS(ctx context.Context) DiagSection {
	servers := dnsServers()
	if len(servers) == 0 {
		return DiagSection{Title: "测试 DNS", Lines: []string{"没有 DNS 服务器"}}
	}
	addr := net.JoinHostPort(servers[0], "53")
	r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	start := time.Now()
	if _, err := r.LookupHost(ctx, "www.apple.com"); err != nil {
		return DiagSection{Title: "测试 DNS", Lines: []string{fmt.Sprintf("%s 没有应答：%v", addr, err)}}
	}
	return DiagSection{Title: "测试 DNS", Lines: []string{fmt.Sprintf("收到 %s 的应答：%d ms", addr, ms(start))}, OK: true}
}

func diagDirect(ctx context.Context) DiagSection {
	client := oneShotClient(0, 5*time.Second)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, directTestURL, nil)
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return DiagSection{Title: "测试直连策略", Lines: []string{"直接连接失败：" + err.Error()}}
	}
	resp.Body.Close()
	return DiagSection{Title: "测试直连策略", Lines: []string{fmt.Sprintf("直接连接到 %s：%d ms", directTestURL, ms(start))}, OK: true}
}

// diagProxies tests the node each selectable group currently uses, once per node.
func (s *ToolService) diagProxies(ctx context.Context) DiagSection {
	groups, err := s.cs.Groups()
	if err != nil {
		return DiagSection{Title: "测试代理策略", Lines: []string{err.Error()}}
	}
	sec := DiagSection{Title: "测试代理策略", OK: true}
	seen := map[string]bool{"DIRECT": true, "REJECT": true, "": true}
	for _, g := range groups {
		if g.Name == "GLOBAL" || seen[g.Now] {
			continue
		}
		seen[g.Now] = true
		delay, err := s.cs.testProxy(ctx, g.Now)
		if err != nil {
			sec.Lines = append(sec.Lines, fmt.Sprintf("通过 %s 连接失败", g.Now))
			sec.OK = false
			continue
		}
		sec.Lines = append(sec.Lines, fmt.Sprintf("通过 %s 连接到 %s：%d ms", g.Now, testURL, delay))
	}
	if len(sec.Lines) == 0 {
		sec.Lines = []string{"没有在用的代理"}
	}
	return sec
}

// ifaceAddr is the interface's first IPv4 (or global IPv6) address.
func ifaceAddr(name string, v6 bool) string {
	ifc, err := net.InterfaceByName(name)
	if err != nil {
		return ""
	}
	addrs, _ := ifc.Addrs()
	for _, a := range addrs {
		ip, _, err := net.ParseCIDR(a.String())
		if err == nil && (ip.To4() == nil) == v6 && ip.IsGlobalUnicast() {
			return ip.String()
		}
	}
	return ""
}

// LeakServer is an address bash.ws saw: a DNS server that queried a test domain, or the address a
// test came from.
type LeakServer struct {
	IP      string `json:"ip"`
	Country string `json:"country"`
	ASN     string `json:"asn"` // "AS13335 CloudFlare Inc": the network and whose it is
}

// DNSLeak is a DNS leak test, done as bash.ws's own client (github.com/macvk/dnsleaktest) does:
// fresh subdomains of one test id are fetched, and bash.ws lists the DNS servers that asked for them.
// The proxied side goes through the mixed port with no rule of its own, so the profile's rules
// decide as they do for any app, including those that make the core resolve a domain locally
// before it is proxied; the direct side is the baseline, this network's own resolvers.
type DNSLeak struct {
	Policy   string       `json:"policy"`   // where the rules sent the test domain, "PROXY → GIA"; "" if unseen
	Exit     LeakServer   `json:"exit"`     // the proxied side's exit
	Proxied  []LeakServer `json:"proxied"`  // the DNS servers that resolved it
	DirectIP LeakServer   `json:"directIP"` // this network's own address
	Direct   []LeakServer `json:"direct"`   // and its resolvers
	Leaked   []LeakServer `json:"leaked"`   // the proxied side's servers in this network's networks or country
}

const leakProbes = 10 // subdomains per side: enough to draw out every resolver of a pool

var leakMu sync.Mutex // one test at a time: the direct side holds a temporary rule

func (s *ToolService) DNSLeakTest() (DNSLeak, error) {
	leakMu.Lock()
	defer leakMu.Unlock()
	port, err := s.ruleModePort()
	if err != nil {
		return DNSLeak{}, err
	}
	client := oneShotClient(port, 10*time.Second)
	var r DNSLeak

	id, err := leakID(client)
	if err != nil {
		return r, err
	}
	stop := make(chan struct{})
	seen := make(chan string, 1)
	go func() { // the test's connections last a moment: watch for one to learn its route
		for {
			conns, _ := s.cs.fetchConnections()
			for _, c := range conns {
				if strings.HasSuffix(c.Host, "."+id+".bash.ws") {
					seen <- c.Chain
					return
				}
			}
			select {
			case <-stop:
				seen <- ""
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
	}()
	probe(client, id)
	close(stop)
	r.Policy = <-seen
	if strings.HasSuffix(r.Policy, "DIRECT") { // or a group whose current node is DIRECT
		return r, errors.New("测试域名按规则直连，没有经过代理，无法检测")
	}
	ips, dns, err := leakResults(client, id)
	if err != nil {
		return r, err
	}
	r.Proxied = dns
	if len(ips) > 0 {
		r.Exit = ips[0]
	}

	// the baseline: a second id with all of bash.ws sent DIRECT by a temporary rule, so its lookups
	// are this network's, and so is the address that fetches the results, which bash.ws reports
	rule, err := s.cs.AddTempRule("DOMAIN-SUFFIX,bash.ws,DIRECT")
	if err != nil {
		return r, err
	}
	defer func() { _ = s.cs.RemoveTempRule(rule) }()
	if id, err = leakID(client); err != nil {
		return r, err
	}
	probe(client, id)
	if ips, r.Direct, err = leakResults(client, id); err != nil {
		return r, err
	}
	if len(ips) > 0 {
		r.DirectIP = ips[0]
	}
	r.Leaked = leakedServers(r.Proxied, r.Direct, r.DirectIP, r.Exit)
	return r, nil
}

// leakedServers are the proxied side's DNS servers that belong to this network: one of the direct
// side's networks (its ISP's, its resolvers'), or its country when the proxy exits elsewhere.
// Resolvers abroad that aren't the exit's own are no leak: a proxy's always are someone else's.
func leakedServers(proxied, direct []LeakServer, directIP, exit LeakServer) []LeakServer {
	asn := func(s LeakServer) string { n, _, _ := strings.Cut(s.ASN, " "); return n }
	local := map[string]bool{asn(directIP): true}
	for _, d := range direct {
		local[asn(d)] = true
	}
	delete(local, "")
	var out []LeakServer
	for _, p := range proxied {
		if local[asn(p)] || (directIP.Country != "" && p.Country == directIP.Country && exit.Country != directIP.Country) {
			out = append(out, p)
		}
	}
	return out
}

// leakID is a fresh test id from bash.ws.
func leakID(c *http.Client) (string, error) {
	resp, err := c.Get("https://bash.ws/id")
	if err != nil {
		return "", fmt.Errorf("bash.ws: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
	id := strings.TrimSpace(string(b))
	if id == "" || strings.ContainsAny(id, "/.: ") {
		return "", fmt.Errorf("bash.ws: no test id (%q)", id)
	}
	return id, nil
}

// probe fetches every test subdomain of id at once; the answers don't matter, only the lookups.
func probe(c *http.Client, id string) {
	var wg sync.WaitGroup
	for i := 1; i <= leakProbes; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if resp, err := c.Get(fmt.Sprintf("http://%d.%s.bash.ws/", i, id)); err == nil {
				resp.Body.Close()
			}
		}()
	}
	wg.Wait()
}

// leakResults reads what bash.ws saw for id: the addresses the tests came from, and the DNS servers.
func leakResults(c *http.Client, id string) (ips, dns []LeakServer, err error) {
	resp, err := c.Get("https://bash.ws/dnsleak/test/" + id + "?json=2")
	if err != nil {
		return nil, nil, fmt.Errorf("bash.ws: %w", err)
	}
	defer resp.Body.Close()
	var rows []struct {
		Type, IP, Country, ASN, Text string
		CountryName                  string `json:"country_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		return nil, nil, fmt.Errorf("bash.ws: %w", err)
	}
	for _, r := range rows {
		s := LeakServer{IP: r.IP, Country: cmp.Or(r.CountryName, r.Country), ASN: r.ASN}
		switch r.Type {
		case "ip":
			ips = append(ips, s)
		case "dns":
			dns = append(dns, s)
		}
	}
	if len(dns) == 0 {
		return ips, nil, errors.New("bash.ws 没有收到任何 DNS 查询，请重试")
	}
	return ips, dns, nil
}

// IPInfo is where traffic through a policy comes out: the address the internet sees, and where it is.
type IPInfo struct {
	IP      string `json:"ip"`
	Code    string `json:"code"` // ISO country code, for its flag
	Country string `json:"country"`
	Region  string `json:"region"`
	City    string `json:"city"`
	ISP     string `json:"isp"`
}

var exitMu sync.Mutex // one lookup at a time: they share the temporary rule's host

// exitAPI answers with the asking address and its location.
const exitAPI = "http://ip-api.com/json/?fields=status,message,query,country,countryCode,regionName,city,isp"

// ExitIP looks up, through policy (a node, a group, or DIRECT), the address the internet sees and
// where it is. It asks twice before giving up: the first connection through a node just after the
// core starts can stall with no answer.
// ponytail: one third-party service, over plain HTTP as its free tier is; add a fallback if it's flaky.
func (s *ToolService) ExitIP(policy string) (IPInfo, error) {
	exitMu.Lock()
	defer exitMu.Unlock()
	c, done, err := s.viaProxy(policy, exitAPI)
	if err != nil {
		return IPInfo{}, err
	}
	defer done()
	c.Timeout = 6 * time.Second
	return lookupIP(c)
}

// lookupIP asks exitAPI through c where its requests come out, twice before giving up.
func lookupIP(c *http.Client) (IPInfo, error) {
	resp, err := c.Get(exitAPI)
	if err != nil {
		resp, err = c.Get(exitAPI)
	}
	if err != nil {
		return IPInfo{}, err
	}
	defer resp.Body.Close()
	var v struct{ Status, Message, Query, Country, CountryCode, RegionName, City, ISP string } // matched to its fields case-insensitively
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return IPInfo{}, err
	}
	if v.Status != "success" {
		return IPInfo{}, fmt.Errorf("ip-api.com: %s", cmp.Or(v.Message, v.Status))
	}
	return IPInfo{IP: v.Query, Code: v.CountryCode, Country: v.Country, Region: v.RegionName, City: v.City, ISP: v.ISP}, nil
}
