//go:build !darwin

package app

import "github.com/wailsapp/wails/v3/pkg/application"

// The window's macOS touches (translucency, traffic lights, leaving the Dock) have no Linux or
// Windows counterpart: Linux keeps the system's decorations, Windows the Mica backdrop set in the
// window options.
func makeVibrant(*application.WebviewWindow) {}

func setDockVisible(bool) {}

func makeFloating(*application.WebviewWindow) {}

// setAppearance: a Windows window takes its theme only as it's built (app.go), Linux's has none.
func setAppearance(application.Window, string) {}

// freeWebView: closing a window frees its web view here already.
// ponytail: assumed for WebView2 and WebKitGTK, not measured; check the process list if it shows up
func freeWebView(*application.WebviewWindow) {}
