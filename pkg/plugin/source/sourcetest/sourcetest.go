// Package sourcetest runs source plugins in-process, for tests on either
// side of the source protocol: [Stub] is a source that reports a scripted
// snapshot, and [Launch] connects any source to a host over a real go-plugin
// connection inside the test binary.
package sourcetest

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/plugintest"
	"github.com/wloophq/wloop/pkg/plugin/source"
)

// Stub is a source whose Ingest reports Snapshots to the host. Embed it to
// add capabilities.
type Stub struct {
	mu        sync.Mutex
	snapshots []source.Snapshot
	host      source.Host
	started   bool
	notes     [][]plugin.Unit
}

var (
	_ source.Source   = (*Stub)(nil)
	_ source.Ingester = (*Stub)(nil)
)

// NewStub is a Stub that reports snapshots.
func NewStub(snapshots ...source.Snapshot) *Stub { return &Stub{snapshots: snapshots} }

// Script replaces the snapshots the next Ingest reports.
func (s *Stub) Script(snapshots ...source.Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots = snapshots
}

// Host is the broker Start received.
func (s *Stub) Host() source.Host {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.host
}

// Notified are the units of every Notify, oldest first.
func (s *Stub) Notified() [][]plugin.Unit {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.notes)
}

func (s *Stub) Health(context.Context) (plugin.Health, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return plugin.Health{Status: plugin.StatusLoading, Reason: "not started"}, nil
	}
	return plugin.Health{Status: plugin.StatusServing}, nil
}

func (s *Stub) Start(_ context.Context, req source.StartRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.host, s.started = req.Host, true
	return nil
}

func (s *Stub) Stop(context.Context) error { return nil }

func (s *Stub) Notify(_ context.Context, units []plugin.Unit, _ []plugin.Topic) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notes = append(s.notes, slices.Clone(units))
	return nil
}

func (s *Stub) Ingest(ctx context.Context, _ source.IngestRequest) (source.IngestResult, error) {
	s.mu.Lock()
	host, snaps := s.host, slices.Clone(s.snapshots)
	s.mu.Unlock()
	if host.Reporter == nil {
		return source.IngestResult{}, errors.New("sourcetest: not started")
	}
	if err := host.Report(ctx, snaps); err != nil {
		return source.IngestResult{}, err
	}
	return source.IngestResult{Summary: "reported"}, nil
}

func (s *Stub) Track(_ context.Context, req source.TrackRequest) (source.Tracked, error) {
	return source.Tracked{Unit: req.Unit}, nil
}

func (s *Stub) Untrack(context.Context, plugin.Unit) error { return nil }

func (s *Stub) Resolve(context.Context, string) (plugin.Unit, bool, error) {
	return plugin.Unit{}, false, nil
}

func (s *Stub) Locate(context.Context, string) (plugin.Unit, bool, error) {
	return plugin.Unit{}, false, nil
}

// Launch serves src as plugin id in-process, serves host to it, starts it
// and returns the host's client. The test's cleanup stops it.
func Launch(t testing.TB, id string, src source.Source, host source.Host) *source.Client {
	t.Helper()
	l := plugintest.NewLauncher(t)
	l.Add(id, map[plugin.Kind]plugin.Impl{source.Kind: source.New(src)})
	p, err := l.Launch(t.Context(), plugin.Spec{ID: id, Kinds: []plugin.Kind{source.Kind}})
	if err != nil {
		t.Fatal(err)
	}
	k, err := p.Kind(source.Kind)
	if err != nil {
		t.Fatal(err)
	}
	hs, err := source.ServeHost(k, host)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = k.Stop(context.Background())
		hs.Close()
	})
	if err := k.Start(t.Context(), plugin.StartRequest{StateDir: t.TempDir(), HostBroker: hs.ID()}); err != nil {
		t.Fatal(err)
	}
	return source.NewClient(k)
}
