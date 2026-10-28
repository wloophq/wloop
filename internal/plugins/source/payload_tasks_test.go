package source_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/timeline"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/plugins/codec"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/pkg/plugin/source/sourcetest"
)

// briefHost is a running plugin acme over a real store, whose tasks' brief
// records project risk; briefed are the units it told were briefed.
func briefHost(t *testing.T) (*sqlite.DB, *sourcetest.Stub, *briefedUnits) {
	t.Helper()
	db, stub, briefed, _ := briefHostOver(t, nil)
	return db, stub, briefed
}

// briefHostOver is briefHost over a store whose writes log notes, with its plugin kind.
func briefHostOver(t *testing.T, log *change.Log) (*sqlite.DB, *sourcetest.Stub, *briefedUnits, *psource.Kind) {
	t.Helper()
	var notes sqlite.Noter
	if log != nil {
		notes = log
	}
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "store.db"), notes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	records := &brief.Records{Holders: db, Props: db.Props(func() time.Time { return at0 }), Projection: func() brief.Projection {
		return brief.Projection{Projects: map[string]string{"risk": "risk"}, Parse: func(prop, text string, w task.Writer) (task.Value, bool) {
			return task.Value{Text: text}, prop == "risk" && w == task.WriterPlugin
		}}
	}}
	briefed := &briefedUnits{}
	snaps := psource.NewSnapshots(db.Snapshots())
	k := &psource.Kind{Snapshots: snaps, StateDir: func(string) string { return t.TempDir() },
		Broker: func(id string, s *manifest.Source) *psource.Broker {
			c, err := codec.Of(id, s.Codec)
			if err != nil {
				t.Fatal(err)
			}
			return &psource.Broker{ID: id, Manifest: s, Snapshots: snaps, Units: psource.Units{Codecs: unit.NewRegistry(c)},
				Config: func() config.Config { return config.Config{} }, Briefs: db, BriefRecords: records, Briefed: briefed.add, Now: func() time.Time { return at0 }}
		}}
	stub := sourcetest.NewStub()
	start(t, k, "acme", "", stub)
	return db, stub, briefed, k
}

type briefedUnits struct {
	mu    sync.Mutex
	units []unit.Unit
}

func (b *briefedUnits) add(u unit.Unit) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.units = append(b.units, u)
}

func (b *briefedUnits) list() []unit.Unit {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.units)
}

// holdTask creates task id holding u, or u's items when items are named.
func holdTask(t *testing.T, db *sqlite.DB, id task.ID, u unit.Unit, items ...string) {
	t.Helper()
	tk := task.Task{ID: id, State: "inbox", Category: task.CategoryInbox, Title: string(id), Origin: task.OriginDerived,
		Created: at0, Updated: at0}
	if len(items) == 0 {
		tk.Units = []unit.Unit{u}
	}
	for _, it := range items {
		tk.Items = append(tk.Items, task.ItemRef{Unit: u, Item: it})
	}
	if err := db.Tasks().Apply(context.Background(), []task.Change{{Op: task.OpCreate, Task: tk}}); err != nil {
		t.Fatal(err)
	}
}

func briefOf(t *testing.T, db *sqlite.DB, u unit.Unit) (map[string]any, bool) {
	t.Helper()
	recs, err := db.Records(context.Background(), "brief", u)
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := recs[u]
	if !ok {
		return nil, false
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(rec.Data), &out); err != nil {
		t.Fatal(err)
	}
	if rec.Actor != string(task.WriterPlugin) {
		t.Fatalf("a plugin's brief written by %q", rec.Actor)
	}
	return out, true
}

func TestAPutBriefOnATaskThatDoesNotHoldTheUnitIsRefused(t *testing.T) {
	db, stub, briefed := briefHost(t)
	ctx := context.Background()
	u := unit.Unit{Provider: "acme", Key: "t1"}
	holder, other := task.ID("t-00000000000000a1"), task.ID("t-00000000000000b2")
	holdTask(t, db, holder, u)
	holdTask(t, db, other, unit.Unit{Provider: "acme", Key: "t2"})
	body := json.RawMessage(`{"goal":"Ship the export","risk":"high"}`)
	if err := stub.Host().PutBrief(ctx, src.Brief{Unit: plugin.Unit{Provider: "acme", Key: "t1"}, Task: string(other), Version: "x1", Body: body}); err == nil {
		t.Fatal("a brief on a task that does not hold the unit is kept")
	}
	if _, ok := briefOf(t, db, task.UnitOf(other)); ok {
		t.Fatal("the refused brief is a record")
	}
	if err := stub.Host().PutBrief(ctx, src.Brief{Unit: plugin.Unit{Provider: "acme", Key: "t1"}, Task: string(holder), Version: "x1", Body: body}); err != nil {
		t.Fatal(err)
	}
	if b, ok := briefOf(t, db, task.UnitOf(holder)); !ok || b["goal"] != "Ship the export" {
		t.Fatalf("the holder's brief %v %v", b, ok)
	}
	if got := briefed.list(); !slices.Equal(got, []unit.Unit{u}) {
		t.Fatalf("told briefed %v, want the unit once, for the holder's brief", got)
	}
	props, err := db.Props(nil).PropsOf(ctx, holder)
	if err != nil || props["risk"].Text != "high" {
		t.Fatalf("the holder's risk %+v %v", props, err)
	}
}

func TestAThreadWithTwoTasksWritesTwoBriefs(t *testing.T) {
	db, stub, _ := briefHost(t)
	ctx := context.Background()
	thread := unit.Unit{Provider: "acme", Key: "t1"}
	export, invoice := task.ID("t-00000000000000a1"), task.ID("t-00000000000000b2")
	holdTask(t, db, export, thread, "i1")
	holdTask(t, db, invoice, thread, "i2")
	body := json.RawMessage(`{"goal":"Two asks in one thread","risk":"low","asks":[
		{"item":"i1","ask":"Can you export the ledger?","asker":"Ana","permalink":"https://chat.example/p1","quotes":[{"ref":"1.1","text":"export it?"}]},
		{"item":"i2","ask":"Who sends the invoice?","asker":"Bruno","deadline":"Friday","missing":"the amount"}]}`)
	if err := stub.Host().PutBrief(ctx, src.Brief{Unit: plugin.Unit{Provider: "acme", Key: "t1"}, Version: "x1", Body: body}); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[task.ID]string{export: "Can you export the ledger?", invoice: "Who sends the invoice?"} {
		b, ok := briefOf(t, db, task.UnitOf(id))
		asks, _ := b["asks"].([]any)
		if !ok || len(asks) != 1 || asks[0].(map[string]any)["ask"] != want || b["goal"] != "Two asks in one thread" {
			t.Fatalf("%s's brief %v", id, b)
		}
	}
	if b, ok := briefOf(t, db, thread); !ok || len(b["asks"].([]any)) != 2 {
		t.Fatalf("the thread's brief %v", b)
	}
	if evs, _ := db.EventsOf(ctx, []unit.Unit{task.UnitOf(export), task.UnitOf(invoice)}, timelineRecords); len(evs) != 2 {
		t.Fatalf("%d brief records for two tasks", len(evs))
	}
	ids := []task.ID{}
	for _, id := range []task.ID{export, invoice} {
		if p, _ := db.Props(nil).PropsOf(ctx, id); p["risk"].Text == "low" {
			ids = append(ids, id)
		}
	}
	if !slices.Equal(ids, []task.ID{export, invoice}) {
		t.Fatalf("the risk projected onto %v", ids)
	}
}

var timelineRecords = timeline.Filter{Kinds: []string{timeline.KindRecord}}
