package app

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ryanzhangtianran/Connaio/internal/core"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type Rule struct {
	Index    int    `json:"index"`
	Type     string `json:"type"`
	Payload  string `json:"payload"`
	Proxy    string `json:"proxy"`
	Disabled bool   `json:"disabled"`
	Hits     uint64 `json:"hits"`
}

type Forward struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Remote string `json:"remote"`
	Local  string `json:"local"`
	State  string `json:"state"`
	Error  string `json:"error"`
}

type Tunnel struct {
	Name     string    `json:"name"`
	Type     string    `json:"type"`
	State    string    `json:"state"`
	Error    string    `json:"error"`
	Since    string    `json:"since"`
	Forwards []Forward `json:"forwards"`
}

// EasyConnectTunnel is one EasyConnect (campus VPN) outbound's state, from the core.
type EasyConnectTunnel struct {
	Name    string              `json:"name"`
	State   string              `json:"state"` // up, connecting, down
	Error   string              `json:"error"`
	Kicked  bool                `json:"kicked"` // paused: the gateway kept ending the session, the account probably signed in elsewhere
	Address string              `json:"address"`
	Routing *EasyConnectRouting `json:"routing"`

	// Kicks is how often the gateway ended the session in the last 10 minutes, the last at LastKick.
	Kicks    int       `json:"kicks"`
	LastKick time.Time `json:"lastKick"`
}

// EasyConnectRouting is what the gateway routes: traffic outside it is dropped by the gateway.
type EasyConnectRouting struct {
	IP      []EasyConnectRange `json:"ip"`
	Domains []string           `json:"domains"`
	DNS     []string           `json:"dns"`
}

type EasyConnectRange struct {
	From     string `json:"from"`
	To       string `json:"to"`
	PortMin  int    `json:"portMin"`
	PortMax  int    `json:"portMax"`
	Protocol string `json:"protocol"`
}

type LogLine struct {
	Level   string `json:"type"`
	Payload string `json:"payload"`
	Time    string `json:"time"` // when the GUI received it, HH:MM:SS; the core's stream carries none
}

func init() {
	application.RegisterEvent[LogLine]("log")
}

func (s *CoreService) Rules() ([]Rule, error) {
	var resp struct {
		Rules []struct {
			Index   int    `json:"index"`
			Type    string `json:"type"`
			Payload string `json:"payload"`
			Proxy   string `json:"proxy"`
			Extra   *struct {
				Disabled bool   `json:"disabled"`
				HitCount uint64 `json:"hitCount"`
			} `json:"extra"`
		} `json:"rules"`
	}
	if err := s.core.Do(context.Background(), http.MethodGet, "/rules", nil, &resp); err != nil {
		return nil, err
	}
	rules := make([]Rule, 0, len(resp.Rules))
	for _, r := range resp.Rules {
		rule := Rule{Index: r.Index, Type: r.Type, Payload: r.Payload, Proxy: r.Proxy}
		if r.Extra != nil {
			rule.Disabled, rule.Hits = r.Extra.Disabled, r.Extra.HitCount
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

// SetRuleDisabled switches one configured rule off or on until the next reload.
func (s *CoreService) SetRuleDisabled(index int, disabled bool) error {
	return s.core.Do(context.Background(), http.MethodPatch, "/rules/disable", map[string]bool{strconv.Itoa(index): disabled}, nil)
}

func (s *CoreService) ReverseTunnels() ([]Tunnel, error) {
	var resp struct {
		Tunnels []Tunnel `json:"tunnels"`
	}
	err := s.core.Do(context.Background(), http.MethodGet, "/reverse-tunnels", nil, &resp)
	return resp.Tunnels, err
}

func (s *CoreService) EasyConnects() ([]EasyConnectTunnel, error) { return easyConnects(s.core) }

// TailscaleStatus is one Tailscale outbound's login state, from the core: State is tailscale's
// (NeedsLogin with AuthURL, Running with IPs, …), "" with Error.
type TailscaleStatus struct {
	Name    string           `json:"name"`
	State   string           `json:"state"`
	AuthURL string           `json:"authURL"`
	IPs     []string         `json:"ips"`
	Routes  []TailscaleRoute `json:"routes"`  // the subnets other nodes advertise
	Latency int              `json:"latency"` // a Tailscale ping's round trip to the exit node or a node, ms; 0: none answered
	Error   string           `json:"error"`
}

// TailscaleRoute is a subnet a tailnet node advertises, and the node.
type TailscaleRoute struct {
	Prefix string `json:"prefix"`
	Node   string `json:"node"`
	Online bool   `json:"online"`
}

// tailscales reads every Tailscale outbound's state; asking starts one nothing has used yet, which
// is what brings its login link.
func tailscales(c *core.Core) ([]TailscaleStatus, error) {
	var resp struct {
		Tunnels []TailscaleStatus `json:"tunnels"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second) // a first start waits for the control server
	defer cancel()
	err := c.Do(ctx, http.MethodGet, "/tailscale", nil, &resp)
	for _, t := range resp.Tunnels {
		tailscaleLatency.Store(t.Name, t.Latency)
	}
	return resp.Tunnels, err
}

// tailscaleLatency is each Tailscale node's last measured latency (name -> ms), what the groups
// show for it: a URL test can't reach past the tailnet without an exit node.
var tailscaleLatency sync.Map

// measureTailscale pings through the named Tailscale node (tailscales) and returns its latency.
func measureTailscale(c *core.Core, name string) (int, error) {
	if _, err := tailscales(c); err != nil {
		return 0, err
	}
	if v, _ := tailscaleLatency.Load(name); v != nil && v.(int) > 0 {
		return v.(int), nil
	}
	return 0, errors.New("tailnet 中没有节点应答")
}

func easyConnects(c *core.Core) ([]EasyConnectTunnel, error) {
	var resp struct {
		Tunnels []EasyConnectTunnel `json:"tunnels"`
	}
	err := c.Do(context.Background(), http.MethodGet, "/easyconnect", nil, &resp)
	return resp.Tunnels, err
}

// streamLogs forwards core log lines (info and above) to the frontend, reconnecting on errors.
func (s *CoreService) streamLogs(ctx context.Context) {
	s.retryStream(ctx, "log stream", "/logs?level=info", func(line []byte) {
		var l LogLine
		if json.Unmarshal(line, &l) == nil {
			l.Time = time.Now().Format("15:04:05")
			keepLog(l)
			emit("log", l)
			if (l.Level == "warning" || l.Level == "error") && strings.Contains(l.Payload, "[EasyConnect]") {
				if _, after, ok := strings.Cut(l.Payload, ")"); ok {
					notify("warning", "VPN：%s", strings.TrimSpace(after))
				}
			}
		}
	}, nil, nil)
}

// retryStream keeps a core stream open, reconnecting with backoff (1s doubling to 30s). It logs
// once when the stream breaks and once when it is back; onBroken and onBack run then, when given.
func (s *CoreService) retryStream(ctx context.Context, name, path string, onLine func([]byte), onBroken func(error), onBack func()) {
	delay, broken := time.Second, false
	for ctx.Err() == nil {
		err := s.core.Stream(ctx, path, func(line []byte) {
			if broken {
				broken, delay = false, time.Second
				log.Printf("%s: back", name)
				if onBack != nil {
					onBack()
				}
			}
			onLine(line)
		})
		if ctx.Err() != nil {
			return
		}
		if !broken {
			broken = true
			log.Printf("%s: %v, retrying", name, err)
		}
		if onBroken != nil {
			onBroken(err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(delay):
		}
		delay = min(delay*2, 30*time.Second)
	}
}
