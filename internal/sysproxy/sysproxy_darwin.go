package sysproxy

import (
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
)

// bypass keeps local and LAN traffic off the proxy.
var bypass = []string{"localhost", "127.0.0.1", "::1", "*.local", "169.254.0.0/16", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}

// Set points every network service's proxies at 127.0.0.1:port, extra bypassed besides local and LAN.
func Set(port int, extra []string) error {
	services, err := services()
	if err != nil {
		return err
	}
	return run(setCommands(services, port, extra))
}

// Clear switches the proxies off.
// ponytail: turns them off instead of restoring settings the user had before Connaio.
func Clear() error {
	services, err := services()
	if err != nil {
		return err
	}
	return run(clearCommands(services))
}

// IsSet reports whether every enabled service still proxies to 127.0.0.1:port (another app may
// have replaced it).
func IsSet(port int) bool {
	services, err := services()
	if err != nil {
		return false
	}
	for _, s := range services {
		for _, get := range []string{"-getwebproxy", "-getsecurewebproxy"} {
			out, err := exec.Command("networksetup", get, s).Output()
			if err != nil || !pointsAt(string(out), port) {
				return false
			}
		}
	}
	return len(services) > 0
}

// pointsAt says whether `networksetup -getwebproxy` output is an enabled proxy at 127.0.0.1:port.
func pointsAt(out string, port int) bool {
	addr, on := parseProxy(out)
	return on && addr == "127.0.0.1:"+strconv.Itoa(port)
}

// parseProxy reads `networksetup -getwebproxy` output ("Enabled: Yes", "Server: ...", "Port: ...")
// as the proxy's server:port and whether it is enabled.
func parseProxy(out string) (string, bool) {
	fields := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok {
			fields[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return fields["Server"] + ":" + fields["Port"], fields["Enabled"] == "Yes"
}

// services lists enabled network services; the first line is a notice and "*" marks disabled ones.
func services() ([]string, error) {
	out, err := exec.Command("networksetup", "-listallnetworkservices").Output()
	if err != nil {
		return nil, fmt.Errorf("networksetup -listallnetworkservices: %w", err)
	}
	var list []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n")[1:] {
		if line != "" && !strings.HasPrefix(line, "*") {
			list = append(list, line)
		}
	}
	return list, nil
}

func setCommands(services []string, port int, extra []string) [][]string {
	p := strconv.Itoa(port)
	var cmds [][]string
	for _, s := range services {
		cmds = append(cmds,
			[]string{"-setwebproxy", s, "127.0.0.1", p},
			[]string{"-setsecurewebproxy", s, "127.0.0.1", p},
			[]string{"-setsocksfirewallproxy", s, "127.0.0.1", p},
			slices.Concat([]string{"-setproxybypassdomains", s}, bypass, extra),
		)
	}
	return cmds
}

func clearCommands(services []string) [][]string {
	var cmds [][]string
	for _, s := range services {
		cmds = append(cmds,
			[]string{"-setwebproxystate", s, "off"},
			[]string{"-setsecurewebproxystate", s, "off"},
			[]string{"-setsocksfirewallproxystate", s, "off"},
		)
	}
	return cmds
}

func run(cmds [][]string) error {
	for _, args := range cmds {
		if out, err := exec.Command("networksetup", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("networksetup %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// Current is the HTTP proxy the first enabled service points at ("127.0.0.1:7890") and whether
// it is on; "" when there is none.
func Current() (string, bool) {
	services, err := services()
	if err != nil || len(services) == 0 {
		return "", false
	}
	out, err := exec.Command("networksetup", "-getwebproxy", services[0]).Output()
	if err != nil {
		return "", false
	}
	return parseProxy(string(out))
}
