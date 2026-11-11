package sysproxy

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestCommands(t *testing.T) {
	set := setCommands([]string{"Wi-Fi"}, 7890, []string{"*.corp.example"})
	if !slices.Equal(set[0], []string{"-setwebproxy", "Wi-Fi", "127.0.0.1", "7890"}) ||
		!slices.Equal(set[2], []string{"-setsocksfirewallproxy", "Wi-Fi", "127.0.0.1", "7890"}) ||
		!slices.Equal(set[3], []string{"-setproxybypassdomains", "Wi-Fi", "*.corp.example"}) {
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

// Changes fires when preferences.plist is written in place and when it is replaced (as configd
// saves it), and keeps firing for the replacement.
func TestChanges(t *testing.T) {
	prefsDir = t.TempDir()
	plist := filepath.Join(prefsDir, "preferences.plist")
	if err := os.WriteFile(plist, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	changed := Changes(ctx)
	if changed == nil {
		t.Fatal("not watched")
	}
	expect := func(what string, change func() error) {
		t.Helper()
		if err := change(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-changed:
		case <-time.After(2 * time.Second):
			t.Fatalf("no signal after %s", what)
		}
		for len(changed) > 0 { // a burst may leave one more behind
			<-changed
		}
	}
	expect("writing in place", func() error { return os.WriteFile(plist, []byte("b"), 0o644) })
	expect("replacing", func() error {
		if err := os.WriteFile(plist+"-new", []byte("c"), 0o644); err != nil {
			return err
		}
		return os.Rename(plist+"-new", plist)
	})
	time.Sleep(100 * time.Millisecond) // let the replacement's own events settle
	for len(changed) > 0 {
		<-changed
	}
	expect("writing the replacement in place", func() error { return os.WriteFile(plist, []byte("d"), 0o644) })
}
