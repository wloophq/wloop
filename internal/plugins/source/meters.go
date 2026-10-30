package source

import (
	"context"
	"slices"
	"sync"

	"github.com/wloophq/wloop/internal/meter"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// Meters bridge the host's meter scopes to the samples a plugin sends back:
// a call made under Call carries its scope's name, and a sample a plugin
// meters under that name lands in the scope while the call is in flight,
// else in Registry under the name.
type Meters struct {
	// Registry keeps a sample whose scope ended; nil drops it.
	Registry *meter.Registry

	mu   sync.Mutex
	live map[string][]*meter.Scope
}

// Call is ctx carrying the Call of a request to a plugin: priority and
// the name of ctx's meter scope, which the scope stays reachable under
// until done runs.
func (m *Meters) Call(ctx context.Context, priority src.Priority) (_ context.Context, done func()) {
	s := meter.ScopeOf(ctx)
	if s == nil || m == nil {
		return src.WithCall(ctx, src.Call{Priority: priority}), func() {}
	}
	name := s.Name()
	m.mu.Lock()
	if m.live == nil {
		m.live = map[string][]*meter.Scope{}
	}
	m.live[name] = append(m.live[name], s)
	m.mu.Unlock()
	return src.WithCall(ctx, src.Call{Priority: priority, MeterScope: name}), func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		scopes := m.live[name]
		if i := slices.Index(scopes, s); i >= 0 {
			scopes = slices.Delete(scopes, i, i+1)
		}
		if len(scopes) == 0 {
			delete(m.live, name)
			return
		}
		m.live[name] = scopes
	}
}

// add records s under its scope. A sample with no duration that carries
// points is the cost a response reported for a request already counted.
func (m *Meters) add(s src.Sample) {
	x := meter.Sample{Res: meter.Resource(s.Resource), Op: s.Op, Spawn: s.Spawn, Dur: s.Duration, Err: s.Failed, Points: s.Points}
	spend := s.Duration == 0 && s.Points > 0 && !s.Spawn && !s.Failed
	m.mu.Lock()
	scopes := m.live[s.Scope]
	var sc *meter.Scope
	if len(scopes) > 0 {
		sc = scopes[len(scopes)-1]
	}
	m.mu.Unlock()
	switch {
	case sc != nil:
		sc.Add(x, spend)
	case m.Registry != nil:
		name := s.Scope
		if name == "" {
			name = meter.Untagged
		}
		m.Registry.Add(name, x, spend)
	}
}

// meterResources are the resources a plugin may meter: the boundaries a
// plugin crosses itself.
var meterResources = []meter.Resource{meter.GitHub, meter.Git, meter.Work}

func knownResource(r string) bool { return slices.Contains(meterResources, meter.Resource(r)) }
