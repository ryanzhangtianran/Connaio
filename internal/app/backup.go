package app

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ryanzhangtianran/Connaio/internal/profile"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// A backup is a zip of what the user made: profiles/ (the subscriptions and local configs, the
// override, their index) and settings.json (VPNs with their passwords, sites, switches). The
// generated config, caches, logs, traffic counts and downloaded databases come back on their own.

// backupName says whether a zip entry is one a backup holds: settings.json, or a file right in profiles/.
func backupName(name string) bool {
	if name == "settings.json" {
		return true
	}
	rest, ok := strings.CutPrefix(name, "profiles/")
	return ok && rest != "" && rest != "." && rest != ".." && !strings.ContainsAny(rest, `/\`)
}

// writeBackup zips dir's profiles/ and settings.json into w.
func writeBackup(dir string, w io.Writer) error {
	z := zip.NewWriter(w)
	names := []string{"settings.json"}
	entries, err := os.ReadDir(filepath.Join(dir, "profiles"))
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Type().IsRegular() {
			names = append(names, "profiles/"+e.Name())
		}
	}
	for _, name := range names {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		f, err := z.Create(name)
		if err != nil {
			return err
		}
		if _, err := f.Write(b); err != nil {
			return err
		}
	}
	return z.Close()
}

// maxBackupFile caps each entry read from a backup: a profile is kilobytes, a crafted zip unbounded.
const maxBackupFile = 32 << 20

// restoreBackup puts a backup's files in dir: it checks every entry before writing any, then swaps
// profiles/ whole (the old one goes, so a profile the backup lacks doesn't linger) and settings.json.
func restoreBackup(dir string, data []byte) error {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return errors.New("不是 Connaio 的备份文件")
	}
	files := map[string][]byte{}
	for _, f := range z.File {
		if !backupName(f.Name) {
			return errors.New("不是 Connaio 的备份文件")
		}
		r, err := f.Open()
		if err != nil {
			return err
		}
		b, err := io.ReadAll(io.LimitReader(r, maxBackupFile+1))
		r.Close()
		if err != nil {
			return err
		}
		if len(b) > maxBackupFile {
			return fmt.Errorf("备份中的 %s 过大", f.Name)
		}
		files[f.Name] = b
	}
	if files["profiles/index.json"] == nil {
		return errors.New("不是 Connaio 的备份文件")
	}
	staged := filepath.Join(dir, "profiles.restore")
	_ = os.RemoveAll(staged)
	if err := os.MkdirAll(staged, 0o755); err != nil {
		return err
	}
	for name, b := range files {
		if rest, ok := strings.CutPrefix(name, "profiles/"); ok {
			if err := os.WriteFile(filepath.Join(staged, rest), b, 0o600); err != nil {
				return err
			}
		}
	}
	live, old := filepath.Join(dir, "profiles"), filepath.Join(dir, "profiles.old")
	_ = os.RemoveAll(old)
	if err := os.Rename(live, old); err != nil {
		return err
	}
	if err := os.Rename(staged, live); err != nil {
		_ = os.Rename(old, live) // as it was
		return err
	}
	_ = os.RemoveAll(old)
	if b, ok := files["settings.json"]; ok {
		return profile.WriteAtomic(filepath.Join(dir, "settings.json"), b, 0o644)
	}
	return nil
}

// ExportBackup saves a backup where the user picks.
func (s *SettingsService) ExportBackup() error {
	path, err := application.Get().Dialog.SaveFile().
		SetFilename("Connaio-"+time.Now().Format("20060102")+".zip").
		AddFilter("Connaio 备份", "*.zip").
		PromptForSingleSelection()
	if err != nil || path == "" {
		return err
	}
	var buf bytes.Buffer
	if err := writeBackup(s.core.core.Dir, &buf); err != nil {
		return err
	}
	return profile.WriteAtomic(path, buf.Bytes(), 0o600) // it holds passwords
}

// ImportBackup restores a backup the user picks over the current data, and restarts Connaio on it.
func (s *SettingsService) ImportBackup() error {
	path, err := application.Get().Dialog.OpenFile().
		SetTitle("恢复备份").
		CanChooseFiles(true).
		AddFilter("Connaio 备份", "*.zip").
		PromptForSingleSelection()
	if err != nil || path == "" {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := restoreBackup(s.core.core.Dir, data); err != nil {
		return err
	}
	s.settings.reload() // or the settings this run still holds would be saved over the restored ones as it quits
	return restartApp()
}
