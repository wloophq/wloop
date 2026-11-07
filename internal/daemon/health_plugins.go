package daemon

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/api"
)

// healthWait bounds how long a health read waits for the plugins to answer before it serves their last
// answer: a busy plugin answers in seconds, and the cockpit reads health on every change.
const healthWait = 250 * time.Millisecond

// pluginHealths are the plugins' last health answers and the ask under way, if any.
type pluginHealths struct {
	mu       sync.Mutex
	plugins  []api.PluginHealth
	sources  []api.SourceHealth
	answered bool
	asking   chan struct{}
}

// pluginsHealth are how the plugins and the sources answer health: asked again unless an ask is under way, and
// waited for up to healthWait once they answered before, else until they answer.
func (s *Server) pluginsHealth(ctx context.Context) ([]api.PluginHealth, []api.SourceHealth) {
	h := &s.healths
	h.mu.Lock()
	asking, answered := h.asking, h.answered
	if asking == nil {
		asking = make(chan struct{})
		h.asking = asking
		s.driving.Go(func() { s.askPlugins(asking) })
	}
	h.mu.Unlock()
	var late <-chan time.Time
	if answered {
		late = time.After(healthWait)
	}
	select {
	case <-asking:
	case <-late:
	case <-ctx.Done():
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.plugins), slices.Clone(h.sources)
}

func (s *Server) askPlugins(asking chan struct{}) {
	var plugins []api.PluginHealth
	var sources []api.SourceHealth
	var wg sync.WaitGroup
	if s.deps.PluginHealth != nil {
		wg.Go(func() { plugins = s.deps.PluginHealth(s.life) })
	}
	wg.Go(func() { sources = s.sourceHealth(s.life) })
	wg.Wait()
	h := &s.healths
	h.mu.Lock()
	h.plugins, h.sources, h.answered, h.asking = plugins, sources, true, nil
	h.mu.Unlock()
	close(asking)
}
