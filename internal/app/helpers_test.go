package app

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ryanzhangtianran/Connaio/internal/core"
	"github.com/ryanzhangtianran/Connaio/internal/profile"
)

// startCore runs a core from CONNAIO_CORE on a free mixed-port (a running Connaio may hold
// 7890), with override on top of the default config. Skips without CONNAIO_CORE.
func startCore(t *testing.T, override string) (*CoreService, *ProfileService, int) {
	t.Helper()
	if os.Getenv("CONNAIO_CORE") == "" {
		t.Skip("CONNAIO_CORE not set")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	dir := t.TempDir()
	store, _ := profile.Open(dir)
	if err := store.SetOverride(fmt.Sprintf("mixed-port: %d\n%s", port, override)); err != nil {
		t.Fatal(err)
	}
	cs := &CoreService{core: core.New(dir), ledger: openLedger(filepath.Join(t.TempDir(), "traffic.json")), onReady: func() error { return nil }}
	ps := &ProfileService{store: store, core: cs.core, settings: loadSettings(t.TempDir())}
	if err := ps.WriteConfig(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := cs.core.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.core.Stop() })
	return cs, ps, port
}
