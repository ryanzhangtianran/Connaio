package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ryanzhangtianran/Connaio/internal/daemon"
	"github.com/ryanzhangtianran/Connaio/internal/sysproxy"
)

// RepairItem is one network check with its fix: what a proxy app leaves behind when it dies
// (a system proxy pointing at nothing, a stray core, a TUN adapter), plus the usual resets.
type RepairItem struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Fix    string `json:"fix"`    // the button; "" for a check with nothing to run
	Found  bool   `json:"found"`  // a problem was found, as opposed to an optional reset
	Reboot bool   `json:"reboot"` // the fix takes effect after a restart
}

// RepairChecks runs every check and reports what it found, problems first.
func (s *ToolService) RepairChecks() []RepairItem {
	items := []RepairItem{s.checkProxy(), s.checkOrphans()}
	items = append(items, platformChecks()...)
	items = append(items, RepairItem{ID: "dns", Title: "DNS 缓存", Detail: "刷新系统 DNS 缓存", Fix: "刷新"})
	var found, rest []RepairItem
	for _, i := range items {
		if i.Found {
			found = append(found, i)
		} else {
			rest = append(rest, i)
		}
	}
	return append(found, rest...)
}

// Repair runs one item's fix and says what it did.
func (s *ToolService) Repair(id string) (string, error) {
	switch id {
	case "proxy":
		if err := sysproxy.Clear(); err != nil {
			return "", err
		}
		return "系统代理已清除", nil
	case "orphan":
		killed, failed := 0, 0
		for _, pid := range orphanCores(s.cs.core.Pid()) {
			if p, err := os.FindProcess(pid); err != nil || p.Kill() != nil {
				failed++
			} else {
				killed++
			}
		}
		if failed > 0 {
			return "", fmt.Errorf("结束了 %d 个，%d 个没能结束", killed, failed)
		}
		return fmt.Sprintf("结束了 %d 个残留内核", killed), nil
	case "dns":
		// a root core flushes it without an administrator prompt (macOS); otherwise the prompt
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if s.cs.core.Daemon() && s.cs.core.Do(ctx, http.MethodPost, "/flush-dns", nil, nil) == nil {
			return "DNS 缓存已刷新", nil
		}
		return flushDNS()
	}
	return platformRepair(id)
}

// checkProxy finds a system proxy that leads nowhere: not ours, or ours while the proxy is off.
func (s *ToolService) checkProxy() RepairItem {
	item := RepairItem{ID: "proxy", Title: "系统代理", Fix: "清除", Detail: "未设置"}
	addr, on := sysproxy.Current()
	if !on {
		return item
	}
	ours := ""
	if port, err := mixedPort(s.cs.core); err == nil {
		ours = "127.0.0.1:" + strconv.Itoa(port)
	}
	who := "其他程序"
	if addr == ours {
		who = "Connaio"
	}
	if listening(addr) { // short: the activity page's card is narrow
		item.Detail = addr + " · " + who
	} else {
		item.Found, item.Detail = true, addr+" · 无人监听"
	}
	return item
}

// listening says something accepts connections at addr right now.
func listening(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// checkOrphans finds this user's connaio-core processes other than this app's child.
func (s *ToolService) checkOrphans() RepairItem {
	item := RepairItem{ID: "orphan", Title: "残留内核", Fix: "结束", Detail: "无"}
	if pids := orphanCores(s.cs.core.Pid()); len(pids) > 0 {
		var list []string
		for _, p := range pids {
			list = append(list, strconv.Itoa(p))
		}
		item.Found, item.Detail = true, fmt.Sprintf("%d 个，PID %s", len(pids), strings.Join(list, ", "))
	}
	return item
}

// runAdmin runs a repair script after the administrator prompt and reports it done.
func runAdmin(script, prompt, done string) (string, error) {
	if err := daemon.RunAsAdmin(script, prompt); err != nil {
		return "", err
	}
	return done, nil
}

var errUnknownRepair = errors.New("没有这个修复项")

// errorsText is a failed command's output as the error, falling back to err.
func errorsText(cmd string, out []byte, err error) error {
	if text := strings.TrimSpace(string(out)); text != "" {
		return fmt.Errorf("%s：%s", cmd, text)
	}
	return fmt.Errorf("%s：%w", cmd, err)
}
