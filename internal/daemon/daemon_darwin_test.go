package daemon

import (
	"encoding/base64"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// TestBundledPlist checks build/darwin/daemon.plist, which the app bundle carries, against what the
// GUI expects of the daemon it starts.
func TestBundledPlist(t *testing.T) {
	p := "../../build/darwin/daemon.plist"
	if out, err := exec.Command("plutil", "-lint", p).CombinedOutput(); err != nil {
		t.Fatalf("plist invalid: %s", out)
	}
	get := func(key string) string {
		out, err := exec.Command("plutil", "-extract", key, "raw", p).Output()
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		return strings.TrimSpace(string(out))
	}
	args := map[string]string{}
	for i := 1; i < 7; i += 2 { // after argv[0], flag and value pairs
		args[get("ProgramArguments."+strconv.Itoa(i))] = get("ProgramArguments." + strconv.Itoa(i+1))
	}
	boot, _ := base64.StdEncoding.DecodeString(args["-config"])
	for name, c := range map[string][2]string{
		"Label": {get("Label"), Label}, "-d": {args["-d"], Dir}, "-ext-ctl-unix": {args["-ext-ctl-unix"], Socket},
		"-config": {string(boot), BootConfig}, "StandardOutPath": {get("StandardOutPath"), LogPath},
		"BundleProgram": {get("BundleProgram"), "Contents/MacOS/connaio-core"},
	} {
		if c[0] != c[1] {
			t.Errorf("%s: %q, want %q", name, c[0], c[1])
		}
	}
}

func TestScripts(t *testing.T) {
	script := legacyRemoval()
	if out, err := exec.Command("sh", "-n", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("removal script syntax: %s", out)
	}
	// AppleScript string literal round trip, without the admin prompt
	out, err := exec.Command("osascript", "-e", "return "+appleQuote(script)).Output()
	if err != nil || strings.TrimSpace(string(out)) != strings.TrimSpace(script) {
		t.Fatalf("apple quoting changed the script: %v\n%s", err, out)
	}
}
