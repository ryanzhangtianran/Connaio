package app

import (
	"cmp"
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ryanzhangtianran/Connaio/internal/daemon"
)

type Conn struct {
	ID       string `json:"id"`
	Network  string `json:"network"`
	Inbound  string `json:"inbound"` // TUN, HTTP, FRP, ...
	Process  string `json:"process"`
	Path     string `json:"path"`
	App      string `json:"app"`            // the application it counts towards (clientOf): WeChat for WeChat Helper, macOS for apsd
	Icon     string `json:"icon,omitempty"` // and the file whose icon stands for it
	System   bool   `json:"-"`              // a system service's: NetworkManager, livepatch on Linux; svchost on Windows
	Source   string `json:"source"`
	SrcPort  string `json:"-"`    // for finding this process's own test connection
	Host     string `json:"host"` // domain, or destination IP when there is none
	Port     string `json:"port"`
	Rule     string `json:"rule"` // e.g. "DomainSuffix(google.com)"
	Chain    string `json:"chain"`
	Upload   int64  `json:"upload"`
	Download int64  `json:"download"`
	Start    string `json:"start"`

	// the process's ancestors' executables, nearest first, as the core read them when the
	// connection opened; none from an older core, or on Windows, where the core can't read them
	Ancestors []string `json:"-"`
	// InName is the inbound's name: for a connection a reverse tunnel brought in (Inbound SSH or
	// FRP), the tunnel's
	InName string `json:"inName,omitempty"`
}

type TempRule struct {
	ID      int    `json:"id"`
	Type    string `json:"type"`
	Payload string `json:"payload"`
	Proxy   string `json:"proxy"`
	Hits    uint64 `json:"hits"`
}

