package sysproxy

import (
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
)

// schema is GNOME's proxy settings, which Ubuntu's desktop and most apps (GTK, Chromium, Firefox
// by default) follow.
const schema = "org.gnome.system.proxy"

// ignoreHosts keeps local and LAN traffic off the proxy.
var ignoreHosts = []string{"localhost", "127.0.0.0/8", "::1", "*.local", "169.254.0.0/16", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}

// Set points GNOME's proxies at 127.0.0.1:port, extra bypassed besides local and LAN.
func Set(port int, extra []string) error { return run(setCommands(port, extra)) }

func Clear() error { return run([][]string{{"set", schema, "mode", "'none'"}}) }

// IsSet reports whether the system proxy is manual and its HTTP proxy is 127.0.0.1:port.
func IsSet(port int) bool {
	addr, on := Current()
	return on && addr == "127.0.0.1:"+strconv.Itoa(port)
}

func setCommands(port int, extra []string) [][]string {
	var quoted []string // a GVariant string array; the entries are checked to hold no quote (SystemProxyService.SetBypass)
	for _, h := range append(slices.Clone(ignoreHosts), extra...) {
		quoted = append(quoted, "'"+h+"'")
	}
	cmds := [][]string{{"set", schema, "ignore-hosts", "[" + strings.Join(quoted, ", ") + "]"}}
	for _, kind := range []string{"http", "https", "socks"} {
		cmds = append(cmds, []string{"set", schema + "." + kind, "host", "'127.0.0.1'"}, []string{"set", schema + "." + kind, "port", strconv.Itoa(port)})
	}
	return append(cmds, []string{"set", schema, "mode", "'manual'"}) // last, once the hosts are in place
}

func run(cmds [][]string) error {
	for _, args := range cmds {
		if out, err := exec.Command("gsettings", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("gsettings %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// Current is GNOME's HTTP proxy ("127.0.0.1:7890") and whether the mode is manual.
func Current() (string, bool) {
	get := func(schema, key string) string {
		out, _ := exec.Command("gsettings", "get", schema, key).Output()
		return strings.Trim(strings.TrimSpace(string(out)), "'")
	}
	return get(schema+".http", "host") + ":" + get(schema+".http", "port"), get(schema, "mode") == "manual"
}
