package app

import (
	"context"
	"log"
	"sync"
	"time"
)

// connectionMonitor owns the snapshot shared by live views and the traffic ledger.
type connectionMonitor struct {
	mu    sync.Mutex
	conns []Conn
	at    time.Time

	ledger   *ledger
	onSample func(conns []Conn, deltas map[string]int64)
}

// connections lists live connections, those outside partial enhancement's scope too when all is set.
// The ledger fetches the list every second; everyone else (the activity, connections, devices and
// processes pages) gets that snapshot while it is fresh, so the core serialises it once a second,
// not four times.
func (m *connectionMonitor) connections(all bool, fetch func() ([]Conn, error)) ([]Conn, error) {
	m.mu.Lock()
	conns, at := m.conns, m.at
	m.mu.Unlock()
	if conns == nil || time.Since(at) > 1500*time.Millisecond {
		var err error
		if conns, err = m.refresh(fetch); err != nil {
			return nil, err
		}
	}
	if all {
		return conns, nil
	}
	out := make([]Conn, 0, len(conns))
	for _, c := range conns {
		if c.Rule != "CaptureFilter" { // constant.CaptureFilter's name in the core
			out = append(out, c)
		}
	}
	return out, nil
}

// refresh fetches the snapshot from the core and keeps it for connections. Callers
// treat the slice as read-only.
func (m *connectionMonitor) refresh(fetch func() ([]Conn, error)) ([]Conn, error) {
	conns, err := fetch()
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.conns, m.at = conns, time.Now()
	m.mu.Unlock()
	return conns, nil
}

// invalidate drops the cached snapshot, so a list after a close shows the close.
func (m *connectionMonitor) invalidate() {
	m.mu.Lock()
	m.at = time.Time{}
	m.mu.Unlock()
}

// sample feeds the ledger every second (and the menu bar's process rows), saving it every minute.
func (m *connectionMonitor) sample(ctx context.Context, fetch func() ([]Conn, error)) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for i := 1; ; i++ {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if conns, err := m.refresh(fetch); err == nil {
				deltas := m.ledger.add(conns, now)
				if m.onSample != nil {
					m.onSample(conns, deltas)
				}
			}
			if i%60 == 0 {
				if err := m.ledger.save(); err != nil {
					log.Printf("save traffic: %v", err)
				}
			}
		}
	}
}
