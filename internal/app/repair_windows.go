package app

import (
	"encoding/csv"
	"os"
	"strconv"
	"strings"

	"github.com/ryanzhangtianran/Connaio/internal/run"
)

// orphanCores lists connaio-core.exe processes other than ours, from tasklist's CSV.
func orphanCores(own int) []int {
	out, _ := run.Command("tasklist", "/FI", "IMAGENAME eq connaio-core.exe", "/FI", "USERNAME eq "+os.Getenv("USERNAME"), "/FO", "CSV", "/NH").Output() // this user's only: the service is not an orphan
	rows, _ := csv.NewReader(strings.NewReader(string(out))).ReadAll()
	var pids []int
	for _, r := range rows {
		if len(r) > 1 {
			if pid, err := strconv.Atoi(r[1]); err == nil && pid != own {
				pids = append(pids, pid)
			}
		}
	}
	return pids
}

func flushDNS() (string, error) {
	if out, err := run.Command("ipconfig", "/flushdns").CombinedOutput(); err != nil {
		return "", errorsText("ipconfig /flushdns", out, err)
	}
	return "DNS 缓存已刷新", nil
}

// leftoverTUN says whether the Meta adapter is there with no core of any user running to own it:
// the service's core runs as SYSTEM, and removing its adapter from under it breaks it.
func leftoverTUN() bool {
	out, err := powershell("Get-NetAdapter -Name Meta -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Name")
	if err != nil || strings.TrimSpace(out) != "Meta" {
		return false
	}
	cores, _ := run.Command("tasklist", "/FI", "IMAGENAME eq connaio-core.exe", "/FO", "CSV", "/NH").Output() // every user's
	return !strings.Contains(strings.ToLower(string(cores)), "connaio-core.exe")
}

// platformChecks: the wintun adapter a dead core left, and the resets Windows users reach for.
func platformChecks() []RepairItem {
	tun := RepairItem{ID: "tun", Title: "残留 TUN", Detail: "无", Fix: "移除"}
	if leftoverTUN() {
		tun.Found, tun.Detail = true, "适配器 Meta 无人使用"
	}
	return []RepairItem{
		tun,
		{ID: "winsock", Title: "Winsock", Detail: "清除代理、VPN、加速器等软件残留的网络组件；所有程序都连不上网时用", Fix: "重置", Reboot: true},
		{ID: "ipstack", Title: "TCP/IP", Detail: "把网卡的 IP、路由设置恢复默认；拿不到 IP 或网络设置被改乱时用", Fix: "重置", Reboot: true},
	}
}

func platformRepair(id string) (string, error) {
	switch id {
	case "tun":
		if !leftoverTUN() { // checked again: a core may have started since the list was shown
			return "TUN 适配器正在使用，未移除", nil
		}
		return runAdmin("netsh interface set interface \"Meta\" admin=disable\r\npnputil /remove-device /deviceid \"Meta\" >nul 2>&1\r\nexit /b 0\r\n", "", "TUN 适配器已移除")
	case "winsock":
		return runAdmin("netsh winsock reset\r\n", "", "Winsock 已重置，重启后生效")
	case "ipstack":
		return runAdmin("netsh int ip reset\r\nnetsh int ipv6 reset\r\n", "", "TCP/IP 已重置，重启后生效")
	}
	return "", errUnknownRepair
}
