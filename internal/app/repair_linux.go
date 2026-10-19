package app

import (
	"os"

	"github.com/ryanzhangtianran/Connaio/internal/run"
)

func flushDNS() (string, error) {
	if out, err := run.Command("resolvectl", "flush-caches").CombinedOutput(); err != nil {
		return "", errorsText("resolvectl flush-caches", out, err)
	}
	return "DNS 缓存已刷新", nil
}

// leftoverTUN says whether the Meta TUN interface is there with no core of any user running to own
// it: the root daemon's core runs as root, and deleting its interface from under it leaves it
// spinning on a dead descriptor.
func leftoverTUN() bool {
	if _, err := os.Stat("/sys/class/net/Meta"); err != nil {
		return false
	}
	return run.Command("pgrep", "-x", "connaio-core").Run() != nil // exit 1: none found
}

// platformChecks looks for the Meta TUN interface a dead core left behind.
func platformChecks() []RepairItem {
	item := RepairItem{ID: "tun", Title: "残留 TUN", Detail: "无", Fix: "移除"}
	if leftoverTUN() {
		item.Found, item.Detail = true, "接口 Meta 无人使用"
	}
	return []RepairItem{item}
}

func platformRepair(id string) (string, error) {
	if id == "tun" {
		if !leftoverTUN() { // checked again: a core may have started since the list was shown
			return "TUN 接口正在使用，未移除", nil
		}
		return runAdmin("ip link delete Meta", "Connaio 要移除残留的 TUN 接口。", "TUN 接口已移除")
	}
	return "", errUnknownRepair
}
