package app

import (
	"context"
	"encoding/json"
)

// Memory is what Connaio holds in memory, in bytes: the app (its window's web view included) and the core.
type Memory struct {
	App  int64 `json:"app"`
	Core int64 `json:"core"`
}

// Memory reads the app's processes now and takes the core's last report on its /memory stream.
func (s *CoreService) Memory() Memory {
	return Memory{App: appMemory(), Core: s.coreMem.Load()}
}

// streamMemory keeps the core's own reading of its memory, which it sends once a second.
func (s *CoreService) streamMemory(ctx context.Context) {
	s.retryStream(ctx, "memory stream", "/memory", func(line []byte) {
		var m struct {
			Inuse int64 `json:"inuse"`
		}
		if json.Unmarshal(line, &m) == nil && m.Inuse > 0 { // the first reading is always 0
			s.coreMem.Store(m.Inuse)
		}
	}, nil, nil)
}
