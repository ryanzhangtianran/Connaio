package app

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// A backup restores the profiles and settings it was made from, and the profile it lacks is gone;
// a zip with any other entry is refused before anything is written.
func TestBackup(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	write := func(dir, name, text string) {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(src, "profiles/index.json", `{"current":"a"}`)
	write(src, "profiles/a.yaml", "proxies: []")
	write(src, "settings.json", `{"systemProxy":true}`)
	write(src, "cache.db", "not backed up")
	write(dst, "profiles/index.json", `{}`)
	write(dst, "profiles/old.yaml", "gone after the restore")

	var buf bytes.Buffer
	if err := writeBackup(src, &buf); err != nil {
		t.Fatal(err)
	}
	if err := restoreBackup(dst, buf.Bytes()); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"profiles/index.json": `{"current":"a"}`, "profiles/a.yaml": "proxies: []", "settings.json": `{"systemProxy":true}`} {
		if b, _ := os.ReadFile(filepath.Join(dst, name)); string(b) != want {
			t.Errorf("%s = %q", name, b)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "profiles/old.yaml")); err == nil {
		t.Error("a profile the backup lacks lingers")
	}

	for _, name := range []string{"../evil", "profiles/../../evil", "profiles/sub/x", "cache.db"} {
		var z bytes.Buffer
		w := zip.NewWriter(&z)
		for _, n := range []string{"profiles/index.json", name} {
			f, _ := w.Create(n)
			f.Write([]byte("x"))
		}
		w.Close()
		if err := restoreBackup(dst, z.Bytes()); err == nil {
			t.Errorf("a backup holding %q restored", name)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "profiles/index.json")); string(b) != `{"current":"a"}` {
		t.Errorf("a refused backup changed the data: %q", b)
	}
}
