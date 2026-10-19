package sysproxy

import (
	"slices"
	"testing"
)

func TestCommands(t *testing.T) {
	set := setCommands([]string{"Wi-Fi"}, 7890)
	if !slices.Equal(set[0], []string{"-setwebproxy", "Wi-Fi", "127.0.0.1", "7890"}) ||
		!slices.Equal(set[2], []string{"-setsocksfirewallproxy", "Wi-Fi", "127.0.0.1", "7890"}) ||
		!slices.Equal(set[3], append([]string{"-setproxybypassdomains", "Wi-Fi"}, bypass...)) {
		t.Fatalf("set commands: %v", set)
	}
	if n := len(clearCommands([]string{"Wi-Fi", "Ethernet"})); n != 6 {
		t.Fatalf("clear commands: %d", n)
	}
}

func TestPointsAt(t *testing.T) {
	out := "Enabled: Yes\nServer: 127.0.0.1\nPort: 7890\nAuthenticated Proxy Enabled: 0\n"
	if !pointsAt(out, 7890) || pointsAt(out, 7898) {
		t.Fatal("port match")
	}
	if pointsAt("Enabled: No\nServer: 127.0.0.1\nPort: 7890\n", 7890) {
		t.Fatal("disabled proxy is not ours")
	}
	if pointsAt("Enabled: Yes\nServer: 127.0.0.1\nPort: 7892\n", 7890) {
		t.Fatal("another client's proxy is not ours")
	}
}
