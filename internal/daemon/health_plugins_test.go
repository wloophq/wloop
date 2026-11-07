package daemon

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/wloophq/wloop/internal/api"
)

// slowPlugins answer health with their current reason, and hang while held.
type slowPlugins struct {
	mu     sync.Mutex
	reason string
	held   chan struct{}
}

func (p *slowPlugins) answer(ctx context.Context) string {
	p.mu.Lock()
	held, reason := p.held, p.reason
	p.mu.Unlock()
	if held != nil {
		select {
		case <-held:
		case <-ctx.Done():
			return "timed out"
		}
	}
	return reason
}

func (p *slowPlugins) set(reason string, held chan struct{}) {
	p.mu.Lock()
	p.reason, p.held = reason, held
	p.mu.Unlock()
}

func (p *slowPlugins) server() *Server {
	return New(Deps{
		PluginHealth: func(ctx context.Context) []api.PluginHealth {
			return []api.PluginHealth{{ID: "slack", Status: "serving", Reason: p.answer(ctx)}}
		},
		Runtimes: []PluginRuntime{{ID: "slack", Health: func(ctx context.Context) (api.SourceHealth, error) {
			return api.SourceHealth{Warnings: []string{p.answer(ctx)}}, nil
		}}},
	})
}

func TestHealthServesThePluginsLastAnswerWhileTheyAreSlow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := &slowPlugins{reason: "first"}
		s := p.server()
		t.Cleanup(func() { s.stopLife(); s.driving.Wait() })
		ctx := context.Background()
		if ps, ss := s.pluginsHealth(ctx); ps[0].Reason != "first" || ss[0].Warnings[0] != "first" {
			t.Fatalf("first read %+v %+v", ps, ss)
		}
		held := make(chan struct{})
		p.set("second", held)
		start := time.Now()
		ps, ss := s.pluginsHealth(ctx)
		if took := time.Since(start); took != healthWait {
			t.Fatalf("a slow plugin held health %v, want %v", took, healthWait)
		}
		if ps[0].Reason != "first" || ss[0].Warnings[0] != "first" {
			t.Fatalf("while slow %+v %+v, want the last answer", ps, ss)
		}
		close(held)
		synctest.Wait()
		if ps, ss := s.pluginsHealth(ctx); ps[0].Reason != "second" || ss[0].Warnings[0] != "second" {
			t.Fatalf("once answered %+v %+v", ps, ss)
		}
	})
}

func TestHealthAsksSlowPluginsOnceAtATime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := &slowPlugins{reason: "first"}
		var asked int
		var mu sync.Mutex
		s := p.server()
		inner := s.deps.PluginHealth
		s.deps.PluginHealth = func(ctx context.Context) []api.PluginHealth {
			mu.Lock()
			asked++
			mu.Unlock()
			return inner(ctx)
		}
		t.Cleanup(func() { s.stopLife(); s.driving.Wait() })
		s.pluginsHealth(context.Background())
		held := make(chan struct{})
		p.set("second", held)
		for range 3 {
			s.pluginsHealth(context.Background())
		}
		close(held)
		synctest.Wait()
		if asked != 2 {
			t.Fatalf("asked %d times, want 2: the first read, then one for the reads while it hung", asked)
		}
	})
}
