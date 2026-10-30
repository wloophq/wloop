package api

import (
	"context"
	"sync"
)

type testMeter struct {
	mu      sync.Mutex
	samples []Sample
}

func (m *testMeter) Hear(_ context.Context, s Sample) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.samples = append(m.samples, s)
}

type testStat struct {
	Calls, Spawns, Errors, NotModified, Points, Bytes int64
}

func (m *testMeter) Of(op string) testStat {
	m.mu.Lock()
	defer m.mu.Unlock()
	var st testStat
	for _, s := range m.samples {
		if op != "" && s.Op != op {
			continue
		}
		st.Points += s.Points
		if s.Spend {
			continue
		}
		st.Calls++
		st.Bytes += s.Bytes
		if s.Spawn {
			st.Spawns++
		}
		if s.Failed {
			st.Errors++
		}
		if s.NotModified {
			st.NotModified++
		}
	}
	return st
}
