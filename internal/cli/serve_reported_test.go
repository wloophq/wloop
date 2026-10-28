package cli

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/plugins/host"
	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	"github.com/wloophq/wloop/pkg/plugin/plugintest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/pkg/plugin/source/sourcetest"
)

// reportingStub loads until it has reported its units once, and records
// the topics of every Notify.
type reportingStub struct {
	*sourcetest.Stub
	reported atomic.Bool
	mu       sync.Mutex
	topics   []plugin.Topic
}

func (s *reportingStub) Notify(ctx context.Context, units []plugin.Unit, topics []plugin.Topic) error {
	s.mu.Lock()
	s.topics = append(s.topics, topics...)
	s.mu.Unlock()
	return s.Stub.Notify(ctx, units, topics)
}

func (s *reportingStub) heard() []plugin.Topic {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.topics)
}

func (s *reportingStub) Health(context.Context) (plugin.Health, error) {
	if !s.reported.Load() {
		return plugin.Health{Status: plugin.StatusLoading, Reason: "reading the threads"}, nil
	}
	return plugin.Health{Status: plugin.StatusServing}, nil
}

func TestServeShowsAPluginsReportsAndNotifiesIt(t *testing.T) {
	te := newServeEnv(t)
	m, err := manifest.Parse([]byte("id = \"acme\"\nkinds = [\"source\"]\nprotocol = 1\n[source]\nname = \"Acme\"\nslot = \"acme\"\n" +
		"codec = {keys = [\"{name}\"]}\nnotify = [\"rounds\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	te.plugins = []host.Plugin{{Manifest: m, Dir: t.TempDir(), Binary: brand.PluginBinary("acme"), Bundled: true}}
	stub := &reportingStub{Stub: sourcetest.NewStub(src.Snapshot{Unit: plugin.Unit{Provider: "acme", Key: "one"}, Rev: 1,
		Situation: &src.Situation{Kind: "waiting", WaitingOn: "me", Reason: "ana asked"}, Card: &src.Card{Title: "deploy?"}})}
	l := plugintest.NewLauncher(t)
	l.Add("acme", map[plugin.Kind]plugin.Impl{src.Kind: src.New(stub)})
	te.pluginLauncher = l
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runServe(ctx, te.Env, []string{"--mcp-port", strconv.Itoa(freePort(t))}) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("serve returned %v", err)
			}
		case <-time.After(30 * time.Second):
			t.Error("serve did not stop")
		}
	}()
	waitHealth(t, te.Env, "the daemon up", func(api.Health) bool { return true })
	c := client.New(te.Socket)
	page := func() api.RowsPage {
		p, err := c.RowsSince(ctx, api.Since{})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	eventually(t, "the plugin says why it loads", func() bool {
		return slices.Equal(page().Loading, []api.Loading{{Plugin: "acme", Reason: "reading the threads"}})
	})

	if _, err := stub.Ingest(ctx, src.IngestRequest{Job: "threads"}); err != nil {
		t.Fatal(err)
	}
	stub.reported.Store(true)
	one := unit.Unit{Provider: "acme", Key: "one"}
	eventually(t, "the reported row, loaded", func() bool {
		p := page()
		return len(p.Loading) == 0 && slices.ContainsFunc(p.Rows, func(r api.Row) bool { return r.Unit == one && r.Title == "deploy?" && !r.Stale })
	})

	if err := c.SetPause(ctx, api.PauseRequest{Paused: true}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the plugin hears of the pause", func() bool { return len(stub.Notified()) > 0 })
	for _, tp := range stub.heard() {
		if tp != plugin.TopicRounds {
			t.Errorf("the plugin, subscribed to rounds only, heard of %s", tp)
		}
	}

	l.Crash("acme")
	eventually(t, "the crashed plugin's row stale", func() bool {
		return slices.ContainsFunc(page().Rows, func(r api.Row) bool { return r.Unit == one && r.Title == "deploy?" && r.Stale })
	})
}
