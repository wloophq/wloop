package source_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/plugins/codec"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// hostOver is the broker of plugin acme over a fresh store's events, marks
// and drafts.
func hostOver(t *testing.T) (src.Host, *sqlite.DB) {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c, err := codec.Of("acme", manifest.Codec{Keys: []string{"{thread}"}})
	if err != nil {
		t.Fatal(err)
	}
	b := &psource.Broker{ID: "acme", Units: psource.Units{Codecs: unit.NewRegistry(c)}, Events: sqlite.New(db), Marks: db, Drafts: db}
	return b.Host(), db
}

func TestTheBrokerKeepsAPluginsEventsAndReadsThemBack(t *testing.T) {
	t.Parallel()
	h, _ := hostOver(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	own := plugin.Unit{Provider: "acme", Key: "t1"}
	ev := src.Event{Unit: own, Kind: "comment", Key: "c1", Actor: "ana", At: at, Data: []byte(`{"n":1}`)}
	for range 2 {
		if err := h.Recorder.RecordEvents(ctx, []src.Event{ev}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := h.History.Events(ctx, src.EventQuery{Units: []plugin.Unit{own}})
	if err != nil || len(got) != 1 || got[0].Key != "c1" || string(got[0].Data) != `{"n":1}` || !got[0].At.Equal(at) {
		t.Fatalf("read back %+v %v, want the one event", got, err)
	}
	other := plugin.Unit{Provider: "slack", Key: "T/C/1"}
	if err := h.Recorder.RecordEvents(ctx, []src.Event{{Unit: other, Kind: "comment", At: at}}); err == nil {
		t.Error("recorded an event on another provider's unit")
	}
	if _, err := h.History.Events(ctx, src.EventQuery{Units: []plugin.Unit{other}}); err == nil {
		t.Error("read the events of another provider's unit")
	}
	if err := h.Recorder.SetMarks(ctx, own, map[string]string{"seen": "c1"}); err != nil {
		t.Fatal(err)
	}
}

func TestTheBrokerRehomesAPluginsDrafts(t *testing.T) {
	t.Parallel()
	h, db := hostOver(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	d, err := db.Drafts().Insert(ctx, draft.Draft{Unit: unit.Unit{Provider: "acme", Key: "t1"}, Kind: draft.Reply, Text: "hi",
		Origin: draft.Origin{Kind: draft.OriginSession}, State: draft.Sent, CreatedAt: at, UpdatedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	moves := []src.UnitMove{{From: plugin.Unit{Provider: "acme", Key: "t1"}, To: plugin.Unit{Provider: "acme", Key: "t2"}}}
	for i, want := range []int{1, 0} {
		n, err := h.Gates.RehomeDrafts(ctx, moves)
		if err != nil || n != want {
			t.Fatalf("move %d moved %d (%v), want %d", i, n, err, want)
		}
	}
	if got, err := db.Drafts().Get(ctx, d.ID); err != nil || got.Unit.Key != "t2" {
		t.Errorf("the draft sits on %v (%v), want acme:t2", got.Unit, err)
	}
	if _, err := h.Gates.RehomeDrafts(ctx, []src.UnitMove{{From: plugin.Unit{Provider: "acme", Key: "t2"}, To: plugin.Unit{Provider: "slack", Key: "T/C/1"}}}); err == nil {
		t.Error("moved a draft onto another provider's unit")
	}
}
