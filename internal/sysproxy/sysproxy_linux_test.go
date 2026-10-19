package sysproxy

import (
	"slices"
	"testing"
)

func TestSetCommands(t *testing.T) {
	cmds := setCommands(7890)
	if !slices.Equal(cmds[1], []string{"set", schema + ".http", "host", "'127.0.0.1'"}) || !slices.Equal(cmds[6], []string{"set", schema + ".socks", "port", "7890"}) {
		t.Fatalf("%q", cmds)
	}
	if last := cmds[len(cmds)-1]; !slices.Equal(last, []string{"set", schema, "mode", "'manual'"}) {
		t.Fatalf("mode must be switched on last, got %q", last)
	}
}
