//go:build !server

package app

import (
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// Run wires the services, window and menu bar item, and blocks until the app quits.
func Run(assets fs.FS, trayIcon []byte) error {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	sv, err := newServices(filepath.Join(configDir, "Connaio"))
	if err != nil {
		return err
	}
	svc, profiles, sysProxy, enhanced, settingsSvc, settings := sv.core, sv.profiles, sv.sysProxy, sv.enhanced, sv.settings, sv.conf
	if settingsSvc.LaunchAtLogin() {
		// written afresh: the app may have moved or been renamed, and older entries lack what the
		// system lists the item under (macOS 登录项与扩展)
		_ = settingsSvc.SetLaunchAtLogin(true)
	}

	// a closed window is freed, its web content process with it, and the app runs on in the menu bar
	// only, out of the Dock; showing it builds it again and brings both back
	var windowMu sync.Mutex
	var window *application.WebviewWindow
	var newWindow func(page string) *application.WebviewWindow
	// show opens the window on page, or where it was left when page is ""
	show := func(page string) {
		windowMu.Lock()
		defer windowMu.Unlock()
		setDockVisible(true)
		if window == nil {
			window = newWindow(page)
		} else if page != "" {
			emit("navigate", page)
		}
		window.Show()
		window.Focus()
	}
	app := application.New(application.Options{
		// the window shows no errors: whatever a call from it fails with is listed under the events button
		MarshalError: marshalError,
		// a second copy would fight the first over the core's ports; bring the first forward instead
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "com.ryanzhangtianran.connaio",
			OnSecondInstanceLaunch: func(d application.SecondInstanceData) { // its arguments carry a clash:// link on Windows and Linux
				if slices.ContainsFunc(d.Args, profiles.offered) {
					show("profiles")
				} else {
					show("")
				}
			},
		},
		Name:        "Connaio",
		Description: "Rule-based proxy with reverse tunnels",
		Services:    sv.list(),
		Assets:      application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		// lives on in the menu bar
		Mac:     application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: false},
		Windows: application.WindowsOptions{DisableQuitOnLastWindowClosed: true},
		Linux:   application.LinuxOptions{DisableQuitOnLastWindowClosed: true},
	})

	// a window takes Settings.Theme as it's built, before its page paints; SetTheme changes the open ones
	macAppearance := map[string]application.MacAppearanceType{"light": application.NSAppearanceNameAqua, "dark": application.NSAppearanceNameDarkAqua}
	winTheme := map[string]application.Theme{"light": application.Light, "dark": application.Dark}
	settingsSvc.setTheme = func(theme string) {
		for _, w := range app.Window.GetAll() {
			setAppearance(w, theme)
		}
	}

	newWindow = func(page string) *application.WebviewWindow {
		w := app.Window.NewWithOptions(application.WebviewWindowOptions{
			Title:          "Connaio",
			Width:          1440,
			Height:         890,
			MinWidth:       1440, // the activity page's four columns of cards show whole without scrolling from here, and every connections column too
			MinHeight:      890,
			BackgroundType: application.BackgroundTypeTranslucent,
			Frameless:      runtime.GOOS != "darwin", // one piece, the topbar drawing the window buttons (App.tsx WindowButtons); Windows keeps its shadow, corners and Mica
			Mac: application.MacWindow{
				InvisibleTitleBarHeight: 52,
				Backdrop:                application.MacBackdropTranslucent,
				TitleBar:                application.MacTitleBarHiddenInset,
				Appearance:              macAppearance[settings.Get().Theme],
			},
			Windows: application.WindowsWindow{BackdropType: application.Mica, Theme: winTheme[settings.Get().Theme]}, // the translucent background needs a backdrop behind it
			URL:     "/?page=" + url.QueryEscape(page),
		})
		w.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) { makeVibrant(w) })
		w.RegisterHook(events.Common.WindowClosing, func(*application.WindowEvent) { // not cancelled: Wails closes it after
			windowMu.Lock()
			window = nil
			windowMu.Unlock()
			freeWebView(w)
			setDockVisible(false)
		})
		return w
	}
	// the first window too is built once the app runs: on macOS one built before app.Run is never
	// freed when closed, left behind as an invisible window (alpha 0, off screen) in Mission Control
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) { show("") })
	// a provider's 一键导入 (clash://install-config): macOS hands it over here, Windows and Linux in the arguments
	app.Event.OnApplicationEvent(events.Common.ApplicationLaunchedWithUrl, func(e *application.ApplicationEvent) {
		if profiles.offered(e.Context().URL()) {
			show("profiles")
		}
	})

	// the floating process monitor: built when opened, kept above other windows, freed when closed
	// and opened again where it was left
	var monitorMu sync.Mutex
	var monitor *application.WebviewWindow
	openMonitor := func() {
		monitorMu.Lock()
		defer monitorMu.Unlock()
		if monitor == nil {
			opts := application.WebviewWindowOptions{
				Name: "monitor", Title: "进程监视", URL: "/?view=monitor",
				Width: 340, Height: 52, MinWidth: 280, MinHeight: 52, // folded: Monitor.tsx sets the height as it opens or folds
				Frameless: true, AlwaysOnTop: true, BackgroundType: application.BackgroundTypeTranslucent,
				Mac: application.MacWindow{
					Backdrop:           application.MacBackdropTranslucent,
					WindowLevel:        application.MacWindowLevelFloating,
					CollectionBehavior: application.MacWindowCollectionBehaviorCanJoinAllSpaces | application.MacWindowCollectionBehaviorFullScreenAuxiliary, // on every desktop, above full-screen apps
					Appearance:         macAppearance[settings.Get().Theme],
				},
				Windows: application.WindowsWindow{BackdropType: application.Acrylic, HiddenOnTaskbar: true, Theme: winTheme[settings.Get().Theme]},
			}
			if p := settings.Get().Monitor; len(p) == 2 {
				opts.InitialPosition, opts.X, opts.Y = application.WindowXY, p[0], p[1]
			}
			monitor = app.Window.NewWithOptions(opts)
			monitor.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) { makeFloating(monitor) })
			monitor.RegisterHook(events.Common.WindowClosing, func(*application.WindowEvent) {
				monitorMu.Lock()
				defer monitorMu.Unlock()
				x, y := monitor.Position()
				_ = settings.Update(func(st *Settings) { st.Monitor = []int{x, y} })
				freeWebView(monitor)
				monitor = nil
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
