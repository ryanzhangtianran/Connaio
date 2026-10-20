package app

import "C"

// trayOpened is exported apart from tray_darwin.go, whose preamble defines C: a file with an
// export may only declare.
//
//export trayOpened
func trayOpened() {
	if trayOpen != nil {
		trayOpen()
	}
}
