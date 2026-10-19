package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ryanzhangtianran/Connaio/internal/core"
	"github.com/ryanzhangtianran/Connaio/internal/profile"
	"github.com/wailsapp/wails/v3/pkg/application"
	"go.yaml.in/yaml/v3"
)

type Profiles struct {
	Current  string            `json:"current"`
	Profiles []profile.Profile `json:"profiles"`
}

func init() {
	application.RegisterEvent[Profiles]("profiles")
	application.RegisterEvent[string]("setting") // dotted path of a setting that changed
}

// ProfileService manages subscriptions and the override, and feeds the merged config to the core.
type ProfileService struct {
	store    *profile.Store
	core     *core.Core
	settings *settingsFile
	mu       sync.Mutex // one change at a time: read the old state, change, apply, roll back
}

func (s *ProfileService) build() ([]byte, error) {
	enhanced := s.settings.Get().Enhanced && s.core.Daemon()
	return s.store.Build(enhanced)
}

// WriteConfig builds config.yaml before the core starts; nothing is running yet to reload.
func (s *ProfileService) WriteConfig() error {
	b, err := s.build()
	if err != nil {
		return err
	}
	return os.WriteFile(s.core.ConfigPath(), b, 0o600)
}

// apply rebuilds the config, has the core binary validate it, then swaps it in and reloads.
// A rejected config never replaces the running one.
func (s *ProfileService) apply() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyLocked()
}

func (s *ProfileService) applyLocked() error {
	b, err := s.build()
	if err != nil {
		return err
	}
	next := filepath.Join(s.core.Dir, "config.next.yaml")
	if err := os.WriteFile(next, b, 0o600); err != nil {
		return err
	}
	if err := s.core.Validate(context.Background(), next); err != nil {
		os.Remove(next)
		return err
	}
	if err := os.Rename(next, s.core.ConfigPath()); err != nil {
		return err
	}
	return s.core.Reload(context.Background(), b)
}

func (s *ProfileService) changed() {
	emit("profiles", s.Profiles())
}

func (s *ProfileService) Profiles() Profiles {
	current, list := s.store.List()
	return Profiles{Current: current, Profiles: list}
}

// clients try the subscription directly first, then through the core's mixed-port (blocked hosts).
func (s *ProfileService) clients() []*http.Client { return directThenProxy(s.core, 30*time.Second) }

// Add downloads a subscription; it is applied right away when it is the first profile.
func (s *ProfileService) Add(name, link string) error {
	p, err := s.store.AddRemote(name, link, s.clients()...)
	if err != nil {
		return err
	}
	return s.added(p.ID)
}

// AddLocal stores pasted Clash YAML as a profile.
func (s *ProfileService) AddLocal(name, content string) error {
	p, err := s.store.AddLocal(name, []byte(content))
	if err != nil {
		return err
	}
	return s.added(p.ID)
}

// added announces a new profile, and applies it when it is the one in use (the first).
func (s *ProfileService) added(id string) error {
	defer s.changed()
	s.mu.Lock()
	defer s.mu.Unlock()
	if current, _ := s.store.List(); current == id {
		return s.applyLocked()
	}
	return nil
}

// ImportFile lets the user pick a Clash YAML file and adds it as a local profile named after it.
func (s *ProfileService) ImportFile() error {
	path, err := application.Get().Dialog.OpenFile().
		SetTitle("导入配置").
		CanChooseFiles(true).
		AddFilter("Clash 配置", "*.yaml;*.yml").
		PromptForSingleSelection()
	if err != nil || path == "" {
		return err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return s.AddLocal(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), string(b))
}

// Content is a profile's YAML, for editing it by hand.
func (s *ProfileService) Content(id string) (string, error) { return s.store.Content(id) }

// SetContent saves a hand-edited profile; for the profile in use it is applied, and restored if the
// core rejects it.
func (s *ProfileService) SetContent(id, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, err := s.store.Content(id)
	if err != nil {
		return err
	}
	if err := s.store.SetContent(id, content); err != nil {
		return err
	}
	return s.applyOrRestore(id, old)
}

// applyOrRestore applies the config when profile id is the one in use, putting old back as its
// content if the core rejects it. Callers hold s.mu.
func (s *ProfileService) applyOrRestore(id, old string) error {
	if current, _ := s.store.List(); current != id {
		return nil
	}
	if err := s.applyLocked(); err != nil {
		return errors.Join(err, s.store.SetContent(id, old))
	}
	return nil
}

