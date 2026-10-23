package app

import (
	"github.com/ryanzhangtianran/Connaio/internal/run"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/ryanzhangtianran/Connaio/internal/daemon"
)

// appVersion is set at build time from the git tag (-ldflags -X); "dev" in a plain go build.
var appVersion = "dev"

// SettingsService covers the GUI-side settings: launch at login and the about page.
type SettingsService struct {
	core        *CoreService
	settings    *settingsFile
	openMonitor func()             // app.go: shows the floating process monitor
	setTheme    func(theme string) // app.go: dresses the open windows
}

// SetTheme keeps the windows' appearance, "system", "light" or "dark", and applies it to those open.
func (s *SettingsService) SetTheme(theme string) error {
	if theme == "system" {
		theme = ""
	}
	if s.settings.Get().Theme == theme { // sent by every page load
		return nil
	}
	if err := s.settings.Update(func(st *Settings) { st.Theme = theme }); err != nil {
		return err
	}
	if s.setTheme != nil {
		s.setTheme(theme)
	}
	return nil
}

// OpenMonitor shows the floating process monitor, a small window kept above the others.
func (s *SettingsService) OpenMonitor() {
	if s.openMonitor != nil {
		s.openMonitor()
	}
}

type About struct {
	Version     string `json:"version"`
	CoreVersion string `json:"coreVersion"`
	DataDir     string `json:"dataDir"`
	Daemon      bool   `json:"daemon"`   // privileged service installed
	Attached    bool   `json:"attached"` // GUI is using it
}

func (s *SettingsService) About() About {
	return About{
		Version:     appVersion,
		CoreVersion: s.core.Status().Version,
		DataDir:     s.core.core.Dir,
		Daemon:      daemon.Installed(),
		Attached:    s.core.core.Daemon() || headless && os.Geteuid() == 0, // headless as root: its own core runs as root
	}
}

// OpenDataDir shows the data folder (config, logs, profiles) in Finder or the Linux file manager.
func (s *SettingsService) OpenDataDir() error {
	opener := "open"
	switch runtime.GOOS {
	case "linux":
		opener = "xdg-open"
	case "windows":
		opener = "explorer"
	}
	return run.Command(opener, s.core.core.Dir).Run()
}

func (s *SettingsService) LaunchAtLogin() bool {
	p, err := loginItemPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// TraySpeed reports whether the menu bar shows the current speed; it applies on the next second's update.
// Sites are the 服务状态 card's sites, the defaults until changed.
func (s *SettingsService) Sites() []Site { return sites(s.settings) }

// SetSites replaces the card's sites; an empty list puts the defaults back.
func (s *SettingsService) SetSites(list []Site) error {
	list, err := cleanSites(list)
	if err != nil {
		return err
	}
	return s.settings.Update(func(st *Settings) { st.Sites = list })
}

func (s *SettingsService) TraySpeed() bool { return !s.settings.Get().HideTraySpeed }

func (s *SettingsService) SetTraySpeed(on bool) error {
	return s.settings.Update(func(st *Settings) { st.HideTraySpeed = !on })
}

// SetLaunchAtLogin installs or removes a LaunchAgent that opens this app at login.
func (s *SettingsService) SetLaunchAtLogin(on bool) error {
	p, err := loginItemPath()
	if err != nil {
		return err
	}
	if !on {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(loginItemFile(exe)), 0o644)
}

// ProcessChoice is something the enhanced-mode scope can name: an app or a networked process.
type ProcessChoice struct {
	Name   string `json:"name"`             // shown to the user, e.g. "Lark"
	Exec   string `json:"exec"`             // executable name the PROCESS-ANCESTOR match uses, e.g. "Feishu"
	Source string `json:"source"`           // "app": running app, "network": has live connections
	Parent string `json:"parent,omitempty"` // the app it runs under, whose capture covers it (network only)
}

// ProcessChoices lists running apps and processes with live connections, apps first.
func (s *SettingsService) ProcessChoices() []ProcessChoice {
	choices := runningApps()
	seen := map[string]bool{}
	for _, c := range choices {
		seen[strings.ToLower(c.Exec)] = true
	}
	if conns, err := s.core.connections(true); err == nil { // processes outside the scope are the ones to add
		var network []ProcessChoice
		for _, c := range conns {
			if systemDaemon(c.Path) || c.System {
				continue
			}
			if c.Process != "" && !seen[strings.ToLower(c.Process)] {
				seen[strings.ToLower(c.Process)] = true
				network = append(network, ProcessChoice{Name: c.Process, Exec: c.Process, Source: "network"})
			}
		}
		slices.SortFunc(network, func(a, b ProcessChoice) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
		for i, c := range network {
			if p := parentOf(c.Exec); p != "" {
				network[i].Parent = filepath.Base(p)
			}
		}
		choices = append(choices, network...)
	}
	return choices
}
