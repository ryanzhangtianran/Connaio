package app

import "testing"

func TestConnectionMonitorSnapshot(t *testing.T) {
	var monitor connectionMonitor
	calls := 0
	fetch := func() ([]Conn, error) {
		calls++
		return []Conn{{ID: "managed"}, {ID: "bypassed", Rule: "CaptureFilter"}}, nil
	}
	filtered, err := monitor.connections(false, fetch)
	if err != nil || len(filtered) != 1 || filtered[0].ID != "managed" {
		t.Fatalf("filtered connections = %v, %v", filtered, err)
	}
	all, err := monitor.connections(true, fetch)
	if err != nil || len(all) != 2 || calls != 1 {
		t.Fatalf("cached connections = %v, %v, fetches = %d", all, err, calls)
	}
	monitor.invalidate()
	if _, err := monitor.connections(true, fetch); err != nil || calls != 2 {
		t.Fatalf("invalidated snapshot not refreshed: %v, fetches = %d", err, calls)
	}
}