// Update re-downloads a subscription; one the core rejects is put back, so later edits keep
// working (its UpdatedAt stays bumped, which holds the auto-update off for an interval).
func (s *ProfileService) Update(id string) error {
	old, err := s.store.Content(id) // the download runs outside the lock; the swap and apply inside
	if err != nil {
		return err
	}
	if _, err := s.store.Update(id, s.clients()...); err != nil {
		return err
	}
	defer s.changed()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyOrRestore(id, old)
}

func (s *ProfileService) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	wasCurrent, _ := s.store.List()
	if err := s.store.Remove(id); err != nil {
		return err
	}
	defer s.changed()
	if wasCurrent == id {
		return s.applyLocked()
	}
	return nil
}

// Rename renames a profile. The name is only shown, so the config is not applied again.
func (s *ProfileService) Rename(id, name string) error {
	if err := s.store.Rename(id, name); err != nil {
		return err
	}
	s.changed()
	return nil
}

// Select switches profiles; on a rejected config the previous one stays selected and running.
func (s *ProfileService) Select(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, _ := s.store.List()
	if err := s.store.Select(id); err != nil {
		return err
	}
	defer s.changed()
	if err := s.applyLocked(); err != nil {
		if previous != "" {
			_ = s.store.Select(previous)
		}
		return err
	}
	return nil
}

// Override is the override as the editor shows it: formatted one item per line, whatever
// style the file on disk has.
func (s *ProfileService) Override() (string, error) {
	text, err := s.store.Override()
	if err != nil {
		return "", err
	}
	if f, err := profile.FormatBlock(text); err == nil && strings.TrimSpace(text) != "" {
		return f, nil
	}
	return text, nil
}

// SetOverride saves and applies the override, restoring the old one if the core rejects the result.
func (s *ProfileService) SetOverride(content string) error {
	return s.edit(func() error { return s.store.SetOverride(content) })
}

// edit runs a change to the override and applies it; if the core rejects the result the override
// is restored, so a bad form entry never breaks the running config. The whole sequence holds the
// lock: two edits at once would otherwise roll each other's work back.
func (s *ProfileService) edit(change func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, err := s.store.Override()
	if err != nil {
		return err
	}
	if err := change(); err != nil {
		return err
	}
	if err := s.applyLocked(); err != nil {
		return errors.Join(err, s.store.SetOverride(old))
	}
	return nil
}

// UserItems are the proxies, groups, rules and tunnels added from the GUI (kept in the override).
func (s *ProfileService) UserItems() (profile.UserItems, error) { return s.store.UserItems() }

// AddRule makes rule permanent by prepending it in the override.
func (s *ProfileService) AddRule(rule string) error {
	return s.edit(func() error { return s.store.PrependRule(rule) })
}

func (s *ProfileService) RemoveRule(rule string) error {
	return s.edit(func() error { return s.store.RemoveRule(rule) })
}

// AddProxy adds a node, also placing it at the front of the given groups. extra is YAML merged
// over the form's fields, for settings the form doesn't cover.
func (s *ProfileService) AddProxy(proxy map[string]any, extra string, groups []string) error {
	if err := profile.MergeYAML(proxy, extra); err != nil {
		return err
	}
	return s.edit(func() error { return s.store.AddProxy(proxy, groups) })
}

// Proxy is a proxy's fields as the core gets them (profile, override and patches merged).
func (s *ProfileService) Proxy(name string) (map[string]any, error) {
	v, err := s.store.Value(false, []string{"proxies"})
	if err != nil {
		return nil, err
	}
	list, _ := v.([]any)
	for _, p := range list {
		if m, ok := p.(map[string]any); ok && m["name"] == name {
			return m, nil
		}
	}
	return nil, fmt.Errorf("没有名为 %s 的代理", name)
}

// EditProxy changes the named proxy by patch, only the fields edited (a null removes one), with
// extra YAML merged over it as in AddProxy.
func (s *ProfileService) EditProxy(name string, patch map[string]any, extra string) error {
	if err := profile.MergeYAML(patch, extra); err != nil {
		return err
	}
	return s.edit(func() error { return s.store.EditProxy(name, patch) })
}

func (s *ProfileService) RemoveProxy(name string) error {
	return s.edit(func() error { return s.store.RemoveProxy(name) })
}

func (s *ProfileService) AddGroup(group map[string]any) error {
	return s.edit(func() error { return s.store.AddGroup(group) })
}

