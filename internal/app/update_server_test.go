//go:build server

package app

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// TestUnpack: the tarball's files land in dir with their modes, over the old ones, and a name
// reaching outside dir lands inside it.
func TestUnpack(t *testing.T) {
	dir := t.TempDir()
	tarball := filepath.Join(t.TempDir(), "c.tar.gz")
	f, _ := os.Create(tarball)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, e := range []struct {
		name, body string
		mode       int64
	}{{"connaio", "new", 0o755}, {"connaio.service", "[Unit]", 0o644}, {"../escape", "x", 0o644}} {
		tw.WriteHeader(&tar.Header{Name: e.name, Mode: e.mode, Size: int64(len(e.body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(e.body))
	}
	tw.Close()
	gz.Close()
	f.Close()
	os.WriteFile(filepath.Join(dir, "connaio"), []byte("old"), 0o755)

	if err := unpack(tarball, dir); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"connaio": "new", "connaio.service": "[Unit]", "escape": "x"} {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(b) != want {
			t.Errorf("%s: %q %v, want %q", name, b, err, want)
		}
	}
	if fi, _ := os.Stat(filepath.Join(dir, "connaio")); fi.Mode().Perm() != 0o755 {
		t.Errorf("connaio's mode %v", fi.Mode())
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape")); err == nil {
		t.Error("../escape written outside dir")
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "*.new")); len(m) != 0 {
		t.Errorf("left behind: %v", m)
	}
}
