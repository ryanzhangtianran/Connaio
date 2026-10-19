package daemon

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"github.com/ryanzhangtianran/Connaio/internal/run"
	"os"
	"path/filepath"
	"strings"
)

// The root core is the ConnaioCore Windows service, running as LocalSystem (the core runs in
// service mode when started by the SCM). It runs the installed core in place, under Program Files,
// where only administrators write: the installer, elevated already, stops it, replaces the files and
// starts it again, so an update needs no second UAC prompt. A core anywhere else (a build run from
// its folder) is copied to ProgramData first, since the service must not run a file its user can
// change. It serves its API on a named pipe. Install and Uninstall run a batch script elevated, so
// each is one UAC prompt.
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

// inProgramFiles says whether path is under Program Files, which only administrators can write.
func inProgramFiles(path string) bool {
	pf := os.Getenv("ProgramFiles")
	return pf != "" && strings.HasPrefix(strings.ToLower(path), strings.ToLower(strings.TrimRight(pf, `\`))+`\`)
}

// installScript sets the service up (or points an existing one at core) and starts it: core itself
// when it is under Program Files, removing the copy an older version ran; else a copy of it.
func installScript(core, stage string) string {
	q := func(s string) string { return `"` + s + `"` }
	exe := Helper
	lines := []string{
		"@echo off",
		"if not exist " + q(Dir) + " mkdir " + q(Dir) + " || exit /b 1",
		"if not exist " + q(filepath.Join(Dir, "config.yaml")) + " copy /Y " + q(filepath.Join(stage, "config.yaml")) + " " + q(filepath.Join(Dir, "config.yaml")) + " >nul || exit /b 1",
		"sc stop " + Service + " >nul 2>&1",
	}
	if inProgramFiles(core) {
		exe = core
	} else {
		lines = append(lines,
			"copy /Y "+q(core)+" "+q(Helper)+" >nul || exit /b 1",
			"if exist "+q(filepath.Join(filepath.Dir(core), "wintun.dll"))+" copy /Y "+q(filepath.Join(filepath.Dir(core), "wintun.dll"))+" "+q(filepath.Join(Dir, "wintun.dll"))+" >nul")
	}
	binPath := q(`\"` + exe + `\" -d \"` + Dir + `\" -ext-ctl-pipe ` + Socket)
	lines = append(lines,
		"sc query "+Service+" >nul 2>&1 && (sc config "+Service+" binPath= "+binPath+" start= auto >nul || exit /b 1) || (sc create "+Service+" binPath= "+binPath+" start= auto DisplayName= \"Connaio Core\" >nul || exit /b 1)",
		"sc start "+Service+" >nul || exit /b 1")
	if exe == core {
		lines = append(lines, "del /F /Q "+q(Helper)+" >nul 2>&1", "del /F /Q "+q(filepath.Join(Dir, "wintun.dll"))+" >nul 2>&1")
	}
	return strings.Join(lines, "\r\n") + "\r\n"
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
	return asAdmin(func(stage string) (string, error) {
		err := os.WriteFile(filepath.Join(stage, "config.yaml"), []byte(BootConfig), 0o644)
		return installScript(core, stage), err
	})
}

// Stale reports whether the service runs another core binary than the one bundled with this app:
// for an installed core, whether it still runs a copy (an older version's way, moved over once);
// otherwise whether its copy differs.
func Stale(bin string) bool {
	if inProgramFiles(bin) {
		_, err := os.Stat(Helper)
		return err == nil
	}
	installed, err1 := os.ReadFile(Helper)
	bundled, err2 := os.ReadFile(bin)
	return err1 == nil && err2 == nil && !bytes.Equal(installed, bundled)
}

// Uninstall stops and removes the service and its binary; the data dir is kept.
func Uninstall() error {
	return asAdmin(func(string) (string, error) { return uninstallScript(), nil })
}

// asAdmin runs a batch file elevated through the UAC prompt and waits for it. script writes what
// the batch file needs into a temporary directory, stage, and returns its text.
func asAdmin(script func(stage string) (string, error)) error {
	stage, err := os.MkdirTemp("", "connaio-admin-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	batch, err := script(stage)
	if err != nil {
		return err
	}
	path := filepath.Join(stage, "connaio.cmd")
	if err := os.WriteFile(path, []byte(batch), 0o644); err != nil {
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
	return asAdmin(func(string) (string, error) { return script, nil })
}
