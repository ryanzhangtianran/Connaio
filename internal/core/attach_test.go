//go:build unix

package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// TestAttach runs the core like the LaunchDaemon does (its own dir and socket, the socket handed
// to one uid) and attaches to it. Needs CONNAIO_CORE.
func TestAttach(t *testing.T) {
	bin := os.Getenv("CONNAIO_CORE")
	if bin == "" {
		t.Skip("CONNAIO_CORE not set")
	}
	daemonDir := t.TempDir()
	socket := filepath.Join(daemonDir, "core.sock")
	os.WriteFile(filepath.Join(daemonDir, "config.yaml"), []byte("mixed-port: 0\nrules:\n  - MATCH,DIRECT\n"), 0o644)
	cmd := exec.Command(bin, "-d", daemonDir, "-ext-ctl-unix", socket)
	cmd.Env = append(os.Environ(), "CONNAIO_SOCKET_UID="+strconv.Itoa(os.Getuid()))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c := New(t.TempDir())
	if err := c.Attach(ctx, socket, filepath.Join(daemonDir, "core.log")); err != nil {
		t.Fatal(err)
	}
	if !c.Daemon() {
		t.Fatal("attached core must report daemon mode")
	}
	fi, err := os.Stat(socket)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("socket mode %o, want 600", perm)
	}
	if uid := fi.Sys().(*syscall.Stat_t).Uid; int(uid) != os.Getuid() {
		t.Fatalf("socket owner %d", uid)
	}

	if err := c.Reload(ctx, []byte("mode: global\nrules:\n  - MATCH,DIRECT\n")); err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Mode string `json:"mode"`
	}
	if err := c.Do(ctx, "GET", "/configs", nil, &cfg); err != nil || cfg.Mode != "global" {
		t.Fatalf("payload reload: %q %v", cfg.Mode, err)
	}
	if err := c.Do(ctx, "POST", "/upgrade", nil, nil); err == nil {
		t.Fatal("core self-upgrade must be gone")
	}
	c.Stop()
	if _, err := c.Version(ctx); err != nil {
		t.Fatal("Stop must leave an attached daemon running")
	}
}
