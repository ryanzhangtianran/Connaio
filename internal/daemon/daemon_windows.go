package daemon

import (
	"cmp"
	"errors"
	"fmt"
	"github.com/ryanzhangtianran/Connaio/internal/run"
	"os"
	"path/filepath"
	"strings"
)

// The root core is the ConnaioCore Windows service, running as LocalSystem from its own copy of
// the binary under ProgramData (the core runs in service mode when started by the SCM). It
// serves its API on a named pipe. Install and Uninstall run a batch script elevated, so each is
// one UAC prompt.
var (
	Service = "ConnaioCore"
	Dir     = filepath.Join(cmp.Or(os.Getenv("ProgramData"), `C:\ProgramData`), "Connaio Core")
	Socket  = `\\.\pipe\connaio-core`
	Helper  = filepath.Join(Dir, "connaio-core.exe")
	LogPath = filepath.Join(Dir, "core.log") // written by the core itself in service mode
)

// Installed reports whether the service exists.
func Installed() bool {
	return run.Command("sc", "query", Service).Run() == nil
}

// installScript sets the service up (or points an existing one at the new binary) and starts it.
func installScript(core, stage string) string {
	q := func(s string) string { return `"` + s + `"` }
	binPath := q(`\"` + Helper + `\" -d \"` + Dir + `\" -ext-ctl-pipe ` + Socket)
	return strings.Join([]string{
		"@echo off",
		"if not exist " + q(Dir) + " mkdir " + q(Dir) + " || exit /b 1",
		"if not exist " + q(filepath.Join(Dir, "config.yaml")) + " copy /Y " + q(filepath.Join(stage, "config.yaml")) + " " + q(filepath.Join(Dir, "config.yaml")) + " >nul || exit /b 1",
		"sc stop " + Service + " >nul 2>&1",
		"copy /Y " + q(core) + " " + q(Helper) + " >nul || exit /b 1",
		"if exist " + q(filepath.Join(filepath.Dir(core), "wintun.dll")) + " copy /Y " + q(filepath.Join(filepath.Dir(core), "wintun.dll")) + " " + q(filepath.Join(Dir, "wintun.dll")) + " >nul",
		"sc query " + Service + " >nul 2>&1 && (sc config " + Service + " binPath= " + binPath + " start= auto >nul || exit /b 1) || (sc create " + Service + " binPath= " + binPath + " start= auto DisplayName= \"Connaio Core\" >nul || exit /b 1)",
		"sc start " + Service + " >nul || exit /b 1",
	}, "\r\n") + "\r\n"
}

func uninstallScript() string {
	return strings.Join([]string{
		"@echo off",
		"sc stop " + Service + " >nul 2>&1",
		"sc delete " + Service + " >nul 2>&1",
		"del /F /Q \"" + Helper + "\" >nul 2>&1",
		"exit /b 0",
	}, "\r\n") + "\r\n"
}

// Install copies core into ProgramData and registers and starts the service, after one UAC prompt.
func Install(core string) error {
	stage, err := os.MkdirTemp("", "connaio-daemon-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := os.WriteFile(filepath.Join(stage, "config.yaml"), []byte(BootConfig), 0o644); err != nil {
		return err
	}
	return asAdmin(stage, installScript(core, stage))
}

// Stale reports whether the service runs another core binary than the one bundled with this app.
func Stale(bin string) bool {
	installed, err1 := fileSum(Helper)
	bundled, err2 := fileSum(bin)
	return err1 == nil && err2 == nil && installed != bundled
}

// Uninstall stops and removes the service and its binary; the data dir is kept.
func Uninstall() error {
	stage, err := os.MkdirTemp("", "connaio-daemon-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	return asAdmin(stage, uninstallScript())
}

// asAdmin runs script as a batch file elevated through the UAC prompt and waits for it.
func asAdmin(stage, script string) error {
	path := filepath.Join(stage, "connaio.cmd")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		return err
	}
	ps := "$p = Start-Process cmd.exe -ArgumentList '/c','\"" + path + "\"' -Verb RunAs -Wait -PassThru; exit $p.ExitCode"
	out, err := run.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps).CombinedOutput()
	text := strings.TrimSpace(string(out))
	switch {
	case err == nil:
		return nil
	case strings.Contains(strings.ToLower(text), "canceled") || strings.Contains(strings.ToLower(text), "cancelled"): // the UAC prompt was dismissed
		return errors.New("已取消授权")
	default:
		return fmt.Errorf("授权失败：%s", cmp.Or(text, err.Error()))
	}
}

// RunAsAdmin runs a batch script elevated through the UAC prompt, for network repairs.
func RunAsAdmin(script, _ string) error {
	stage, err := os.MkdirTemp("", "connaio-repair")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	return asAdmin(stage, script)
}

// InBundle says the daemon runs the app's own core; here it runs a copy, replaced by Install when Stale.
const InBundle = false
