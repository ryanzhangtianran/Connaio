//go:build server

package app

import (
	"strings"
	"testing"

	"github.com/ryanzhangtianran/Connaio/internal/core"
	"github.com/ryanzhangtianran/Connaio/internal/profile"
)

// headless, enhanced mode on: the config asks the core for TUN, though no separate daemon runs.
func TestHeadlessBuildsTun(t *testing.T) {
	store, err := profile.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := &ProfileService{store: store, core: core.New(t.TempDir()), settings: loadSettings(t.TempDir())}
	if err := s.settings.Update(func(st *Settings) { st.Enhanced = true }); err != nil {
		t.Fatal(err)
	}
	b, err := s.build()
	if err != nil || !strings.Contains(string(b), "mode: tun") {
		t.Fatalf("%v\n%s", err, b)
	}
}
