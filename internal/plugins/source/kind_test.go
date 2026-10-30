package source_test

import (
	"context"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/plugins/codec"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	"github.com/wloophq/wloop/pkg/plugin/plugintest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/pkg/plugin/source/sourcetest"
)

// kindOver is the source kind over a fresh store, whose brokers note in log.
func kindOver(t *testing.T, log *change.Log) *psource.Kind {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	snaps := psource.NewSnapshots(db.Snapshots())
	return &psource.Kind{
		Snapshots: snaps,
		StateDir:  func(string) string { return t.TempDir() },
		Broker: func(id string, s *manifest.Source) *psource.Broker {
			c, err := codec.Of(id, s.Codec)
			if err != nil {
				t.Fatal(err)
			}
			return &psource.Broker{ID: id, Manifest: s, Snapshots: snaps, Units: psource.Units{Codecs: unit.NewRegistry(c)},
				Config: func() config.Config { return config.Config{} }, Note: func(tp port.Topic, us ...unit.Unit) { log.Note(tp, us...) }}
		},
	}
}

// start launches impl in-process as plugin id, whose [source] table adds
// extra, and starts it through k; the test's cleanup stops it.
func start(t *testing.T, k *psource.Kind, id, extra string, impl src.Source) {
	t.Helper()
	m, err := manifest.Parse([]byte("id = \"" + id + "\"\nkinds = [\"source\"]\nprotocol = 1\n[source]\nname = \"Acme\"\nslot = \"" + id +
		"\"\ncodec = {keys = [\"{name}\"]}\n" + extra))
	if err != nil {
		t.Fatal(err)
	}
	l := plugintest.NewLauncher(t)
	l.Add(id, map[plugin.Kind]plugin.Impl{src.Kind: src.New(impl)})
	p, err := l.Launch(t.Context(), plugin.Spec{ID: id, Kinds: []plugin.Kind{src.Kind}})
	if err != nil {
		t.Fatal(err)
	}
	c, err := p.Kind(src.Kind)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Start(t.Context(), m, c); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { k.Stop(context.Background(), id) })
}

// eventually polls cond; its deadline only guards a hang.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("never: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// listener is a stub that records the topics of every Notify too.
type listener struct {
	*sourcetest.Stub
	mu     sync.Mutex
	topics [][]plugin.Topic
}

func (l *listener) Notify(ctx context.Context, units []plugin.Unit, topics []plugin.Topic) error {
	l.mu.Lock()
	l.topics = append(l.topics, slices.Clone(topics))
	l.mu.Unlock()
	return l.Stub.Notify(ctx, units, topics)
}

func (l *listener) heard() [][]plugin.Topic {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.topics)
}
