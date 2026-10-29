package source_test

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/plugins/codec"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/pkg/plugin/source/sourcetest"
)

// hostOver is the broker of plugin acme over a fresh store's events, marks,
// drafts and overrides.
func hostOver(t *testing.T) (src.Host, *sqlite.DB) {
	t.Helper()
	b, db := brokerOver(t)
	return b.Host(), db
}

func brokerOver(t *testing.T) (*psource.Broker, *sqlite.DB) {
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
	b := &psource.Broker{ID: "acme", Units: psource.Units{Codecs: unit.NewRegistry(c)}, Events: sqlite.New(db), Marks: db, Drafts: db, Overrides: db}
	return b, db
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

func TestTheBrokerSetsOverridesCompareAndSetAndReadsThemInTheRoundState(t *testing.T) {
	t.Parallel()
	h, db := hostOver(t)
	ctx := context.Background()
	own := plugin.Unit{Provider: "acme", Key: "t1"}
	guide := map[string]string{port.OverrideNextGuidance: "ask ana"}
	if ok, err := h.Gates.SetOverride(ctx, own, guide, map[string]string{port.OverrideNextGuidance: ""}); err != nil || !ok {
		t.Fatalf("guidance %v %v", ok, err)
	}
	if ok, err := h.Gates.SetOverride(ctx, own, map[string]string{port.OverrideNextGuidance: ""}, map[string]string{port.OverrideNextGuidance: "ask bruno"}); err != nil || ok {
		t.Fatalf("a stale match wrote %v %v", ok, err)
	}
	st, err := h.Reader.RoundState(ctx, own)
	if err != nil || st.Overrides[port.OverrideNextGuidance] != "ask ana" || st.Paused {
		t.Fatalf("round state %+v %v", st, err)
	}
	if _, err := h.Gates.SetOverride(ctx, own, map[string]string{port.OverrideHold: port.OverrideTrue}, nil); err == nil {
		t.Error("a plugin set the user's hold")
	}
	if _, err := h.Gates.SetOverride(ctx, plugin.Unit{Provider: "slack", Key: "T/C/1"}, guide, nil); err == nil {
		t.Error("a plugin set another provider's override")
	}
	if _, err := db.SetOverride(ctx, unit.Unit{Provider: "acme", Key: "t1"}, map[string]string{port.OverrideHold: port.OverrideTrue}, nil); err != nil {
		t.Fatal(err)
	}
	if st, err := h.Reader.RoundState(ctx, own); err != nil || !st.Paused {
		t.Errorf("the hold reaches the plugin as %+v %v, want paused", st, err)
	}
}

func TestAWiderPauseReachesThePluginAsPaused(t *testing.T) {
	t.Parallel()
	b, _ := brokerOver(t)
	held := unit.Unit{Provider: "acme", Key: "t1"}
	b.Paused = func(_ context.Context, u unit.Unit) (bool, error) { return u == held, nil }
	h := b.Host()
	ctx := context.Background()
	for key, want := range map[string]bool{"t1": true, "t2": false} {
		if st, err := h.Reader.RoundState(ctx, plugin.Unit{Provider: "acme", Key: key}); err != nil || st.Paused != want {
			t.Errorf("%s: round state %+v %v, want paused %v", key, st, err, want)
		}
	}
}

func TestRecordEventsKeepsTheAnchor(t *testing.T) {
	t.Parallel()
	b, _ := brokerOver(t)
	stub := sourcetest.NewStub()
	sourcetest.Launch(t, "acme", stub, b.Host())
	h := stub.Host()
	ctx := context.Background()
	own := plugin.Unit{Provider: "acme", Key: "t1"}
	at := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	anchor := src.Anchor{Unit: own, Ref: "comment:c7", Permalink: "https://example.com/t1#c7", At: at.Add(-time.Minute)}
	evs := []src.Event{{Unit: own, Kind: "comment", Key: "c7", Actor: "bruno", At: at, Anchor: anchor}, {Unit: own, Kind: "push", Key: "h2", At: at}}
	if err := h.Recorder.RecordEvents(ctx, evs); err != nil {
		t.Fatal(err)
	}
	got, err := h.History.Events(ctx, src.EventQuery{Units: []plugin.Unit{own}})
	if err != nil || len(got) != 2 {
		t.Fatalf("events %+v %v", got, err)
	}
	for _, e := range got {
		want := src.Anchor{}
		if e.Kind == "comment" {
			want = anchor
		}
		if e.Anchor.Unit != want.Unit || e.Anchor.Ref != want.Ref || e.Anchor.Permalink != want.Permalink || !e.Anchor.At.Equal(want.At) {
			t.Errorf("%s: anchor %+v, want %+v", e.Kind, e.Anchor, want)
		}
	}
}

func TestTheRoundStateCrossesTheWireWithItsBookkeeping(t *testing.T) {
	t.Parallel()
	b, db := brokerOver(t)
	ctx := context.Background()
	sent := time.Date(2026, 10, 28, 8, 0, 0, 0, time.UTC)
	since := sent.Add(30 * time.Minute)
	want := src.RoundState{Marks: map[string]string{"plugin.x": "1", "reviewed": "f1"}, SentAt: sent, AgentHome: "/homes/ana", Today: 2, DayTotal: 5,
		Blocked: &src.Blocked{Question: "trust", Root: "/src/api", Folder: "/wt/rev", Since: since}}
	t1 := unit.Unit{Provider: "acme", Key: "t1"}
	b.Rounds = func(_ context.Context, u unit.Unit) (src.RoundState, error) {
		if u != t1 {
			return src.RoundState{}, nil
		}
		return want, nil
	}
	if _, err := db.SetOverride(ctx, t1, map[string]string{port.OverrideNextGuidance: "ask ana", port.OverrideHold: port.OverrideTrue}, nil); err != nil {
		t.Fatal(err)
	}
	stub := sourcetest.NewStub()
	sourcetest.Launch(t, "acme", stub, b.Host())
	got, err := stub.Host().Reader.RoundState(ctx, plugin.Unit{Provider: "acme", Key: "t1"})
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(got.Marks, want.Marks) || !got.SentAt.Equal(sent) || got.AgentHome != want.AgentHome || got.Today != 2 || got.DayTotal != 5 {
		t.Errorf("bookkeeping %+v", got)
	}
	if got.Blocked == nil || got.Blocked.Question != "trust" || got.Blocked.Root != "/src/api" || got.Blocked.Folder != "/wt/rev" || !got.Blocked.Since.Equal(since) {
		t.Errorf("blocked %+v", got.Blocked)
	}
	if got.Overrides[port.OverrideNextGuidance] != "ask ana" || !got.Paused {
		t.Errorf("overrides %v, paused %v; want them over the bookkeeping", got.Overrides, got.Paused)
	}
}

func TestBooksServeEachTurnAtWhenItReachedItsState(t *testing.T) {
	t.Parallel()
	b, _ := brokerOver(t)
	sent := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	t1 := unit.Unit{Provider: "acme", Key: "t1"}
	b.Books = func(_ context.Context, units ...unit.Unit) (map[unit.Unit]turn.Book, error) {
		if !slices.Equal(units, []unit.Unit{t1}) {
			return nil, fmt.Errorf("books of %v", units)
		}
		return map[unit.Unit]turn.Book{t1: {Unit: t1, Turns: []turn.Turn{
			{Seq: 1, ID: "a", Intent: "address", Role: "pr.address", State: turn.Done, SentAt: sent, StartedAt: sent.Add(time.Minute)},
			{Seq: 2, ID: "b", Intent: "review", Role: "pr.review", State: turn.Started, SentAt: sent.Add(time.Hour), StartedAt: sent.Add(61 * time.Minute)},
			{Seq: 3, ID: "c", Intent: "review", Role: "pr.review", State: turn.Sent, SentAt: sent.Add(2 * time.Hour)},
		}, Records: []turn.Record{{ID: 9, TurnSeq: 1, Head: "h7", At: sent.Add(30 * time.Minute), Record: record.Record{Summary: "pushed"}}}}}, nil
	}
	stub := sourcetest.NewStub()
	sourcetest.Launch(t, "acme", stub, b.Host())
	books, err := stub.Host().Reader.Books(context.Background(), []plugin.Unit{{Provider: "acme", Key: "t1"}})
	if err != nil || len(books) != 1 || len(books[0].Turns) != 3 || len(books[0].Records) != 1 {
		t.Fatalf("books %+v %v", books, err)
	}
	for i, want := range []time.Time{sent.Add(30 * time.Minute), sent.Add(61 * time.Minute), sent.Add(2 * time.Hour)} {
		if got := books[0].Turns[i]; !got.At.Equal(want) || got.Role == "" {
			t.Errorf("turn %d at %v, want %v: %+v", got.Seq, got.At, want, got)
		}
	}
	if s := books[0].Turns[0].Summary; s != "pushed" {
		t.Errorf("summary %q", s)
	}
	if _, err := stub.Host().Reader.Books(context.Background(), []plugin.Unit{{Provider: "slack", Key: "T/C/1"}}); err == nil {
		t.Error("a plugin read another provider's books")
	}
}
