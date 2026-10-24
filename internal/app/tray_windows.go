package app

import (
	"github.com/wailsapp/wails/v3/pkg/application"
)

// showTray sets the cat on its state's tile, only when that changes. The notification area shows
// no text, so the tooltip carries the mode and whether anything is captured; no speed, as on Linux.
func showTray(tray *application.SystemTray, icon []byte, _ Traffic, mode, state string, _ bool) bool {
	setTrayIcon(tray, icon, state)
	tip := "Connaio"
	if label := modeLabel(mode); label != "" {
		tip += " · " + label
	}
	if state == "" {
		tip += " · 未接管流量"
	}
	tray.SetTooltip(tip)
	return true
}

// menuRebuilt hands the rebuilt menu to the tray again, which keeps the native menu it built when
// it was given one (the first layout, made before the core had any groups). The menu keeps Windows'
// check column: dropping it put every label flush against the menu's edge, a cramped look.
func menuRebuilt(tray *application.SystemTray, menu *application.Menu) { tray.SetMenu(menu) }

// textDots: a Windows menu item shows no icon of ours, so a state's dot is ● or ○ in the text.
const textDots = true

func markState(*application.MenuItem, string) {}