// winDir is Windows' own directory, lowercased with a trailing separator; "" elsewhere.
var winDir = func() string {
	if d := os.Getenv("SystemRoot"); runtime.GOOS == "windows" && d != "" {
		return strings.ToLower(strings.TrimRight(d, `\`)) + `\`
	}
	return ""
}()

// Connections lists the live connections Connaio manages, newest first: not those partial
// enhancement's capture filter passes straight through, which go through the core only because
// TUN takes everything.
func (s *CoreService) Connections() ([]Conn, error) { return s.connections(false) }

// connections lists live connections, those outside partial enhancement's scope too when all is set.
// The ledger fetches the list every second; everyone else (the activity, connections, devices and
// processes pages) gets that snapshot while it is fresh, so the core serialises it once a second,
// not four times.
func (s *CoreService) connections(all bool) ([]Conn, error) {
	s.connMu.Lock()
	conns, at := s.conns, s.connAt
	s.connMu.Unlock()
	if conns == nil || time.Since(at) > 1500*time.Millisecond {
		var err error
		if conns, err = s.refreshConnections(); err != nil {
			return nil, err
		}
	}
	if all {
		return conns, nil
	}
	out := make([]Conn, 0, len(conns))
	for _, c := range conns {
		if c.Rule != "CaptureFilter" { // constant.CaptureFilter's name in the core
			out = append(out, c)
		}
	}
	return out, nil
}

// refreshConnections fetches the snapshot from the core and keeps it for connections. Callers
// treat the slice as read-only.
func (s *CoreService) refreshConnections() ([]Conn, error) {
	conns, err := s.fetchConnections()
	if err != nil {
		return nil, err
	}
	s.connMu.Lock()
	s.conns, s.connAt = conns, time.Now()
	s.connMu.Unlock()
	return conns, nil
}

func (s *CoreService) fetchConnections() ([]Conn, error) {
	var snap struct {
		Connections []struct {
			ID          string    `json:"id"`
			Upload      int64     `json:"upload"`
			Download    int64     `json:"download"`
			Start       time.Time `json:"start"`
			Chains      []string  `json:"chains"`
			Rule        string    `json:"rule"`
			RulePayload string    `json:"rulePayload"`
			Metadata    struct {
				Network     string  `json:"network"`
				Type        string  `json:"type"`
				SourceIP    string  `json:"sourceIP"`
				SourcePort  string  `json:"sourcePort"`
				Host        string  `json:"host"`
				DestIP      string  `json:"destinationIP"`
				DestPort    string  `json:"destinationPort"`
				Process     string  `json:"process"`
				ProcessPath string  `json:"processPath"`
				UID         *uint32 `json:"uid"`

				Ancestors []string `json:"processAncestors"`
				InName    string   `json:"inboundName"`
			} `json:"metadata"`
		} `json:"connections"`
	}
	if err := s.core.Do(context.Background(), http.MethodGet, "/connections", nil, &snap); err != nil {
		return nil, err
	}
	conns := make([]Conn, 0, len(snap.Connections))
	for _, c := range snap.Connections {
		m := c.Metadata
		process := m.Process
		if process == "" && m.ProcessPath != "" {
			process = filepath.Base(m.ProcessPath)
		}
		// a system service's: on Linux one run below UID_MIN (1000), by root or a service's own account
		// (a connection with no local process, a LAN device's or the core's own, carries uid 0 too);
		// on Windows one from the Windows directory (svchost.exe and its like)
		system := m.ProcessPath != "" && (runtime.GOOS == "linux" && m.UID != nil && *m.UID < 1000 ||
			runtime.GOOS == "windows" && winDir != "" && strings.HasPrefix(strings.ToLower(m.ProcessPath), winDir))
		if m.ProcessPath != "" && m.ProcessPath == ownExe {
			process = "Connaio" // not the executable's lowercase name, so it lists with the core's own connections
		}
		rule := c.Rule
		if c.RulePayload != "" {
			rule += "(" + c.RulePayload + ")"
		}
		chain := slices.Clone(c.Chains)
		slices.Reverse(chain) // the core lists the exit first; show group → node
		conn := Conn{
			ID: c.ID, Network: m.Network, Inbound: m.Type, Process: process, Path: m.ProcessPath, System: system, Source: m.SourceIP, SrcPort: m.SourcePort, Ancestors: m.Ancestors, InName: m.InName,
			Host: cmp.Or(m.Host, m.DestIP), Port: m.DestPort, Rule: rule, Chain: strings.Join(chain, " → "),
			Upload: c.Upload, Download: c.Download, Start: c.Start.Format(time.RFC3339),
		}
		conn.App, conn.Icon = clientOf(conn)
		conns = append(conns, conn)
	}
	slices.SortFunc(conns, func(a, b Conn) int { return strings.Compare(b.Start, a.Start) })
	return conns, nil
}

// CloseAll closes every live connection.
func (s *CoreService) CloseAll() error {
	defer s.forgetConnections()
	return s.core.Do(context.Background(), http.MethodDelete, "/connections", nil, nil)
}

// forgetConnections drops the cached snapshot, so a list after a close shows the close.
func (s *CoreService) forgetConnections() {
	s.connMu.Lock()
	s.connAt = time.Time{}
	s.connMu.Unlock()
}

// Reload re-applies the current config (profile and override) to the core.
func (s *CoreService) Reload() error {
	return s.onReady()
}

// Restart restarts the core process (the root daemon re-executes itself, a child is started anew)
// and hands it the current config again.
func (s *CoreService) Restart() error {
	restarting.Add(1) // the heartbeat would reload the config a second time otherwise
	defer restarting.Add(-1)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if s.core.Daemon() {
		if err := s.core.Do(ctx, http.MethodPost, "/restart", nil, nil); err != nil {
			return err
		}
		time.Sleep(time.Second) // the old process answers until it has re-executed
		if err := s.core.Attach(ctx, daemon.Socket, daemon.LogPath); err != nil {
			return err
		}
	} else {
		s.core.Stop()
		if err := s.core.Start(ctx); err != nil {
			s.setStatus(Status{Error: err.Error()})
			return err
		}
	}
	if err := s.Reload(); err != nil {
		return err
	}
	s.markRunning(ctx)
	notify("info", "内核已重启")
	return nil
}

func (s *CoreService) CloseConnection(id string) error {
	defer s.forgetConnections()
	return s.core.Do(context.Background(), http.MethodDelete, "/connections/"+url.PathEscape(id), nil, nil)
}

// CloseHost closes every live connection to host, so they reconnect under a new rule.
func (s *CoreService) CloseHost(host string) error {
	return s.closeWhere(func(c Conn) bool { return c.Host == host || strings.HasSuffix(c.Host, "."+host) })
}

// CloseSource closes every live connection from a source IP, so a device picks up a new policy.
func (s *CoreService) CloseSource(ip string) error {
	return s.closeWhere(func(c Conn) bool { return c.Source == ip })
}

// closeWhere closes every live connection match picks.
func (s *CoreService) closeWhere(match func(Conn) bool) error {
	conns, err := s.Connections()
	if err != nil {
		return err
	}
	for _, c := range conns {
		if match(c) {
			_ = s.CloseConnection(c.ID)
		}
	}
	return nil
}

// AddTempRule adds a runtime rule like "DOMAIN-SUFFIX,google.com,DIRECT"; it lasts until the next reload.
func (s *CoreService) AddTempRule(rule string) (int, error) {
	var resp struct {
		ID int `json:"id"`
	}
	err := s.core.Do(context.Background(), http.MethodPost, "/rules/temporary", map[string]string{"rule": rule}, &resp)
	return resp.ID, err
}

func (s *CoreService) TempRules() ([]TempRule, error) {
	var resp struct {
		Rules []struct {
			Index   int    `json:"index"`
			Type    string `json:"type"`
			Payload string `json:"payload"`
			Proxy   string `json:"proxy"`
			Extra   *struct {
				HitCount uint64 `json:"hitCount"`
			} `json:"extra"`
		} `json:"rules"`
	}
	if err := s.core.Do(context.Background(), http.MethodGet, "/rules/temporary", nil, &resp); err != nil {
		return nil, err
	}
	rules := make([]TempRule, 0, len(resp.Rules))
	for _, r := range resp.Rules {
		t := TempRule{ID: r.Index, Type: r.Type, Payload: r.Payload, Proxy: r.Proxy}
		if r.Extra != nil {
			t.Hits = r.Extra.HitCount
		}
		rules = append(rules, t)
	}
	return rules, nil
}

func (s *CoreService) RemoveTempRule(id int) error {
	return s.core.Do(context.Background(), http.MethodDelete, "/rules/temporary/"+strconv.Itoa(id), nil, nil)
}

// Policies are the targets a rule can point at: the config's groups, DIRECT and REJECT.
func (s *CoreService) Policies() ([]string, error) {
	groups, err := s.Groups()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, g := range groups {
		if g.Name != "GLOBAL" {
			names = append(names, g.Name)
		}
	}
	return append(names, "DIRECT", "REJECT"), nil
}
