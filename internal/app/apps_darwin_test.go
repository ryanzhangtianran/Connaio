package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoginItemFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "login.plist")
	os.WriteFile(p, []byte(loginItemFile("/Applications/Connaio & Co.app/Contents/MacOS/connaio")), 0o644)
	if out, err := exec.Command("plutil", "-lint", p).CombinedOutput(); err != nil {
		t.Fatalf("invalid plist: %s", out)
	}
	out, _ := exec.Command("plutil", "-extract", "ProgramArguments.0", "raw", p).Output()
	if string(out) != "/Applications/Connaio & Co.app/Contents/MacOS/connaio\n" {
		t.Fatalf("path round trip: %q", out)
	}
}

func TestParseApps(t *testing.T) {
	apps := parseApps([]string{
		"/Applications/Ghostty.app/Contents/MacOS/ghostty",
		"/Applications/Lark.app/Contents/MacOS/Feishu",
		"/Applications/Lark.app/Contents/Frameworks/Lark Framework.framework/Versions/1/Helpers/Lark Helper.app/Contents/MacOS/Lark Helper",
		"/Applications/Surge.app/Contents/Applications/Surge Dashboard.app/Contents/MacOS/Surge Dashboard",
		"/Applications/Ghostty.app/Contents/MacOS/ghostty",
		"/usr/sbin/mDNSResponder",
		"/System/Library/CoreServices/Dock.app/Contents/MacOS/Dock",
		"/Library/Apple/System/Library/CoreServices/MRT.app/Contents/MacOS/MRT",
	})
	if len(apps) != 2 || apps[0].Name != "Ghostty" || apps[0].Exec != "ghostty" || apps[1].Name != "Lark" || apps[1].Exec != "Feishu" {
		t.Fatalf("apps %+v", apps)
	}
}

// The table as the kernel gives it: this test's own process, under its parent, by its full path.
func TestProcTable(t *testing.T) {
	exe, _ := os.Executable()
	exe, _ = filepath.EvalSymlinks(exe) // the kernel's path: /private/var where the temp dir says /var
	p, ok := procTable()[os.Getpid()]
	if !ok || p.ppid != os.Getppid() || p.comm != exe {
		t.Fatalf("this process: %+v, want parent %d and %s", p, os.Getppid(), exe)
	}
}

func TestParentIn(t *testing.T) {
	table := map[int]proc{}
	for pid, pc := range map[int]struct {
		ppid int
		comm string
	}{
		100: {1, "/Applications/Ghostty.app/Contents/MacOS/ghostty"},
		101: {100, "/usr/bin/login"},
		102: {101, "/bin/zsh"},
		103: {102, "/Users/me/.local/bin/claude"},
		200: {1, "/usr/sbin/mDNSResponder"},
		300: {1, "/System/Library/CoreServices/Dock.app/Contents/MacOS/Dock"},
		301: {300, "/usr/bin/ssh"},
	} {
		p := proc{ppid: pc.ppid, comm: pc.comm}
		_, p.app = appOfPath(p.comm)
		table[pid] = p
	}
	if p, _ := parentIn(table, "claude"); p != "/Applications/Ghostty.app/Contents/MacOS/ghostty" {
		t.Fatalf("claude's parent %q", p)
	}
	if p, found := parentIn(table, "mDNSResponder"); p != "" || !found {
		t.Fatalf("mDNSResponder's parent %q, found %v", p, found)
	}
	if p, _ := parentIn(table, "ssh"); p != "" { // under Dock, which nobody routes
		t.Fatalf("ssh's parent %q", p)
	}
	if _, found := parentIn(table, "curl"); found {
		t.Fatal("curl isn't running")
	}
}

func TestAppExecutable(t *testing.T) {
	c, err := appExecutable("/System/Applications/Calculator.app")
	if err != nil || c.Exec != "Calculator" || c.Name != "Calculator" {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := appExecutable(t.TempDir()); err == nil {
		t.Fatal("a folder that isn't an app must be rejected")
	}
}

func TestAppIcon(t *testing.T) {
	for _, p := range []string{"/System/Applications/Calculator.app", "/bin/ls"} {
		if u := (&SettingsService{}).AppIcon(p); !strings.HasPrefix(u, "data:image/png;base64,") || len(u) < 500 {
			t.Fatalf("%s: %.60q", p, u)
		}
	}
}
