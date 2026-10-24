// Package profile stores subscriptions and the user's override, and merges them into the core's config.
package profile

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.yaml.in/yaml/v3"
)

// UserAgent makes subscription services answer with Clash/mihomo YAML.
const UserAgent = "clash.meta/1.10.0 mihomo/1.10.0 Connaio/1.0"

type Usage struct {
	Upload   int64 `json:"upload"`
	Download int64 `json:"download"`
	Total    int64 `json:"total"`
	Expire   int64 `json:"expire"` // unix seconds, 0 = never
}

type Profile struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url,omitempty"` // empty for a local profile
	UpdatedAt time.Time `json:"updatedAt"`
	Interval  int       `json:"interval"` // auto-update hours, 0 = never
	// IntervalSet: the user chose Interval, which a subscription's own header then no longer sets
	IntervalSet bool      `json:"intervalSet,omitempty"`
	Usage       *Usage    `json:"usage,omitempty"`
	LastTried   time.Time `json:"lastTried,omitempty"` // last failed auto-update, so it waits a full interval before the next
}

type Store struct {
	dir string
	mu  sync.Mutex
	// the last merged config Value served, and the files it came from (see Value)
	valueMu    sync.Mutex
	valueKey   string
	valueCache any
	index      struct {
		Current  string    `json:"current"`
		Profiles []Profile `json:"profiles"`
	}
}

// Open loads the store kept in dir/profiles.
func Open(dir string) (*Store, error) {
	s := &Store{dir: filepath.Join(dir, "profiles")}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(s.path("index.json"))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.index); err != nil {
		// a torn file must not keep the app from launching; the profiles themselves are still on disk
		log.Printf("profiles/index.json unreadable (%v), moving it aside", err)
		_ = os.Rename(s.path("index.json"), s.path("index.json.bad"))
		s.index.Current, s.index.Profiles = "", nil
	}
	return s, nil
}

// WriteAtomic writes data to a temporary file next to path and renames it in, so a crash mid-write
// leaves the old file rather than a torn one.
func WriteAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (s *Store) path(name string) string { return filepath.Join(s.dir, name) }

func (s *Store) save() error {
	b, err := json.MarshalIndent(s.index, "", "  ")
	if err != nil {
		return err
	}
	return WriteAtomic(s.path("index.json"), b, 0o644)
}

func (s *Store) List() (string, []Profile) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.index.Current, slices.Clone(s.index.Profiles)
}

// AddRemote downloads a subscription and stores it; the first profile becomes current.
func (s *Store) AddRemote(name, url string, clients ...*http.Client) (Profile, error) {
	content, usage, interval, err := download(url, clients)
	if err != nil {
		return Profile{}, err
	}
	if name == "" {
		name = "订阅"
	}
	return s.add(Profile{Name: name, URL: url, Usage: usage, Interval: interval}, content)
}

// AddLocal stores a Clash config the user provides.
func (s *Store) AddLocal(name string, content []byte) (Profile, error) {
	if err := validate(content); err != nil {
		return Profile{}, err
	}
	return s.add(Profile{Name: name}, content)
}

func (s *Store) add(p Profile, content []byte) (Profile, error) {
	id := make([]byte, 6)
	_, _ = rand.Read(id)
	p.ID, p.UpdatedAt = hex.EncodeToString(id), time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := WriteAtomic(s.path(p.ID+".yaml"), formatted(content), 0o600); err != nil {
		return Profile{}, err
	}
	s.index.Profiles = append(s.index.Profiles, p)
	if s.index.Current == "" {
		s.index.Current = p.ID
	}
	return p, s.save()
}

// Update re-downloads a remote profile.
func (s *Store) Update(id string, clients ...*http.Client) (Profile, error) {
	s.mu.Lock()
	i := slices.IndexFunc(s.index.Profiles, func(p Profile) bool { return p.ID == id })
	if i < 0 || s.index.Profiles[i].URL == "" {
		s.mu.Unlock()
		return Profile{}, fmt.Errorf("profile %s is not a subscription", id)
	}
	link := s.index.Profiles[i].URL
	s.mu.Unlock()

	content, usage, interval, err := download(link, clients)
	s.mu.Lock()
	defer s.mu.Unlock()
	if i = slices.IndexFunc(s.index.Profiles, func(p Profile) bool { return p.ID == id }); i < 0 {
		return Profile{}, fmt.Errorf("profile %s was removed", id)
	}
	p := &s.index.Profiles[i]
	if err != nil {
		p.LastTried = time.Now() // Due waits a full interval before trying again
		_ = s.save()
		return Profile{}, err
	}
	if err := WriteAtomic(s.path(id+".yaml"), formatted(content), 0o600); err != nil {
		return Profile{}, err
	}
	p.UpdatedAt, p.Usage, p.LastTried = time.Now(), usage, time.Time{}
	if interval > 0 && !p.IntervalSet {
		p.Interval = interval
	}
	return *p, s.save()
}

func (s *Store) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.index.Profiles = slices.DeleteFunc(s.index.Profiles, func(p Profile) bool { return p.ID == id })
	if s.index.Current == id {
		s.index.Current = ""
		if len(s.index.Profiles) > 0 {
			s.index.Current = s.index.Profiles[0].ID
		}
	}
	_ = os.Remove(s.path(id + ".yaml"))
	return s.save()
}

