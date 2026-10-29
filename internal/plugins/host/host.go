// Package host runs plugin processes of any kind: it finds them, decides
// which binaries it trusts, starts them through a Launcher, restarts one
// that crashes with a backoff, and stops them all when the daemon stops.
// What a kind does with a started process belongs to the kind's adapter,
// such as internal/plugins/source; the host names no kind.
package host

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
)

// Kind is the adapter of one plugin kind, which connects the kind of a
// started process to the daemon.
type Kind interface {
	Kind() plugin.Kind
	// Start connects the kind of plugin m's freshly started process.
	Start(ctx context.Context, m *manifest.Manifest, c *plugin.Client) error
	// Stop asks the kind of plugin id to stop, before the host kills it.
	Stop(ctx context.Context, id string)
	// Down says plugin id's process ended or did not start.
	Down(id string)
}

// Run is a plugin the host runs: its manifest and how it starts.
type Run struct {
	Manifest *manifest.Manifest
	Spec     plugin.Spec
}

// Defaults of a Host's zero fields.
const (
	DefaultMinBackoff  = time.Second
	DefaultMaxBackoff  = 5 * time.Minute
	DefaultPoll        = time.Second
	DefaultStopTimeout = 5 * time.Second
)

// Host runs plugins. Its zero durations take the defaults.
type Host struct {
	Launcher plugin.Launcher
	Kinds    []Kind
	Logf     func(format string, args ...any)
	// MinBackoff is the wait before restarting a plugin that crashed; it
	// doubles after each restart that crashes before MaxBackoff passed, up
	// to MaxBackoff, and is MinBackoff again after a run that lasted longer.
	MinBackoff, MaxBackoff time.Duration
	// Poll is how often the host checks that a plugin process runs.
	Poll time.Duration
	// StopTimeout bounds each kind's Stop at shutdown.
	StopTimeout time.Duration

	wg sync.WaitGroup
}

// Start runs each of runs, each in a goroutine of its own, until ctx ends;
// Wait waits for them.
func (h *Host) Start(ctx context.Context, runs []Run) {
	for _, r := range runs {
		h.wg.Go(func() { h.supervise(ctx, r) })
	}
}

// Wait waits for every plugin Start ran to stop; each run kills the
// process it launched before it returns, so none is left: there is no
// reattach. It leaves the plugin processes of another host alone.
func (h *Host) Wait() {
	h.wg.Wait()
}

func (h *Host) supervise(ctx context.Context, r Run) {
	id := r.Manifest.ID
	delay := h.or(h.MinBackoff, DefaultMinBackoff)
	for {
		started := time.Now()
		p, err := h.launch(ctx, r)
		if err == nil {
			h.logf("plugin %s: started", id)
			if !h.watch(ctx, p) {
				h.stop(ctx, r, p)
				return
			}
			err = errProcessEnded
			if time.Since(started) >= h.or(h.MaxBackoff, DefaultMaxBackoff) {
				delay = h.or(h.MinBackoff, DefaultMinBackoff)
			}
		}
		h.down(r)
		if ctx.Err() != nil {
			return
		}
		h.logf("plugin %s: %v; restarting in %s", id, err, delay)
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		delay = min(delay*2, h.or(h.MaxBackoff, DefaultMaxBackoff))
	}
}

// launch starts r's process and connects each of its kinds.
func (h *Host) launch(ctx context.Context, r Run) (plugin.Process, error) {
	p, err := h.Launcher.Launch(ctx, r.Spec)
	if err != nil {
		return nil, err
	}
	for _, k := range r.Manifest.Kinds {
		i := slices.IndexFunc(h.Kinds, func(a Kind) bool { return a.Kind() == k })
		if i < 0 {
			continue
		}
		c, err := p.Kind(k)
		if err == nil {
			err = h.Kinds[i].Start(ctx, r.Manifest, c)
		}
		if err != nil {
			p.Kill()
			return nil, fmt.Errorf("kind %s: %w", k, err)
		}
	}
	return p, nil
}

// watch waits until p ends, true, or ctx ends, false.
func (h *Host) watch(ctx context.Context, p plugin.Process) bool {
	t := time.NewTicker(h.or(h.Poll, DefaultPoll))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
			if p.Exited() {
				return true
			}
		}
	}
}

// stop gives each kind of r its Stop, then kills the process.
func (h *Host) stop(ctx context.Context, r Run, p plugin.Process) {
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), h.or(h.StopTimeout, DefaultStopTimeout))
	defer cancel()
	for _, k := range h.kindsOf(r) {
		k.Stop(stopCtx, r.Manifest.ID)
	}
	p.Kill()
	h.logf("plugin %s: stopped", r.Manifest.ID)
}

func (h *Host) down(r Run) {
	for _, k := range h.kindsOf(r) {
		k.Down(r.Manifest.ID)
	}
}

func (h *Host) kindsOf(r Run) []Kind {
	var out []Kind
	for _, k := range h.Kinds {
		if slices.Contains(r.Manifest.Kinds, k.Kind()) {
			out = append(out, k)
		}
	}
	return out
}

func (h *Host) or(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

func (h *Host) logf(format string, args ...any) {
	if h.Logf != nil {
		h.Logf(format, args...)
	}
}

var errProcessEnded = errors.New("the process ended")