// Group is the named group as the config has it, for editing.
func (s *ProfileService) Group(name string) (map[string]any, error) {
	v, err := s.store.Value(false, []string{"proxy-groups"})
	if err != nil {
		return nil, err
	}
	list, _ := v.([]any)
	for _, g := range list {
		if m, ok := g.(map[string]any); ok && m["name"] == name {
			return m, nil
		}
	}
	return nil, fmt.Errorf("没有名为 %s 的策略组", name)
}

// EditGroup replaces the named group; its name stays.
func (s *ProfileService) EditGroup(name string, group map[string]any) error {
	return s.edit(func() error { return s.store.EditGroup(name, group) })
}

func (s *ProfileService) RemoveGroup(name string) error {
	return s.edit(func() error { return s.store.RemoveGroup(name) })
}

func (s *ProfileService) AddTunnel(tunnel map[string]any) error {
	return s.edit(func() error { return s.store.AddTunnel(tunnel) })
}

func (s *ProfileService) EditTunnel(name string, tunnel map[string]any) error {
	return s.edit(func() error { return s.store.EditTunnel(name, tunnel) })
}

// SetTunnelDisabled stops a tunnel (kept in the config, not connected) or starts it again.
func (s *ProfileService) SetTunnelDisabled(name string, disabled bool) error {
	return s.edit(func() error { return s.store.SetTunnelDisabled(name, disabled) })
}

func (s *ProfileService) RemoveTunnel(name string) error {
	return s.edit(func() error { return s.store.RemoveTunnel(name) })
}

// MoveRule moves the GUI-added rule at index one place up (delta -1) or down (+1).
func (s *ProfileService) MoveRule(index, delta int) error {
	return s.edit(func() error { return s.store.MoveRule(index, delta) })
}

// DeleteRule removes the rule at index (the core's numbering): a GUI-added one from the override,
// one of the profile's by hiding it, so the profile's other rules keep following the subscription.
func (s *ProfileService) DeleteRule(index int) error {
	items, err := s.store.UserItems()
	if err != nil {
		return err
	}
	v, err := s.store.Value(false, []string{"rules"})
	if err != nil {
		return err
	}
	list, _ := v.([]any)
	if index < 0 || index >= len(list) {
		return fmt.Errorf("没有第 %d 条规则", index)
	}
	rule, _ := list[index].(string)
	if index < len(items.Rules) {
		return s.edit(func() error { return s.store.RemoveRule(rule) })
	}
	if strings.HasPrefix(strings.ToUpper(rule), "MATCH,") {
		return errors.New("MATCH 是兜底规则，不能删除")
	}
	return s.edit(func() error { return s.store.HideRule(rule) })
}

// Set writes a dotted setting such as "dns.enhanced-mode" into the override and applies it.
func (s *ProfileService) Set(path string, value any) error {
	if err := s.edit(func() error { return s.store.SetPath(strings.Split(path, "."), value) }); err != nil {
		return err
	}
	emit("setting", path)
	return nil
}

// Format re-indents YAML (two spaces, comments kept); profiles are stored formatted anyway.
func (s *ProfileService) Format(text string) (string, error) { return profile.Format(text) }

// SetText is Set with the value typed as YAML, as in the override: "true", "7890", "[a, b]",
// "{enable: true}"; anything else is a string.
func (s *ProfileService) SetText(path, text string) error {
	path = strings.TrimSpace(path)
	if path == "" || strings.Contains(path, "..") || strings.HasPrefix(path, ".") || strings.HasSuffix(path, ".") {
		return fmt.Errorf("字段格式不对：%q", path)
	}
	var value any
	if err := yaml.Unmarshal([]byte(text), &value); err != nil {
		return fmt.Errorf("值无法解析：%w", err)
	}
	return s.Set(path, value)
}

// Value reads a dotted setting from the effective config (defaults, profile and override merged).
// capture.* reads as if enhanced, so the chosen scope shows while enhanced mode is off too.
func (s *ProfileService) Value(path string) (any, error) {
	enhanced := s.settings.Get().Enhanced && s.core.Daemon() || strings.HasPrefix(path, "capture.")
	return s.store.Value(enhanced, strings.Split(path, "."))
}

func (s *ProfileService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	go func() {
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				for _, id := range s.store.Due(now) {
					if err := s.Update(id); err != nil {
						notify("error", "订阅自动更新失败（%s）：%v", id, err)
					}
				}
			}
		}
	}()
	return nil
}
