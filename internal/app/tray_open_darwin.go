package app

import "C"

// trayOpened and trayClosed are exported apart from tray_darwin.go, whose preamble defines C: a
// file with an export may only declare.
//
//export trayOpened
func trayOpened() {
	if trayOpen != nil {
		trayOpen()
	}
}

//export trayClosed
func trayClosed() {
	if trayClose != nil {
		trayClose()
	}
}
