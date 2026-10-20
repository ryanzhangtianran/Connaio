package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/ryanzhangtianran/Connaio/internal/profile"
)

// Settings are the GUI's own switches, kept in settings.json next to the config.
type Settings struct {
	SystemProxy bool `json:"systemProxy"`
	Enhanced    bool `json:"enhanced"`
	// Partial remembers that partial enhancement was on when enhanced mode went on over it, so
	// turning enhanced mode off returns to it rather than to nothing.
	Partial bool `json:"partial,omitempty"`
	// HideTraySpeed hides the upload/download speed next to the menu bar icon.
	HideTraySpeed bool `json:"hideTraySpeed"`
	// VPNs are the VPN page's entries by name, kept while disconnected so 连接 needs no retyping.
	VPNs map[string]VPN `json:"vpns,omitempty"`
	// Monitor is where the floating process monitor was last closed, x and y, to open it there again.
	Monitor []int `json:"monitor,omitempty"`
	// Sites are the activity page's 服务状态 sites; none: defaultSites.
	Sites []Site `json:"sites,omitempty"`
}

type settingsFile struct {
	path string
	mu   sync.Mutex
	s    Settings
}

func loadSettings(dir string) *settingsFile {
	f := &settingsFile{path: filepath.Join(dir, "settings.json")}
	if b, err := os.ReadFile(f.path); err == nil {
		_ = json.Unmarshal(b, &f.s)
	}
	return f
}

func (f *settingsFile) Get() Settings {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.s
}

func (f *settingsFile) Update(change func(*Settings)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(&f.s)
	b, err := json.MarshalIndent(f.s, "", "  ")
	if err != nil {
		return err
	}
	return profile.WriteAtomic(f.path, b, 0o644)
}
