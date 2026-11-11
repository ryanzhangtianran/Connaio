package app

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// An open menu isn't rebuilt (AppKit would keep its old rows and height), and the group and app it
// hasn't rows for yet leave no nil item for decorate, which would crash on one.
func TestRefreshHoldsRebuildWhileOpen(t *testing.T) {
	m := &trayMenu{layout: "old", details: map[*application.MenuItem]string{}, icons: map[*application.MenuItem]string{}}
	m.open.Store(true)
	groups := []Group{{Name: "新组", Type: "Selector", Now: "a", All: []Proxy{{Name: "a"}}}}
	apps := []partialApp{{name: "App", exec: "/Applications/App.app", on: true}}
	if m.refresh(groups, Profiles{}, nil, apps) || m.layout != "old" {
		t.Fatal("rebuilt while open")
	}
	if _, ok := m.details[nil]; ok {
		t.Fatal("nil item left in details")
	}
}
