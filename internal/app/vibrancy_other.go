//go:build !darwin

package app

import "github.com/wailsapp/wails/v3/pkg/application"

// The window's macOS touches (translucency, traffic lights, leaving the Dock) have no Linux or
// Windows counterpart: Linux keeps the system's decorations, Windows the Mica backdrop set in the
// window options.
func makeVibrant(*application.WebviewWindow) {}

func setDockVisible(bool) {}

func makeFloating(*application.WebviewWindow) {}
