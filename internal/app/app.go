package app

import (
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/ryanzhangtianran/Connaio/internal/core"
	"github.com/ryanzhangtianran/Connaio/internal/profile"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// Run wires the services, window and menu bar item, and blocks until the app quits.
func Run(assets fs.FS, trayIcon []byte) error {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	dataDir := filepath.Join(configDir, "Connaio")
	store, err := profile.Open(dataDir)
	if err != nil {
		return err
	}
	settings := loadSettings(dataDir)
	svc := &CoreService{core: core.New(dataDir), ledger: openLedger(filepath.Join(dataDir, "traffic.json"))}
	profiles := &ProfileService{store: store, core: svc.core, settings: settings}
	svc.onReady = profiles.apply
	sysProxy := &SystemProxyService{core: svc.core, settings: settings, enabled: settings.Get().SystemProxy} // known before the menu is built; applied by the guard once the core answers
	enhanced := &EnhancedService{core: svc.core, profiles: profiles, settings: settings}
	devices := &DeviceService{core: svc, profiles: profiles}
	settingsSvc := &SettingsService{core: svc, settings: settings}
	if settingsSvc.LaunchAtLogin() {
		// written afresh: the app may have moved or been renamed, and older entries lack what the
		// system lists the item under (macOS 登录项与扩展)
		_ = settingsSvc.SetLaunchAtLogin(true)
	}
	if err := profiles.WriteConfig(); err != nil {
		log.Printf("build config: %v (keeping the previous config.yaml)", err)
	}

	var window *application.WebviewWindow
	// a closed window leaves the app running in the menu bar only, out of the Dock; showing it brings both back
	show := func() {
		if window != nil {
			setDockVisible(true)
			window.Show()
			window.Focus()
		}
	}
	app := application.New(application.Options{
		// the window shows no errors: whatever a call from it fails with is listed under the events button
		MarshalError: func(err error) []byte { notify("error", "%v", err); return nil },
		// a second copy would fight the first over the core's ports; bring the first forward instead
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID:               "com.ryanzhangtianran.connaio",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) { show() },
		},
		Name:        "Connaio",
		Description: "Rule-based proxy with reverse tunnels",
		Services:    []application.Service{application.NewService(svc), application.NewService(&ToolService{cs: svc}), application.NewService(profiles), application.NewService(sysProxy), application.NewService(enhanced), application.NewService(devices), application.NewService(settingsSvc)},
		Assets:      application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: false, // lives on in the menu bar
		},
	})

	window = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:          "Connaio",
		Width:          1440,
		Height:         890,
		MinWidth:       1440, // the activity page's four columns of cards show whole without scrolling from here, and every connections column too
		MinHeight:      890,
		BackgroundType: application.BackgroundTypeTranslucent,
		Frameless:      runtime.GOOS == "linux", // the topbar draws the window buttons (App.tsx WindowButtons)
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 52,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		Windows: application.WindowsWindow{BackdropType: application.Mica}, // the translucent background needs a backdrop behind it
		URL:     "/",
	})
	window.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) { makeVibrant(window) })
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		window.Hide()
		setDockVisible(false)
		e.Cancel()
	})

	// the floating process monitor: built on first use, kept above other windows, hidden when closed
	// and shown again where it was left
	var monitorMu sync.Mutex
	var monitor *application.WebviewWindow
	openMonitor := func() {
		monitorMu.Lock()
		defer monitorMu.Unlock()
		if monitor == nil {
			opts := application.WebviewWindowOptions{
				Name: "monitor", Title: "进程监视", URL: "/?view=monitor",
				Width: 340, Height: 44, MinWidth: 280, MinHeight: 44, // folded: Monitor.tsx sets the height as it opens or folds
				Frameless: true, AlwaysOnTop: true, BackgroundType: application.BackgroundTypeTranslucent,
				Mac: application.MacWindow{
					Backdrop:           application.MacBackdropTranslucent,
					WindowLevel:        application.MacWindowLevelFloating,
					CollectionBehavior: application.MacWindowCollectionBehaviorCanJoinAllSpaces | application.MacWindowCollectionBehaviorFullScreenAuxiliary, // on every desktop, above full-screen apps
				},
				Windows: application.WindowsWindow{BackdropType: application.Acrylic, HiddenOnTaskbar: true},
			}
			if p := settings.Get().Monitor; len(p) == 2 {
				opts.InitialPosition, opts.X, opts.Y = application.WindowXY, p[0], p[1]
			}
			monitor = app.Window.NewWithOptions(opts)
			monitor.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) { makeFloating(monitor) })
			monitor.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
				x, y := monitor.Position()
				_ = settings.Update(func(st *Settings) { st.Monitor = []int{x, y} })
				monitor.Hide()
				e.Cancel()
			})
		}
		monitor.Show()
		monitor.Focus()
	}
	settingsSvc.openMonitor = openMonitor

	tray := app.SystemTray.New()
	tray.SetTemplateIcon(trayIcon)
	newTrayMenu(&trayMenu{
		app: app, tray: tray, icon: trayIcon, show: show, monitor: openMonitor,
		svc: svc, profiles: profiles, sysProxy: sysProxy, enhanced: enhanced, settings: settings,
	})

	return app.Run()
}

// rate formats bytes per second for the menu bar, at most 9 characters: 0 KB/s, 512 KB/s, 3.4 MB/s.
func rate(b int64) string {
	switch mb := float64(b) / 1e6; {
	case b < 1e6:
		return fmt.Sprintf("%d KB/s", b/1000)
	case mb < 99.95:
		return fmt.Sprintf("%.1f MB/s", mb)
	default:
		return fmt.Sprintf("%.0f MB/s", mb)
	}
}
