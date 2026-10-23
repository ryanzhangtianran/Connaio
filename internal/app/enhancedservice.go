package app

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/ryanzhangtianran/Connaio/internal/core"
	"github.com/ryanzhangtianran/Connaio/internal/daemon"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func init() {
	application.RegisterEvent[bool]("enhanced")
}

// EnhancedService switches enhanced mode (TUN). It needs the root daemon, which Grant installs first.
type EnhancedService struct {
	core     *core.Core
	profiles *ProfileService
	settings *settingsFile
	onChange func(bool) // menu bar checkbox
}

func (s *EnhancedService) Enabled() bool { return s.settings.Get().Enhanced }

// Mode is how TUN is used: "" (off), "tun" (enhanced mode, all traffic) or "process-tun" (partial
// enhancement: only the selected apps and their child processes).
func (s *EnhancedService) Mode() string {
	if !s.Enabled() {
		return ""
	}
	if v, _ := s.profiles.Value("capture.mode"); v == "process-tun" {
		return "process-tun"
	}
	return "tun"
}

// Full is whether enhanced mode is on: all traffic through TUN.
func (s *EnhancedService) Full() bool { return s.Mode() == "tun" }

// Partial is whether partial enhancement is chosen. Enhanced mode covers it: it captures while
// enhanced mode is off, and waits underneath while it is on.
func (s *EnhancedService) Partial() bool { return s.settings.Get().Partial }

// SetFull turns enhanced mode on or off; off falls back to partial enhancement when that is chosen.
func (s *EnhancedService) SetFull(on bool) error { return s.set(on, s.Partial()) }

// SetPartial chooses partial enhancement or not, leaving enhanced mode as it is.
func (s *EnhancedService) SetPartial(on bool) error { return s.set(s.Full(), on) }

// set records the two choices and runs what they come to: enhanced mode if on, else partial
// enhancement if chosen, else no TUN. The config is applied only when that changes, since applying
// rebuilds the TUN under every connection.
func (s *EnhancedService) set(full, partial bool) error {
	mode := ""
	switch {
	case full:
		mode = "tun"
	case partial:
		mode = "process-tun"
	}
	was := s.Partial()
	if err := s.settings.Update(func(st *Settings) { st.Partial = partial }); err != nil {
		return err
	}
	undo := func(err error) error {
		_ = s.settings.Update(func(st *Settings) { st.Partial = was })
		return err
	}
	switch {
	case mode == s.Mode():
		s.changed(s.Enabled()) // only the choice underneath changed: the views show it
	case mode == "":
		if err := s.SetEnabled(false); err != nil {
			return undo(err)
		}
	default:
		// capture.mode goes into the override without applying; SetEnabled applies once and
		// rolls both back if the core refuses
		old, _ := s.profiles.Value("capture.mode")
		if err := s.profiles.store.SetPath([]string{"capture", "mode"}, mode); err != nil {
			return undo(err)
		}
		if err := s.SetEnabled(true); err != nil {
			if old != nil {
				_ = s.profiles.store.SetPath([]string{"capture", "mode"}, old)
			}
			return undo(err)
		}
		emit("setting", "capture.mode")
	}
	return nil
}

// DaemonInstalled reports whether the core may run as root: the daemon installed, or headless,
// Connaio itself running as root, its child core with it.
func (s *EnhancedService) DaemonInstalled() bool {
	if headless {
		return os.Geteuid() == 0
	}
	return daemon.Installed()
}

// errHeadlessRoot answers granting root headless: Connaio is run as root instead.
var errHeadlessRoot = errors.New("以 root 运行 Connaio 即可使用增强模式")

// Grant installs the root daemon (one administrator prompt) and moves the GUI's core onto it;
// enhanced mode can be turned on only after this.
func (s *EnhancedService) Grant() error {
	if headless {
		return errHeadlessRoot
	}
	bin, err := core.Binary()
	if err != nil {
		return err
	}
	if err := daemon.Install(bin); err != nil {
		return err
	}
	if err := s.useDaemon(); err != nil {
		return err
	}
	if err := s.profiles.apply(); err != nil {
		return err
	}
	s.changed(s.Enabled())
	return nil
}

func (s *EnhancedService) SetEnabled(on bool) error {
	if on && headless && os.Geteuid() != 0 {
		return errHeadlessRoot
	}
	if on && !headless && !s.core.Daemon() {
		if !daemon.Installed() {
			return errors.New("请先在「通用」中授予 root 权限")
		}
		if err := s.useDaemon(); err != nil {
			return err
		}
	}
	if err := s.settings.Update(func(st *Settings) { st.Enhanced = on }); err != nil {
		return err
	}
	if err := s.profiles.apply(); err != nil {
		_ = s.settings.Update(func(st *Settings) { st.Enhanced = !on })
		return err
	}
	s.changed(on)
	return nil
}

// useDaemon moves the GUI from its child core to the installed daemon.
func (s *EnhancedService) useDaemon() error {
	restarting.Add(1) // the traffic stream breaks here; the caller applies the config itself
	defer restarting.Add(-1)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s.core.Stop() // frees the ports the daemon is about to take over
	if err := s.core.Attach(ctx, daemon.Socket, daemon.LogPath); err != nil {
		return errors.Join(err, s.core.Start(ctx)) // keep a working core either way
	}
	return nil
}

// Uninstall turns enhanced mode off, removes the daemon and goes back to a child core.
func (s *EnhancedService) Uninstall() error {
	if headless {
		return errHeadlessRoot
	}
	restarting.Add(1) // the stream breaks as the daemon goes; the config is applied below
	defer restarting.Add(-1)
	if s.Enabled() {
		if err := s.SetEnabled(false); err != nil {
			return err
		}
	}
	if err := daemon.Uninstall(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := s.core.Start(ctx); err != nil {
		return err
	}
	if err := s.profiles.apply(); err != nil {
		return err
	}
	s.changed(false) // the window and menu bar disable enhancing again
	return nil
}

func (s *EnhancedService) changed(on bool) {
	if s.onChange != nil {
		s.onChange(on)
	}
	emit("enhanced", on)
}