// Rename sets a profile's name, as listed and in the menu bar; an update keeps it.
func (s *Store) Rename(id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("名称不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.index.Profiles, func(p Profile) bool { return p.ID == id })
	if i < 0 {
		return fmt.Errorf("profile %s not found", id)
	}
	s.index.Profiles[i].Name = name
	return s.save()
}

// Edit sets a profile's name and, for a subscription, its link and auto-update hours (0: never), which
// its provider's header no longer overrides; the new link is fetched at the next update.
func (s *Store) Edit(id, name, link string, interval int) error {
	name, link = strings.TrimSpace(name), strings.TrimSpace(link)
	if name == "" {
		return errors.New("名称不能为空")
	}
	if interval < 0 {
		return errors.New("更新间隔不能为负")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.index.Profiles, func(p Profile) bool { return p.ID == id })
	if i < 0 {
		return fmt.Errorf("profile %s not found", id)
	}
	p := &s.index.Profiles[i]
	if p.URL != "" {
		if u, err := url.Parse(link); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("订阅链接须为 http 或 https 地址")
		}
		p.URL, p.Interval, p.IntervalSet = link, interval, true
	}
	p.Name = name
	return s.save()
}

// Content is a profile's YAML as stored.
func (s *Store) Content(id string) (string, error) {
	if !s.has(id) {
		return "", fmt.Errorf("profile %s not found", id)
	}
	b, err := os.ReadFile(s.path(id + ".yaml"))
	return string(b), err
}

// SetContent replaces a profile's YAML after the same check a new local profile gets.
func (s *Store) SetContent(id, content string) error {
	if !s.has(id) {
		return fmt.Errorf("profile %s not found", id)
	}
	if err := validate([]byte(content)); err != nil {
		return err
	}
	formatted, err := Format(content)
	if err != nil {
		return err
	}
	return WriteAtomic(s.path(id+".yaml"), []byte(formatted), 0o600)
}

func (s *Store) has(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.ContainsFunc(s.index.Profiles, func(p Profile) bool { return p.ID == id })
}

func (s *Store) Select(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !slices.ContainsFunc(s.index.Profiles, func(p Profile) bool { return p.ID == id }) {
		return fmt.Errorf("profile %s not found", id)
	}
	s.index.Current = id
	return s.save()
}

// Due lists remote profiles whose auto-update interval has passed.
func (s *Store) Due(now time.Time) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	for _, p := range s.index.Profiles {
		since := p.UpdatedAt
		if p.LastTried.After(since) {
			since = p.LastTried
		}
		if p.URL != "" && p.Interval > 0 && now.Sub(since) >= time.Duration(p.Interval)*time.Hour {
			ids = append(ids, p.ID)
		}
	}
	return ids
}

// Override is the user's YAML merged on top of the current profile.
func (s *Store) Override() (string, error) {
	b, err := os.ReadFile(s.path("override.yaml"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(b), err
}

func (s *Store) SetOverride(content string) error {
	if _, err := parseMap([]byte(content)); err != nil {
		return fmt.Errorf("覆写不是有效的 YAML 字典: %w", err)
	}
	return WriteAtomic(s.path("override.yaml"), []byte(content), 0o600) // holds tunnel keys and proxy passwords
}

// formatted is a profile as stored: Format'd, or as given if it can't be (validate already passed).
func formatted(content []byte) []byte {
	if f, err := Format(string(content)); err == nil {
		return []byte(f)
	}
	return content
}

// Format re-indents YAML to two spaces per level, keeping comments, order and quoting.
// FormatBlock is Format with every non-empty list and mapping one item per line, for the
// override editor, whose content the GUI writes.
func FormatBlock(text string) (string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return "", err
	}
	blockStyle(&doc)
	b, err := yaml.Marshal(&doc)
	return string(b), err
}

func Format(text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return "", fmt.Errorf("不是有效的 YAML: %w", err)
	}
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return "", err
	}
	return b.String(), enc.Close()
}

func parseMap(b []byte) (map[string]any, error) {
	m := map[string]any{}
	if err := yaml.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// validate rejects content that isn't a Clash config, e.g. a base64 share-link list.
func validate(b []byte) error {
	m, err := parseMap(b)
	if err != nil {
		return fmt.Errorf("不是 Clash 配置（可能是 base64 节点列表）: %w", err)
	}
	if m["proxies"] == nil && m["proxy-providers"] == nil {
		return errors.New("配置中没有 proxies 或 proxy-providers")
	}
	return nil
}

// download fetches a subscription, trying each client in turn (e.g. direct, then through the core).
func download(url string, clients []*http.Client) ([]byte, *Usage, int, error) {
	if len(clients) == 0 {
		clients = []*http.Client{{Timeout: 30 * time.Second}}
	}
	var errs []error
	for _, c := range clients {
		content, usage, interval, err := fetch(c, url)
		if err == nil {
			return content, usage, interval, nil
		}
		errs = append(errs, err)
	}
	return nil, nil, 0, errors.Join(errs...)
}

func fetch(c *http.Client, url string) ([]byte, *Usage, int, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, 0, err
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := c.Do(req)
	if err != nil {
		return nil, nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, 0, fmt.Errorf("下载订阅: %s", resp.Status)
	}
	content, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, nil, 0, err
	}
	if err := validate(content); err != nil {
		return nil, nil, 0, err
	}
	interval, _ := strconv.Atoi(resp.Header.Get("profile-update-interval"))
	return content, parseUsage(resp.Header.Get("subscription-userinfo")), interval, nil
}

// parseUsage reads "upload=1; download=2; total=3; expire=4".
func parseUsage(header string) *Usage {
	if header == "" {
		return nil
	}
	u := &Usage{}
	for _, field := range strings.Split(header, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(field), "=")
		if !ok {
			continue
		}
		n, _ := strconv.ParseFloat(strings.TrimSpace(v), 64) // some services send floats
		switch strings.ToLower(k) {
		case "upload":
			u.Upload = int64(n)
		case "download":
			u.Download = int64(n)
		case "total":
			u.Total = int64(n)
		case "expire":
			u.Expire = int64(n)
		}
	}
	return u
}
