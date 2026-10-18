package daemon

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// The root core is connaio-core@<uid>.service, a systemd template the .deb installs, disabled, to
// run /usr/lib/connaio/connaio-core: the binary is root-owned from the start, so unlike macOS
// nothing is copied or checksummed. connaio-root, which the .deb also installs, enables or
// disables it for the calling user when run through pkexec, under a polkit action that names
// Connaio; its data dir and socket are per user, under /var/lib/connaio.
var (
	uid     = strconv.Itoa(os.Getuid())
	Dir     = "/var/lib/connaio/" + uid
	Socket  = Dir + "/core.sock"
	LogPath = Dir + "/core.log"
	unit    = "connaio-core@" + uid + ".service"
)

const helper = "/usr/lib/connaio/connaio-root"

func Installed() bool {
	out, _ := exec.Command("systemctl", "is-enabled", unit).Output()
	return strings.TrimSpace(string(out)) == "enabled"
}

// Install starts the root core and keeps it on across reboots, after one administrator prompt.
func Install(string) error { return asRoot("enable") }

func Uninstall() error { return asRoot("disable") }

// Stale is always false: upgrading the .deb restarts the service on the new binary itself.
func Stale(string) bool { return false }

func asRoot(op string) error {
	if _, err := os.Stat(helper); err != nil {
		return errors.New("增强模式需要用 .deb 安装的 Connaio")
	}
	out, err := exec.Command("pkexec", helper, op, uid).CombinedOutput()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &exit) && (exit.ExitCode() == 126 || exit.ExitCode() == 127): // dismissed, or not authorised
		return errors.New("已取消授权")
	default:
		return fmt.Errorf("授权失败：%s", cmp.Or(strings.TrimSpace(string(out)), err.Error()))
	}
}

// RunAsAdmin runs a shell script through pkexec, for network repairs.
func RunAsAdmin(script, _ string) error {
	out, err := exec.Command("pkexec", "sh", "-c", script).CombinedOutput()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &exit) && (exit.ExitCode() == 126 || exit.ExitCode() == 127):
		return errors.New("已取消授权")
	default:
		return fmt.Errorf("修复失败：%s", cmp.Or(strings.TrimSpace(string(out)), err.Error()))
	}
}

// InBundle says the daemon runs the app's own core; here it runs a copy, replaced by Install when Stale.
const InBundle = false
