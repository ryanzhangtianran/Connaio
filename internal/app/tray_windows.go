package app

import (
	"reflect"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/w32"
)

// showTray sets the cat on its state's tile, only when that changes. The notification area shows
// no text, so the tooltip carries the mode and whether anything is captured; no speed, as on Linux.
func showTray(tray *application.SystemTray, icon []byte, _ Traffic, mode, state string, _ bool) {
	setTrayIcon(tray, icon, state)
	tip := "Connaio"
	if label := modeLabel(mode); label != "" {
		tip += " · " + label
	}
	if state == "" {
		tip += " · 未接管流量"
	}
	tray.SetTooltip(tip)
}

// menuRebuilt hands the rebuilt menu to the tray again, which keeps the native menu it built when
// it was given one (the first layout, made before the core had any groups), and drops that menu's
// check-mark column: no main-menu item is ticked, their states being details on the right, and
// the column was a blank strip down the left. Submenus keep theirs, for the chosen node.
func menuRebuilt(tray *application.SystemTray, menu *application.Menu) {
	tray.SetMenu(menu)
	application.InvokeSync(func() {
		// the tray's native menu handle, impl.menu.menu: Wails keeps it unexported
		impl := reflect.ValueOf(tray).Elem().FieldByName("impl")
		if !impl.IsValid() || impl.IsNil() {
			return
		}
		popup := impl.Elem().Elem().FieldByName("menu")
		if !popup.IsValid() || popup.IsNil() {
			return
		}
		h := popup.Elem().FieldByName("menu")
		if !h.IsValid() || h.Uint() == 0 {
			return
		}
		info := w32.MENUINFO{FMask: w32.MIM_STYLE, DwStyle: w32.MNS_NOCHECK}
		info.CbSize = uint32(unsafe.Sizeof(info))
		w32.SetMenuInfo(w32.HMENU(h.Uint()), &info)
	})
}
