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

// Restart starts the root core again, for one that runs but doesn't answer: enable restarts it.
func Restart(bin string) error { return Install(bin) }

func Uninstall() error { return asRoot("disable") }

// Stale is always false: upgrading the .deb restarts the service on the new binary itself.
func Stale(string) bool { return false }

func asRoot(op string) error {
	if _, err := os.Stat(helper); err != nil {
		return errors.New("增强模式需要用 .deb 安装的 Connaio")
	}
	return pkexec("授权失败", helper, op, uid)
}

// RunAsAdmin runs a shell script through pkexec, for network repairs.
func RunAsAdmin(script, _ string) error { return pkexec("修复失败", "sh", "-c", script) }

// pkexec runs args as root through pkexec; failed heads the error when it fails other than by the
// prompt being dismissed.
func pkexec(failed string, args ...string) error {
	out, err := exec.Command("pkexec", args...).CombinedOutput()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &exit) && (exit.ExitCode() == 126 || exit.ExitCode() == 127): // dismissed, or not authorised
		return errors.New("已取消授权")
	default:
		return fmt.Errorf("%s：%s", failed, cmp.Or(strings.TrimSpace(string(out)), err.Error()))
	}
}
