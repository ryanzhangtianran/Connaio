package app

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/ryanzhangtianran/Connaio/internal/run"
	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/windows"
)

// updateAsset ends the name of this platform's release package: the NSIS installer.
const updateAsset = "-windows-" + runtime.GOARCH + ".exe"

// installPackage hands the install to a script that outlives this app: the installer wants
// elevation, which only ShellExecute (Start-Process -Verb RunAs) can ask for, and it kills a running
// Connaio, so nothing here could wait for it. Once this app has quit (relaunch), the script runs the
// installer silently, drops it, and starts the new version; a declined prompt starts the old one
// again. The installer is moved out of the download dir first, which InstallUpdate removes.
func installPackage(installer string) error {
	moved := filepath.Join(os.TempDir(), "Connaio-update.exe")
	if err := os.Rename(installer, moved); err != nil {
		return err
	}
	exe, _ := os.Executable()
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	log := filepath.Join(os.TempDir(), "connaio-update.log") // what the script did, for when an update doesn't take
	script := fmt.Sprintf("Start-Transcript -Path %s -Force | Out-Null; Wait-Process -Id %d -ErrorAction SilentlyContinue; try { Start-Process -FilePath %s -ArgumentList '/S' -Verb RunAs -Wait } catch { $_ }; Remove-Item -Force %s; Start-Process -FilePath %s",
		q(log), os.Getpid(), q(moved), q(moved), q(exe))
	return startDetached(run.Command("powershell", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script))
}

// startDetached starts c to outlive this app: no window, its own process group, and out of the Job
// this app runs in. A launcher that put Connaio in a kill-on-close Job (file managers and app
// launchers do) would otherwise end it with Connaio, before it had installed or restarted anything.
// A Job that forbids breaking away refuses that start; then c starts in it, as before.
func startDetached(c *exec.Cmd) error {
	flags := uint32(windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: flags | windows.CREATE_BREAKAWAY_FROM_JOB}
	err := c.Start()
	if errors.Is(err, syscall.ERROR_ACCESS_DENIED) {
		again := exec.Command(c.Path, c.Args[1:]...)
		again.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: flags}
		return again.Start()
	}
	return err
}

func restartCommand() []string { return nil } // installPackage's script starts the new version

// restartApp quits and starts this app again, once this process is gone (the single-instance check
// would hand a new one back to the quitting one), on the data as it is now (a restored backup).
func restartApp() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	script := fmt.Sprintf("Wait-Process -Id %d -ErrorAction SilentlyContinue; Start-Process -FilePath '%s'", os.Getpid(), strings.ReplaceAll(exe, "'", "''"))
	if err := startDetached(run.Command("powershell", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script)); err != nil {
		return err
	}
	return relaunch(nil)
}

// relaunch quits; installPackage's script, waiting for that, installs and starts what comes next.
func relaunch([]string) error {
	go func() {
		time.Sleep(300 * time.Millisecond) // the window hears the call returned first
		application.Get().Quit()
	}()
	return nil
}
