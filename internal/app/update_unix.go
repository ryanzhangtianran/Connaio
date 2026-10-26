//go:build unix

package app

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// restartApp quits and starts this app again, on the data as it is now (a restored backup).
func restartApp() error {
	cmd := restartCommand()
	if len(cmd) == 0 || cmd[len(cmd)-1] == "" { // macOS run outside a bundle, as in development
		return errors.New("无法自动重启，请手动重新打开 Connaio")
	}
	return relaunch(cmd)
}

// relaunch quits and runs cmd once this process is gone, so the single-instance check doesn't
// hand the new one back to the quitting one.
func relaunch(cmd []string) error {
	// the service: systemd restarts it; a command left waiting would end with it, in its cgroup
	if headless && os.Getenv("INVOCATION_ID") != "" {
		go func() {
			time.Sleep(300 * time.Millisecond) // the page hears the call returned first
			if out, err := exec.Command("systemctl", "--no-block", "restart", "connaio").CombinedOutput(); err != nil {
				notify("error", "重启服务失败：%s", out)
			}
		}()
		return nil
	}
	wait := `while kill -0 "$0" 2>/dev/null; do sleep 0.2; done; exec "$@"`
	c := exec.Command("sh", append([]string{"-c", wait, strconv.Itoa(os.Getpid())}, cmd...)...)
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // outlives this process
	c.Stdout, c.Stderr = os.Stdout, os.Stderr          // /etc/init.d/connaio's log goes on
	if err := c.Start(); err != nil {
		return err
	}
	go func() {
		time.Sleep(300 * time.Millisecond) // the window hears the call returned first
		application.Get().Quit()
	}()
	return nil
}
