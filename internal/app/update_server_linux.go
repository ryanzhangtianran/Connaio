//go:build server

package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

const updateAsset = "-linux-" + runtime.GOARCH + ".tar.gz"

// unit is where install.sh puts the service's unit file.
const unit = "/etc/systemd/system/connaio.service"

// installPackage unpacks the release's tarball over this one's directory, as install.sh does, and
// the service's unit file too, when install.sh installed it.
func installPackage(tarball string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dir := filepath.Dir(exe)
	if err := unpack(tarball, dir); err != nil {
		return fmt.Errorf("安装更新失败（需以 root 运行服务）：%w", err)
	}
	if _, err := os.Stat(unit); err == nil {
		src, err := os.Open(filepath.Join(dir, "connaio.service"))
		if err != nil {
			return err
		}
		defer src.Close()
		if err := writeRenamed(unit, src, 0o644); err != nil {
			return fmt.Errorf("安装更新失败：%w", err)
		}
		if out, err := exec.Command("systemctl", "daemon-reload").CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl daemon-reload：%s", out)
		}
	}
	return nil
}

// restartCommand is this version started again with this run's flags, when not the service
// (relaunch restarts the service through systemd).
func restartCommand() []string {
	exe, _ := os.Executable()
	return append([]string{exe}, os.Args[1:]...)
}
