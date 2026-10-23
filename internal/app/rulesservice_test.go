package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// Needs a built core: CONNAIO_CORE=/path/to/connaio-core go test -run TestRulesService .
func TestRulesService(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, _, _ := startCore(t, `prepend-rules:
  - DOMAIN-SUFFIX,example.com,DIRECT
reverse-tunnels:
  - name: wg
    type: wireguard
    server: 127.0.0.1
    port: 1
    ip: 10.8.0.2
    private-key: dwdtCnMYpX08FsFyUbJmRd9ML4frwJkqsXf7pR25LCo=
    public-key: hSDwCYkwp1R0i33ctD73Wg2/Og0mOBr066SpjqqbTmo=
    forwards:
      - {remote: 0.0.0.0:8080, local: 127.0.0.1:80}
`)

	rules, err := s.Rules()
	if err != nil || len(rules) != 2 || rules[0].Payload != "example.com" {
		t.Fatalf("rules %+v %v", rules, err)
	}
	if err := s.SetRuleDisabled(0, true); err != nil {
		t.Fatal(err)
	}
	if rules, _ = s.Rules(); !rules[0].Disabled || rules[1].Disabled {
		t.Fatalf("disable: %+v", rules)
	}

	tunnels, err := s.ReverseTunnels()
	if err != nil || len(tunnels) != 1 || tunnels[0].Name != "wg" || tunnels[0].Type != "wireguard" || len(tunnels[0].Forwards) != 1 || tunnels[0].State == "running" {
		t.Fatalf("tunnels %+v %v", tunnels, err)
	}

	got := make(chan LogLine, 1)
	streamCtx, stop := context.WithCancel(ctx)
	defer stop()
	go s.core.Stream(streamCtx, "/logs?level=info", func(line []byte) {
		var l LogLine
		if json.Unmarshal(line, &l) == nil && l.Payload != "" {
			select {
			case got <- l:
			default:
			}
		}
	})
	time.Sleep(200 * time.Millisecond)
	_ = s.SetMode("global")                                                  // logs nothing by itself; a failing tunnel retry logs a warning
	_ = s.core.Reload(ctx, []byte("mode: rule\nrules:\n  - MATCH,DIRECT\n")) // a reload logs at info
	select {
	case l := <-got:
		t.Logf("log line: [%s] %s", l.Level, l.Payload)
	case <-time.After(5 * time.Second):
		t.Fatal("no log line")
	}
}
