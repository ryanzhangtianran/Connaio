//go:build linux && !server

package app

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// updateAsset ends the name of this platform's release package.
const updateAsset = "-linux-" + runtime.GOARCH + ".deb"

// installPackage upgrades the installed .deb through apt, after the system's administrator prompt.
func installPackage(deb string) error {
	out, err := exec.Command("pkexec", "apt-get", "install", "-y", "--allow-downgrades", deb).CombinedOutput()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &exit) && (exit.ExitCode() == 126 || exit.ExitCode() == 127): // dismissed, or not authorised
		return errors.New("已取消授权")
	default:
		return fmt.Errorf("安装更新失败：%s", cmp.Or(strings.TrimSpace(string(out)), err.Error()))
	}
}

func restartCommand() []string {
	exe, _ := os.Executable() // /usr/lib/connaio/connaio, now the new version
	return []string{exe}
}
