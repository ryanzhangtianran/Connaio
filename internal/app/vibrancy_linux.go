package app

import "github.com/wailsapp/wails/v3/pkg/application"

// The window's macOS touches (translucency, traffic lights, leaving the Dock) have no Linux
// counterpart: the window keeps the system's decorations.
func makeVibrant(*application.WebviewWindow) {}

func setDockVisible(bool) {}

func makeFloating(*application.WebviewWindow) {}
