//go:build !server

package app

// headless: built as connaio-server (server.go), with no window, tray or desktop around it, the core
// always its own child and nothing done to the system it runs on
const headless = false
