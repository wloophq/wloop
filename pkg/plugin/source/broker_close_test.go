package source_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/plugintest"
	"github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/pkg/plugin/source/sourcetest"
)

// slowReporter holds each Report until release closes.
type slowReporter struct {
	started  chan struct{}
	release  chan struct{}
	finished atomic.Bool
}

func (r *slowReporter) Report(ctx context.Context, _ []source.Snapshot) error {
	close(r.started)
	<-r.release
	r.finished.Store(true)
	return nil
}

func (r *slowReporter) Changed(context.Context, []plugin.Unit, []plugin.Topic) error { return nil }

func TestHostServerCloseWaitsForAHostCallInFlight(t *testing.T) {
	stub := sourcetest.NewStub(source.Snapshot{Unit: pr, Rev: 1})
	l := plugintest.NewLauncher(t)
	l.Add("pulls", map[plugin.Kind]plugin.Impl{source.Kind: source.New(stub)})
	p, err := l.Launch(t.Context(), plugin.Spec{ID: "pulls", Kinds: []plugin.Kind{source.Kind}})
	if err != nil {
		t.Fatal(err)
	}
	k, err := p.Kind(source.Kind)
	if err != nil {
		t.Fatal(err)
	}
	rep := &slowReporter{started: make(chan struct{}), release: make(chan struct{})}
	hs, err := source.ServeHost(k, source.Host{Reporter: rep})
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Start(t.Context(), plugin.StartRequest{StateDir: t.TempDir(), HostBroker: hs.ID()}); err != nil {
		t.Fatal(err)
	}
	c := source.NewClient(k)
	ingested := make(chan struct{})
	go func() {
		defer close(ingested)
		_, _ = c.Ingest(context.Background(), source.IngestRequest{Job: "ingest"})
	}()
	<-rep.started
	var finishedAtClose atomic.Bool
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		hs.Close()
		finishedAtClose.Store(rep.finished.Load())
	}()
	// A Close that does not wait for handlers returns within this window.
	select {
	case <-closed:
	case <-time.After(250 * time.Millisecond):
	}
	close(rep.release)
	<-closed
	<-ingested
	_ = k.Stop(context.Background())
	if !finishedAtClose.Load() {
		t.Error("Close returned while a Report it served was still running")
	}
}
