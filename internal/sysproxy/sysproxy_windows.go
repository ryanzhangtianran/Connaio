package sysproxy

import (
	"fmt"
	"strconv"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// The system proxy is WinINET's, in the registry; most apps (Edge, Chrome, Firefox by default,
// .NET) follow it. A WinINET notification makes running apps pick the change up.
const (
	key = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	// bypass keeps local and LAN traffic off the proxy; <local> is every host without a dot.
	bypass = "localhost;127.*;10.*;172.16.*;172.17.*;172.18.*;172.19.*;172.20.*;172.21.*;172.22.*;172.23.*;172.24.*;172.25.*;172.26.*;172.27.*;172.28.*;172.29.*;172.30.*;172.31.*;192.168.*;169.254.*;*.local;<local>"

	internetOptionSettingsChanged = 39
	internetOptionRefresh         = 37
)

func Set(port int) error {
	return write(func(k registry.Key) error {
		if err := k.SetDWordValue("ProxyEnable", 1); err != nil {
			return err
		}
		if err := k.SetStringValue("ProxyServer", server(port)); err != nil {
			return err
		}
		return k.SetStringValue("ProxyOverride", bypass)
	})
}

func Clear() error {
	return write(func(k registry.Key) error { return k.SetDWordValue("ProxyEnable", 0) })
}

// IsSet reports whether the proxy is on and points at 127.0.0.1:port.
func IsSet(port int) (bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, key, registry.QUERY_VALUE)
	if err != nil {
		return false, err
	}
	defer k.Close()
	enabled, _, err := k.GetIntegerValue("ProxyEnable")
	if err != nil {
		return false, nil // never set
	}
	got, _, _ := k.GetStringValue("ProxyServer")
	return enabled == 1 && got == server(port), nil
}

func server(port int) string { return "127.0.0.1:" + strconv.Itoa(port) }

func write(change func(k registry.Key) error) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, key, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("打开代理设置失败: %w", err)
	}
	defer k.Close()
	if err := change(k); err != nil {
		return fmt.Errorf("写入代理设置失败: %w", err)
	}
	refresh()
	return nil
}

// refresh tells WinINET the settings changed, so apps already running follow them.
func refresh() {
	setOption := windows.NewLazySystemDLL("wininet.dll").NewProc("InternetSetOptionW")
	for _, option := range []uintptr{internetOptionSettingsChanged, internetOptionRefresh} {
		_, _, _ = setOption.Call(0, option, 0, 0)
	}
}

// Current is the Internet Settings proxy ("127.0.0.1:7890") and whether it is enabled.
func Current() (string, bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, key, registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer k.Close()
	enabled, _, _ := k.GetIntegerValue("ProxyEnable")
	got, _, _ := k.GetStringValue("ProxyServer")
	return got, enabled == 1
}
