package app

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ryanzhangtianran/Connaio/internal/core"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// releaseAPI answers with this app's newest release on GitHub.
const releaseAPI = "https://api.github.com/repos/ryanzhangtianran/Connaio/releases/latest"

// Update is the newest release, and whether it is newer than this app.
type Update struct {
	Current string `json:"current"`
	Latest  string `json:"latest"`
	Newer   bool   `json:"newer"`
	URL     string `json:"url"` // its release page
}

// UpdateProgress is how far an update has got, sent to the window as "update-progress": the
// package's bytes so far and in all while it downloads (Total 0 when the server doesn't say), then
// the install.
type UpdateProgress struct {
	Stage string `json:"stage"` // "download" or "install"
	Done  int64  `json:"done"`
	Total int64  `json:"total"`
}

func init() { application.RegisterEvent[UpdateProgress]("update-progress") }

type release struct {
	Tag    string  `json:"tag_name"`
	URL    string  `json:"html_url"`
	Assets []asset `json:"assets"`
}

type asset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"` // "sha256:<hex>"
}

func (s *SettingsService) CheckUpdate() (Update, error) {
	r, err := s.latestRelease()
	if err != nil {
		return Update{}, err
	}
	latest := strings.TrimPrefix(r.Tag, "v")
	return Update{Current: appVersion, Latest: latest, Newer: newerVersion(latest, appVersion), URL: r.URL}, nil
}

// InstallUpdate downloads the newest release's package for this platform (updateAsset), checks it
// against the digest GitHub lists for it, installs it over this app and starts the new version.
func (s *SettingsService) InstallUpdate() error {
	r, err := s.latestRelease()
	if err != nil {
		return err
	}
	if !newerVersion(strings.TrimPrefix(r.Tag, "v"), appVersion) {
		return errors.New("已是最新版本")
	}
	i := slices.IndexFunc(r.Assets, func(a asset) bool { return strings.HasSuffix(a.Name, updateAsset) })
	if i < 0 {
		return fmt.Errorf("%s 没有本平台的安装包（*%s）", r.Tag, updateAsset)
	}
	file, err := s.downloadVerified(r.Assets[i])
	if err != nil {
		return err
	}
	defer os.RemoveAll(filepath.Dir(file))
	emit("update-progress", UpdateProgress{Stage: "install"})
	if err := installPackage(file); err != nil {
		return err
	}
	notify("info", "已更新到 %s，正在重启", r.Tag)
	return relaunch(restartCommand())
}

// ruleThenDirect routes an update through the core first, where the rules pick its way (GitHub
// through a node where it is slow or blocked), and direct only when the core isn't up.
func ruleThenDirect(c *core.Core, timeout time.Duration) []*http.Client {
	clients := directThenProxy(c, timeout)
	slices.Reverse(clients)
	return clients
}

func (s *SettingsService) latestRelease() (release, error) {
	var errs []error
	for _, c := range ruleThenDirect(s.core.core, 15*time.Second) {
		req, _ := http.NewRequest(http.MethodGet, releaseAPI, nil)
		req.Header.Set("Accept", "application/vnd.github+json")
		resp, err := c.Do(req)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		var r release
		err = json.NewDecoder(resp.Body).Decode(&r)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || err != nil || r.Tag == "" {
			errs = append(errs, fmt.Errorf("%s %v", resp.Status, err))
			continue
		}
		return r, nil
	}
	return release{}, fmt.Errorf("查询新版本失败：%w", errors.Join(errs...))
}

// downloadVerified saves a into a fresh temporary directory, direct or through the core.
func (s *SettingsService) downloadVerified(a asset) (string, error) {
	want, ok := strings.CutPrefix(a.Digest, "sha256:")
	if !ok {
		return "", fmt.Errorf("%s 没有校验值，不安装", a.Name)
	}
	dir, err := os.MkdirTemp("", "connaio-update-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, a.Name)
	var errs []error
	for _, c := range ruleThenDirect(s.core.core, 10*time.Minute) {
		if err := fetchTo(c, a.URL, path, want); err != nil {
			errs = append(errs, err)
			continue
		}
		return path, nil
	}
	os.RemoveAll(dir)
	return "", fmt.Errorf("下载更新失败：%w", errors.Join(errs...))
}

// progress counts the bytes written through it and sends them as "update-progress", at most every
// 200 ms and once at the end.
type progress struct {
	done, total int64
	sent        time.Time
}

func (p *progress) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	if time.Since(p.sent) >= 200*time.Millisecond || p.done == p.total {
		p.sent = time.Now()
		emit("update-progress", UpdateProgress{Stage: "download", Done: p.done, Total: max(p.total, 0)})
	}
	return len(b), nil
}

// fetchTo saves url at path, failing unless its SHA-256 is want (hex).
func fetchTo(c *http.Client, url, path, want string) error {
	resp, err := c.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New(resp.Status)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h, &progress{total: resp.ContentLength}), resp.Body)
	if err := errors.Join(err, f.Close()); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("安装包校验失败：SHA-256 是 %s，应为 %s", got, want)
	}
	return nil
}

// newerVersion reports whether version a is above b, e.g. 1.0.10 above 1.0.9, and a release above
// its test builds: 1.0.0 above 1.0.0-rc.2, which is above 1.0.0-rc.1.
func newerVersion(a, b string) bool {
	an, apre, _ := strings.Cut(a, "-")
	bn, bpre, _ := strings.Cut(b, "-")
	if c := compareDotted(an, bn); c != 0 || apre == bpre {
		return c > 0
	}
	if apre == "" || bpre == "" {
		return apre == ""
	}
	return compareDotted(apre, bpre) > 0
}

// compareDotted compares dotted numbers part by part, a missing or non-numeric part as 0 (rc.2: 0.2).
func compareDotted(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := range max(len(as), len(bs)) {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			return cmp.Compare(x, y)
		}
	}
	return 0
}
