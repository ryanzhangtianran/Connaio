package app

import (
	"log"
	"path/filepath"

	"github.com/ryanzhangtianran/Connaio/internal/core"
	"github.com/ryanzhangtianran/Connaio/internal/profile"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// services are the app's Go side, the same behind the window (app.go) and the web UI (server.go).
type services struct {
	core     *CoreService
	profiles *ProfileService
	sysProxy *SystemProxyService
	enhanced *EnhancedService
	devices  *DeviceService
	settings *SettingsService
	conf     *settingsFile
}

// newServices wires the services over dataDir and writes the config the core starts with.
func newServices(dataDir string) (*services, error) {
	store, err := profile.Open(dataDir)
	if err != nil {
		return nil, err
	}
	settings := loadSettings(dataDir)
	svc := &CoreService{core: core.New(dataDir), ledger: openLedger(filepath.Join(dataDir, "traffic.json")), settings: settings}
	profiles := &ProfileService{store: store, core: svc.core, settings: settings}
	enhanced := &EnhancedService{core: svc.core, profiles: profiles, settings: settings}
	svc.onReady = func() error {
		defer enhanced.changed(enhanced.Enabled()) // whether TUN runs depends on the core just attached
		return profiles.apply()
	}
	s := &services{
		core:     svc,
		profiles: profiles,
		sysProxy: &SystemProxyService{core: svc.core, settings: settings, enabled: settings.Get().SystemProxy && !headless}, // known before the menu is built; applied by the guard once the core answers
		enhanced: enhanced,
		devices:  &DeviceService{core: svc, profiles: profiles},
		settings: &SettingsService{core: svc, settings: settings},
		conf:     settings,
	}
	if err := profiles.WriteConfig(); err != nil {
		log.Printf("build config: %v (keeping the previous config.yaml)", err)
	}
	return s, nil
}

func (s *services) list() []application.Service {
	return []application.Service{application.NewService(s.core), application.NewService(&ToolService{cs: s.core}), application.NewService(s.profiles),
		application.NewService(s.sysProxy), application.NewService(s.enhanced), application.NewService(s.devices), application.NewService(s.settings)}
}

// marshalError lists whatever a call from the UI fails with under the events button: the UI shows
// no errors itself.
func marshalError(err error) []byte { notify("error", "%v", err); return nil }
