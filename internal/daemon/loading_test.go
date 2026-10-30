package daemon

import (
	"context"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/plugins/codec"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	"github.com/wloophq/wloop/pkg/plugin/plugintest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/pkg/plugin/source/sourcetest"
)

// importing is a plugin that loads until it has imported and reported.
type importing struct {
	*sourcetest.Stub
	done atomic.Bool
}

func (i *importing) Health(context.Context) (plugin.Health, error) {
	if !i.done.Load() {
		return plugin.Health{Status: plugin.StatusLoading, Reason: "importing import.db"}, nil
	}
	return plugin.Health{Status: plugin.StatusServing}, nil
}

func TestViewsSayLoadingUntilTheFirstFullReport(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "plugins.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	loaded := make(chan string, 1)
	k := &psource.Kind{Snapshots: psource.NewSnapshots(db.Snapshots()), StateDir: func(string) string { return t.TempDir() },
		HealthPoll: time.Millisecond, Loaded: func(id string) { loaded <- id },
		Broker: func(id string, s *manifest.Source) *psource.Broker {
			c, err := codec.Of(id, s.Codec)
			if err != nil {
				t.Fatal(err)
			}
			return &psource.Broker{ID: id, Manifest: s, Units: psource.Units{Codecs: unit.NewRegistry(c)},
				Config: func() config.Config { return config.Config{} }}
		}}
	k.Expect("acme")
	r := newTasksRig(t, func(d *Deps) { d.Loading = k.Loading })
	views := func(what string, want []api.Loading) {
		t.Helper()
		var rows api.RowsPage
		if code := get(t, r.s, api.Rows, &rows); code != 200 {
			t.Fatalf("%s: rows %d", what, code)
		}
		if tasks := r.page(t, "view=inbox"); !slices.Equal(rows.Loading, want) || !slices.Equal(tasks.Loading, want) {
			t.Fatalf("%s: rows say %v, tasks %v; want %v", what, rows.Loading, tasks.Loading, want)
		}
	}
	views("before the plugin runs", []api.Loading{{Plugin: "acme", Reason: psource.ReasonStarting}})

	stub := &importing{Stub: sourcetest.NewStub()}
	m, err := manifest.Parse([]byte("id = \"acme\"\nkinds = [\"source\"]\nprotocol = 1\n[source]\nname = \"Acme\"\nslot = \"acme\"\ncodec = {keys = [\"{name}\"]}\n"))
	if err != nil {
		t.Fatal(err)
	}
	l := plugintest.NewLauncher(t)
	l.Add("acme", map[plugin.Kind]plugin.Impl{src.Kind: src.New(stub)})
	p, err := l.Launch(t.Context(), plugin.Spec{ID: "acme", Kinds: []plugin.Kind{src.Kind}})
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
	t.Cleanup(func() { k.Stop(context.Background(), "acme") })
	deadline := time.Now().Add(30 * time.Second)
	for k.Loading()["acme"] != "importing import.db" {
		if time.Now().After(deadline) {
			t.Fatalf("never asked: %v", k.Loading())
		}
		time.Sleep(time.Millisecond)
	}
	views("while the plugin imports", []api.Loading{{Plugin: "acme", Reason: "importing import.db"}})

	stub.done.Store(true)
	select {
	case id := <-loaded:
		if id != "acme" {
			t.Fatalf("loaded %s", id)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("never loaded")
	}
	views("once it reported", nil)
	k.Down("acme")
	views("a later crash", nil)
}
