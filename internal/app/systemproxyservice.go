package app

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/ryanzhangtianran/Connaio/internal/core"
	"github.com/ryanzhangtianran/Connaio/internal/sysproxy"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func init() {
	application.RegisterEvent[bool]("sysproxy")
}

// SystemProxyService points the macOS system proxy at the core's mixed-port while enabled, and
// guards it: another app replacing the setting gets overridden within 15s.
type SystemProxyService struct {
	core     *core.Core
	settings *settingsFile
	onChange func(bool) // menu bar checkbox

	mu      sync.Mutex
	enabled bool // the user's choice, persisted
	port    int  // port currently set, 0 = not set
}

func (s *SystemProxyService) Enabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enabled
}

func (s *SystemProxyService) SetEnabled(on bool) error {
	if headless {
		return errors.New("无界面运行时没有系统代理可设置")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if on {
		port, err := mixedPort(s.core)
		if err != nil {
			return err
		}
		if err := sysproxy.Set(port); err != nil {
			return err
		}
		s.port = port
	} else { // whether this session set it or an earlier one did (a crash left it on)
		if err := sysproxy.Clear(); err != nil {
			return err
		}
		s.port = 0
	}
	s.enabled = on
	if err := s.settings.Update(func(st *Settings) { st.SystemProxy = on }); err != nil {
		return err
	}
	if s.onChange != nil {
		s.onChange(on)
	}
	emit("sysproxy", on)
	return nil
}

// mixedPort is the core's HTTP/SOCKS5 port, an error when it has none.
func mixedPort(c *core.Core) (int, error) {
	var cfg struct {
		MixedPort int `json:"mixed-port"`
	}
	if err := c.Do(context.Background(), http.MethodGet, "/configs", nil, &cfg); err != nil {
		return 0, err
	}
	if cfg.MixedPort == 0 {
		return 0, errors.New("配置中没有 mixed-port，代理端口没有在监听")
	}
	return cfg.MixedPort, nil
}

// directThenProxy are one-shot clients to try in turn: direct, then through the core's mixed-port
// (for hosts the network blocks), when the core has one.
func directThenProxy(c *core.Core, timeout time.Duration) []*http.Client {
	direct := oneShotClient(0, timeout)
	port, err := mixedPort(c)
	if err != nil {
		return []*http.Client{direct}
	}
	return []*http.Client{direct, oneShotClient(port, timeout)}
}

// oneShotClient is an HTTP client through the core's mixed-port (port > 0) or direct, its connection
// closed after each request: a fresh Transport's idle connection is never reused, and would linger
// (listed as this app's own live connection) until the server drops it.
func oneShotClient(port int, timeout time.Duration) *http.Client {
	t := &http.Transport{DisableKeepAlives: true}
	if port > 0 {
		t.Proxy = http.ProxyURL(&url.URL{Scheme: "http", Host: "127.0.0.1:" + strconv.Itoa(port)})
	}
	return &http.Client{Transport: t, Timeout: timeout}
}

func (s *SystemProxyService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			s.guard()
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	return nil
}

// guard (re)applies the system proxy when enabled: at startup, after another app changed it,
// or when a new profile moved mixed-port.
func (s *SystemProxyService) guard() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.enabled {
		return
	}
	port, err := mixedPort(s.core)
	if err != nil {
		return // core not up yet
	}
	if sysproxy.IsSet(port) {
		s.port = port
		return
	}
	if err := sysproxy.Set(port); err != nil {
		notify("error", "设置系统代理失败：%v", err)
		return
	}
	if s.port == port {
		notify("warning", "系统代理被其他程序修改，已恢复为 127.0.0.1:%d", port)
	}
	s.port = port
	if s.onChange != nil {
		s.onChange(true)
	}
	emit("sysproxy", true)
}

// ServiceShutdown turns the proxy off so apps aren't left pointing at a closed port; the setting
// stays on and is re-applied next launch.
func (s *SystemProxyService) ServiceShutdown() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.port == 0 && !s.settings.Get().SystemProxy {
		return nil
	}
	s.port = 0
	return sysproxy.Clear() // whether this session set it or an earlier one did
}
