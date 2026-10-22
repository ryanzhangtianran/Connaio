package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

// speedURL is downloaded for bandwidth tests (a var so tests can point it at a local server).
var speedURL = "https://speed.cloudflare.com/__down?bytes=100000000"

const speedDuration = 10 * time.Second

type Speed struct {
	Bytes   int64   `json:"bytes"`
	Seconds float64 `json:"seconds"`
	Mbps    float64 `json:"mbps"`
}

var speedMu sync.Mutex // one test at a time: they share the temporary rule's host

// viaProxy is an HTTP client whose requests to rawURL's host go through proxy (a node or group): a
// temporary rule sends the host to it through the mixed-port; done removes the rule. Needs rule
// mode (ruleModePort).
func (s *ToolService) viaProxy(proxy, rawURL string) (*http.Client, func(), error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, nil, err
	}
	host := u.Hostname()
	port, err := s.ruleModePort()
	if err != nil {
		return nil, nil, err
	}
	rule := "DOMAIN," + host + "," + proxy
	if ip := net.ParseIP(host); ip != nil {
		rule = "IP-CIDR," + host + "/32," + proxy + ",no-resolve"
	}
	id, err := s.cs.AddTempRule(rule)
	if err != nil {
		return nil, nil, err
	}
	return oneShotClient(port, 0), func() { _ = s.cs.RemoveTempRule(id) }, nil
}

// ruleModePort is the core's mixed-port, for a test whose traffic a temporary rule steers: an error
// outside rule mode, since global/direct mode skip rules.
func (s *ToolService) ruleModePort() (int, error) {
	if mode, err := s.cs.Mode(); err != nil {
		return 0, err
	} else if mode != "rule" {
		return 0, errors.New("需要「规则判定」模式，全局和直连模式不经过规则")
	}
	return mixedPort(s.cs.core)
}

// SpeedTest measures download bandwidth through proxy for up to 10s.
func (s *ToolService) SpeedTest(proxy string) (Speed, error) {
	speedMu.Lock()
	defer speedMu.Unlock()
	client, done, err := s.viaProxy(proxy, speedURL)
	if err != nil {
		return Speed{}, err
	}
	defer done()
	reqCtx, cancel := context.WithTimeout(context.Background(), speedDuration+5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, speedURL, nil)
	resp, err := client.Do(req)
	if err != nil {
		return Speed{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Speed{}, fmt.Errorf("测速服务器返回 %s", resp.Status)
	}

	start := time.Now() // from the first response byte: excludes connection setup
	buf := make([]byte, 64<<10)
	var total int64
	for time.Since(start) < speedDuration {
		n, err := resp.Body.Read(buf)
		total += int64(n)
		if err == io.EOF {
			break
		}
		if err != nil {
			if total == 0 {
				return Speed{}, err
			}
			break
		}
	}
	secs := time.Since(start).Seconds()
	return Speed{Bytes: total, Seconds: secs, Mbps: float64(total) * 8 / secs / 1e6}, nil
}

// uploadURL receives the upload test's body (a var so tests can point it at a local server).
var uploadURL = "https://speed.cloudflare.com/__up"

// UploadTest measures upload bandwidth through proxy: zeros are posted for up to 10s.
func (s *ToolService) UploadTest(proxy string) (Speed, error) {
	speedMu.Lock()
	defer speedMu.Unlock()
	client, done, err := s.viaProxy(proxy, uploadURL)
	if err != nil {
		return Speed{}, err
	}
	defer done()
	body := &timedZeros{until: time.Now().Add(speedDuration)}
	ctx, cancel := context.WithTimeout(context.Background(), speedDuration+10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, body)
	start := time.Now()
	resp, err := client.Do(req)
	if resp != nil {
		resp.Body.Close() // the transport may still be sending the body until the response is done with
	}
	n := body.n.Load()
	if err != nil && n == 0 {
		return Speed{}, err
	}
	secs := time.Since(start).Seconds()
	return Speed{Bytes: n, Seconds: secs, Mbps: float64(n) * 8 / secs / 1e6}, nil
}

// timedZeros reads zeros until a deadline, counting them.
type timedZeros struct {
	until time.Time
	n     atomic.Int64
}

func (z *timedZeros) Read(p []byte) (int, error) {
	if time.Now().After(z.until) {
		return 0, io.EOF
	}
	clear(p)
	z.n.Add(int64(len(p)))
	return len(p), nil
}

// UDPRelay times a DNS query sent over UDP through proxy.
func (s *ToolService) UDPRelay(proxy string) (string, error) {
	var r struct {
		Server string `json:"server"`
		Delay  int    `json:"delay"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.cs.core.Do(ctx, http.MethodGet, "/proxies/"+url.PathEscape(proxy)+"/udp", nil, &r); err != nil {
		return "", err
	}
	return fmt.Sprintf("通过 %s 向 %s 发送 DNS 查询：%d ms", proxy, r.Server, r.Delay), nil
}

// NATType reports how the proxy's UDP maps, from two STUN servers.
func (s *ToolService) NATType(proxy string) ([]string, error) {
	var r struct {
		Mapped []string `json:"mapped"`
		NAT    string   `json:"nat"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := s.cs.core.Do(ctx, http.MethodGet, "/proxies/"+url.PathEscape(proxy)+"/nat", nil, &r); err != nil {
		return nil, err
	}
	lines := []string{"NAT 类型：" + r.NAT}
	for _, m := range r.Mapped {
		lines = append(lines, "外部地址："+m)
	}
	return lines, nil
}

// TCPRelay times an HTTP request through proxy on an established connection (as the latency test).
func (s *ToolService) TCPRelay(proxy string) (string, error) {
	ms, err := s.cs.testProxy(context.Background(), proxy)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("通过 %s 请求 %s：%d ms", proxy, testURL, ms), nil
}
