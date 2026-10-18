package app

import "github.com/wailsapp/wails/v3/pkg/application"

// showTray sets the cat on its state's tile, only when that changes, as each icon goes over D-Bus.
// No speed beside it: the menu's process rows show each one's; the mode shows on 出站模式.
func showTray(tray *application.SystemTray, icon []byte, _ Traffic, _, state string, _ bool) {
	setTrayIcon(tray, icon, state)
}

// menuRebuilt hands the rebuilt menu to the tray again: it passes its menu to the panel over D-Bus
// only when one is set, and Update redraws GTK's menus, not that copy, which kept the first layout,
// made before the core had any groups.
func menuRebuilt(tray *application.SystemTray, menu *application.Menu) { tray.SetMenu(menu) }
