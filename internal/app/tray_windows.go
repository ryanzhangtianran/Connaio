package app

import (
	"os"
	"syscall"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/w32"
	"golang.org/x/sys/windows"
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
func menuRebuilt(tray *application.SystemTray, menu *application.Menu) {
	tray.SetMenu(menu)
	if trayProc == 0 {
		roomyRows()
	}
}

// menuRowHeight is a tray menu row's height at 100%: Windows fits a native menu's rows tightly
// around their text, with no setting for it.
const menuRowHeight = 30

var trayProc uintptr // the tray window's own procedure, once roomyRows has taken it over

// roomyRows makes the tray menu's rows menuRowHeight tall. A row whose bitmap is drawn on request
// (HBMMENU_CALLBACK) is as tall as its owner, the tray's window, says when the menu measures it;
// the bitmap shares the check's column (MNS_CHECKORBMP), so the menu grows no wider, and draws nothing.
func roomyRows() {
	hwnd := trayWindow()
	if hwnd == 0 {
		return // not made yet: the next rebuild tries again
	}
	trayProc = w32.GetWindowLongPtr(hwnd, w32.GWLP_WNDPROC) // before the swap, which hands it messages at once
	w32.SetWindowLongPtr(hwnd, w32.GWLP_WNDPROC, syscall.NewCallback(func(hwnd w32.HWND, msg uint32, wParam, lParam uintptr) uintptr {
		switch msg {
		case w32.WM_INITMENUPOPUP: // the main menu or a submenu, about to be measured
			menu := w32.HMENU(wParam)
			w32.SetMenuInfo(menu, &w32.MENUINFO{CbSize: uint32(unsafe.Sizeof(w32.MENUINFO{})), FMask: w32.MIM_STYLE, DwStyle: w32.MNS_CHECKORBMP})
			for i := range w32.GetMenuItemCount(menu) {
				info := w32.MENUITEMINFO{CbSize: uint32(unsafe.Sizeof(w32.MENUITEMINFO{})), FMask: w32.MIIM_FTYPE | w32.MIIM_BITMAP}
				if w32.GetMenuItemInfo(menu, uint32(i), true, &info) && info.FType&w32.MFT_SEPARATOR == 0 && info.HbmpItem == 0 {
					info.FMask, info.HbmpItem = w32.MIIM_BITMAP, ^w32.HBITMAP(0) // HBMMENU_CALLBACK, (HBITMAP)-1
					w32.SetMenuItemInfo(menu, uint32(i), true, &info)
				}
			}
		case w32.WM_MEASUREITEM:
			if m := *(**w32.MEASUREITEMSTRUCT)(unsafe.Pointer(&lParam)); m.CtlType == w32.ODT_MENU {
				m.ItemWidth, m.ItemHeight = 0, uint32(menuRowHeight*w32.GetDpiForWindow(hwnd)/96)
				return 1
			}
		case w32.WM_DRAWITEM:
			if **(**uint32)(unsafe.Pointer(&lParam)) == w32.ODT_MENU { // DRAWITEMSTRUCT's CtlType
				return 1
			}
		}
		return w32.CallWindowProc(trayProc, hwnd, msg, wParam, lParam)
	}))
}

// trayWindow is the tray's hidden message-only window, of the class Wails gives its windows.
func trayWindow() w32.HWND {
	class, _ := windows.UTF16PtrFromString("WailsWebviewWindow")
	var after uintptr
	for {
		h, _, _ := findWindowEx.Call(uintptr(w32.HWND_MESSAGE), after, uintptr(unsafe.Pointer(class)), 0)
		if h == 0 {
			return 0
		}
		if _, pid := w32.GetWindowThreadProcessId(w32.HWND(h)); pid == os.Getpid() {
			return w32.HWND(h)
		}
		after = h
	}
}

var findWindowEx = windows.NewLazySystemDLL("user32.dll").NewProc("FindWindowExW")

// textDots: a Windows menu item shows no icon of ours, so a state's dot is ● or ○ in the text.
const textDots = true

func markState(*application.MenuItem, string) {}
