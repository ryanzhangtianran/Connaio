//go:build !windows

package app

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ryanzhangtianran/Connaio/internal/core"
	"github.com/ryanzhangtianran/Connaio/internal/profile"
)

func TestEnhancementConfigCommit(t *testing.T) {
	// Use a short pathname so the test also fits macOS's Unix socket limit.
	socketDir, err := os.MkdirTemp("/tmp", "connaio-config-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(socketDir) })
	socket := filepath.Join(socketDir, "control.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var reloads atomic.Int32
	var rejectReload atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"connaio":1}`)
	})
	mux.HandleFunc("PUT /configs", func(w http.ResponseWriter, r *http.Request) {
		reloads.Add(1)
		if rejectReload.Load() {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"message":"reload rejected"}`)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	srv := &http.Server{Handler: mux}
	t.Cleanup(func() { srv.Close(); ln.Close() })
	go srv.Serve(ln)

	for _, tc := range []struct {
		name, validator, wantErr string
		reloads                  int32
		previousEnhanced         bool
	}{
		{name: "validation failure", validator: "/usr/bin/false", wantErr: "配置校验失败"},
		{name: "reload failure while enabled", validator: "/usr/bin/true", wantErr: "reload rejected", reloads: 1, previousEnhanced: true},
		{name: "success", validator: "/usr/bin/true", reloads: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CONNAIO_CORE", tc.validator)
			rejectReload.Store(tc.wantErr == "reload rejected")
			reloads.Store(0)
			dir := t.TempDir()
			store, err := profile.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			const original = "# preserve the user's config\nmixed-port: 7891\n"
			if err := store.SetOverride(original); err != nil {
				t.Fatal(err)
			}
			s := &ProfileService{store: store, core: core.New(dir), settings: loadSettings(dir)}
			if err := s.settings.Update(func(st *Settings) { st.Enhanced = tc.previousEnhanced }); err != nil {
				t.Fatal(err)
			}
			if err := s.core.Attach(context.Background(), socket, ""); err != nil {
				t.Fatal(err)
			}
			if err := s.WriteConfig(); err != nil {
				t.Fatal(err)
			}
			readConfig := func() string {
				t.Helper()
				b, err := os.ReadFile(s.core.ConfigPath())
				if err != nil {
					t.Fatal(err)
				}
				return string(b)
			}
			before := readConfig()
			partial := true
			err = s.applyEnhancement(true, "process-tun", &partial)
			if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("apply error = %v, want %q", err, tc.wantErr)
			}
			if got := reloads.Load(); got != tc.reloads {
				t.Fatalf("reloads = %d, want %d", got, tc.reloads)
			}
			settings := s.settings.Get()
			if tc.wantErr == "" {
				mode, err := s.Value("capture.mode")
				if !settings.Enhanced || !settings.Partial || err != nil || mode != "process-tun" {
					t.Fatalf("mode and settings weren't applied: %v %v %+v", mode, err, settings)
				}
				return
			}
			if persisted := loadSettings(dir).Get(); settings.Enhanced != tc.previousEnhanced || settings.Partial || persisted.Enhanced != tc.previousEnhanced || persisted.Partial {
				t.Fatalf("settings weren't restored: %+v (disk %+v)", settings, persisted)
			}
			if override, err := store.Override(); err != nil || override != original || readConfig() != before {
				t.Fatalf("config wasn't restored: override %q, error %v", override, err)
			}
		})
	}
}
