package readmodel_test

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin/builtintest"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/readmodel/overlay"
	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

var reportedAt = time.Date(2026, 10, 28, 15, 0, 0, 0, time.UTC)

// acmeRole installs the vocabulary of a plugin acme whose role acme.chase
// drives its row.
func acmeRole(t *testing.T) *intentload.Loaded {
	builtintest.Merge(t, &manifest.Source{Name: "Acme", Codec: manifest.Codec{Keys: []string{"{name}"}},
		SourceVocabulary: manifest.SourceVocabulary{Provider: "acme", Slot: "acme", Roles: []manifest.Role{{Name: "acme.chase",
			Doc: "Chases whoever a unit waits on.", Context: "pr", Policy: "round", DrivesRow: true}}}})
	return &intentload.Loaded{Registry: dintent.NewRegistry([]dintent.Def{{Name: "chase", DrivesRow: true, Answerable: true}})}
}

// reports are what the plugin reported, which the test never changes after
// the plugin stops: the rows below derive from them and the host's state.
type reports struct {
	snaps map[unit.Unit]src.Snapshot
	reads atomic.Int32
}

func (r *reports) Units(_ context.Context, p unit.ProviderID) ([]unit.Unit, error) {
	var out []unit.Unit
	for u := range r.snaps {
		if u.Provider == p {
			out = append(out, u)
		}
	}
	slices.SortFunc(out, api.CompareUnits)
	return out, nil
}

func (r *reports) Reported(_ context.Context, units []unit.Unit) (map[unit.Unit]src.Snapshot, error) {
	r.reads.Add(1)
	out := map[unit.Unit]src.Snapshot{}
	for _, u := range units {
		if s, ok := r.snaps[u]; ok {
			out[u] = s
		}
	}
	return out, nil
}

type mutes map[unit.Unit]overlay.Mute

func (m mutes) Mutes(_ context.Context, units []unit.Unit) (map[unit.Unit]overlay.Mute, error) {
	out := map[unit.Unit]overlay.Mute{}
	for _, u := range units {
		if x, ok := m[u]; ok {
			out[u] = x
		}
	}
	return out, nil
}

type books struct {
	mu sync.Mutex
	by map[unit.Unit]turn.Book
}

func (b *books) set(bk turn.Book) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.by[bk.Unit] = bk
}

func (b *books) Books(_ context.Context, units ...unit.Unit) (map[unit.Unit]turn.Book, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := map[unit.Unit]turn.Book{}
	for _, u := range units {
		if bk, ok := b.by[u]; ok {
			out[u] = bk
		}
	}
	return out, nil
}

type sessions map[string]turn.Session

func (s sessions) Sessions(context.Context) ([]turn.Session, error) {
	return slices.Collect(maps.Values(s)), nil
}

func (s sessions) Session(_ context.Context, handle string) (turn.Session, bool, error) {
	x, ok := s[handle]
	return x, ok, nil
}

func acmeUnit(key string) unit.Unit { return unit.Unit{Provider: "acme", Key: key} }

// report is acme's snapshot of key: a review waiting on the user for two
// hours, stalled after one.
func report(key string) src.Snapshot {
	since := reportedAt.Add(-2 * time.Hour)
	return src.Snapshot{Unit: plugin.Unit{Provider: "acme", Key: key}, Rev: 1, At: since,
		Situation: &src.Situation{Kind: "review", WaitingOn: "me", Since: since, Reason: "Bruno asked for your review",
			Stalled: src.After(since, time.Hour, src.Calendar)},
		Card: &src.Card{Label: "acme " + key, Title: "Ship the " + key, Link: "https://acme.example/" + key, Why: "Bruno asked for your review",
			WaitsOn: []src.Person{{Key: "acme:ana", Name: "Ana"}}, State: "open"},
		Views: []string{api.ViewNeedsMe}, Facets: map[string]string{"repo": "acme/api"}, Data: json.RawMessage(`{"size":3}`),
		Items: []src.Item{{ID: "i1", Kind: "request", Summary: "review the schema", State: "open", WaitingOn: []src.Person{{Key: "acme:ana"}}}}}
}

func reportedProvider(t *testing.T, r *reports, b *books, s sessions, m mutes, open []intake.Intake) *readmodel.Reported {
	l := acmeRole(t)
	return readmodel.NewReported(readmodel.ReportedSource{ID: "acme", Reports: r, Mutes: m,
		Viewer: func(context.Context) items.Person { return items.Person{Key: "acme:me", Name: "Me"} },
		RowDeps: readmodel.RowDeps{Books: b, Sessions: s,
			Intakes: func(context.Context) ([]intake.Intake, error) { return open, nil },
			Intake: func(i intake.Intake, _ intake.BriefState, _ func() (int, error)) api.Intake {
				return api.Intake{Seq: i.Seq, State: api.IntakeBriefing}
			},
			Settings: func() readmodel.RowSettings { return readmodel.RowSettings{Loaded: l} }}})
}

func TestARowIsDerivedFromItsSnapshotAndHostState(t *testing.T) {
	plain, muted, pending, briefing, gone := acmeUnit("plain"), acmeUnit("muted"), acmeUnit("pending"), acmeUnit("briefing"), acmeUnit("gone")
	r := &reports{snaps: map[unit.Unit]src.Snapshot{}}
	for _, u := range []unit.Unit{plain, muted, pending, briefing} {
		r.snaps[u] = report(u.Key)
	}
	p := reportedProvider(t, r, &books{by: map[unit.Unit]turn.Book{}}, nil,
		mutes{muted: {UntilStateChange: true, Kind: "review", WaitingOn: "me", At: reportedAt.Add(-time.Hour)}},
		[]intake.Intake{{Seq: 4, Unit: briefing}})
	ctx := context.Background()
	units, err := p.Listed(ctx)
	if err != nil || !slices.Equal(units, []unit.Unit{briefing, muted, pending, plain}) {
		t.Fatalf("listed %v %v", units, err)
	}
	ins, err := p.Load(ctx, append(units, gone))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ins[gone]; ok {
		t.Error("a unit with no snapshot has a row input")
	}
	base := readmodel.Base{Pending: map[unit.Unit]round.Pending{pending: {Intent: "chase", Reason: "Ana waits", Since: reportedAt.Add(-time.Minute),
		Deadline: reportedAt.Add(time.Hour)}}}
	row := func(u unit.Unit, in any) api.Row {
		t.Helper()
		r, err := p.Derive(base, in, reportedAt)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	got := row(plain, ins[plain])
	if got.Kind != "review" || got.Summary.WaitsOn != "me" || !got.Summary.Stalled || got.Summary.Reason != "Bruno asked for your review" ||
		got.Label != "acme plain" || got.Title != "Ship the plain" || got.Link != "https://acme.example/plain" ||
		!slices.Equal(got.Views, []string{api.ViewNeedsMe}) || got.Facets["repo"][0] != "acme/api" || string(got.Payload) != `{"size":3}` ||
		len(got.Items) != 1 || got.Items[0].WaitingOn[0] != "acme:ana" || got.Summary.OpenItems != 1 || got.Pending != nil {
		t.Errorf("the snapshot's row: %+v", got)
	}
	if early, _ := p.Derive(base, ins[plain], reportedAt.Add(-90*time.Minute)); early.Summary.Stalled {
		t.Error("stalled before its threshold: the host's clock resolves it")
	}

	got = row(muted, ins[muted])
	if got.Kind != overlay.KindMuted || got.Summary.WaitsOn != "nobody" || !got.Summary.Since.Equal(reportedAt.Add(-time.Hour)) || len(got.Views) != 0 {
		t.Errorf("the muted row: %+v", got)
	}
	if un := row(muted, readmodel.Unmuted(ins[muted])); un.Kind != "review" {
		t.Errorf("the unmuted variant: %+v", un)
	}

	got = row(pending, ins[pending])
	if got.Kind != overlay.KindRoundPending || got.Summary.Reason != "chase waiting for your answer: Ana waits" || got.Summary.Stalled ||
		got.Pending == nil || !got.Pending.Deadline.Equal(reportedAt.Add(time.Hour)) || !slices.Contains(got.Views, api.ViewNeedsMe) {
		t.Errorf("the pending round's row: %+v", got)
	}

	if got = row(briefing, ins[briefing]); got.Summary.Line != "briefing…" || got.Kind != "review" {
		t.Errorf("the row of an open intake: %+v", got.Summary)
	}
}

func TestATurnEndingRefreshesTheRowWithoutThePlugin(t *testing.T) {
	u := acmeUnit("one")
	r := &reports{snaps: map[unit.Unit]src.Snapshot{u: report("one")}}
	b := &books{by: map[unit.Unit]turn.Book{}}
	started := turn.Turn{Seq: 1, ID: "r1", Intent: "chase", Role: "acme.chase", State: turn.Started, Session: "s1",
		SentAt: reportedAt.Add(-10 * time.Minute), StartedAt: reportedAt.Add(-9 * time.Minute)}
	b.set(turn.Book{Unit: u, Turns: []turn.Turn{started}})
	p := reportedProvider(t, r, b, sessions{"s1": {ID: "s1", Pane: "p1"}}, nil, nil)
	log := change.NewLog(change.DefaultWindow, nil)
	agents := readmodel.AgentsFunc(func() ([]turns.AgentObservation, bool) {
		return []turns.AgentObservation{{Pane: "p1", State: turn.AgentWorking}}, true
	})
	m := readmodel.New(log, nil, nil, agents, nil, p)
	m.UseClock(func() time.Time { return reportedAt })
	ctx := context.Background()
	rowAndCard := func(what string) (api.Row, []items.Person, string) {
		t.Helper()
		row, ok, err := m.Row(ctx, u, catalog.Asked{})
		if err != nil || !ok {
			t.Fatalf("%s: no row: %v", what, err)
		}
		c, err := p.Card(ctx, row)
		if err != nil {
			t.Fatalf("%s: card: %v", what, err)
		}
		return row, c.WaitsOn, c.Why
	}

	row, waits, why := rowAndCard("started")
	if row.Kind != overlay.KindLoopWorking || row.Summary.Reason != "chase round in progress" || slices.Contains(row.Views, api.ViewNeedsMe) ||
		why != "chase round in progress" || len(waits) != 1 || waits[0].Key != "acme:me" {
		t.Fatalf("a started run's row %+v, card %q %v", row, why, waits)
	}

	asked := started
	asked.State, asked.EndedAt = turn.Asked, reportedAt.Add(-time.Minute)
	b.set(turn.Book{Unit: u, Turns: []turn.Turn{asked}, Records: []turn.Record{{ID: 7, TurnSeq: 1, At: reportedAt.Add(-time.Minute),
		Record: record.Record{Summary: "Which schema version?", Question: &record.Question{Kind: "choice", Text: "Which schema version?"}}}}})
	log.Note(port.TopicTurns, u)
	row, waits, why = rowAndCard("asked")
	if row.Kind != overlay.KindLoopWaiting || row.Summary.Reason != "Which schema version?" || row.Question == nil ||
		row.Question.Text != "Which schema version?" || !slices.Contains(row.Views, api.ViewNeedsMe) || why != "Which schema version?" || len(waits) != 1 {
		t.Fatalf("an asking run's row %+v, card %q %v", row, why, waits)
	}

	done := started
	done.State, done.EndedAt = turn.Done, reportedAt
	b.set(turn.Book{Unit: u, Turns: []turn.Turn{done}})
	log.Note(port.TopicTurns, u)
	row, waits, why = rowAndCard("ended")
	if row.Kind != "review" || row.Summary.Reason != "Bruno asked for your review" || row.Question != nil ||
		why != "Bruno asked for your review" || len(waits) != 1 || waits[0].Key != "acme:ana" {
		t.Fatalf("an ended run left its overlay: row %+v, card %q %v", row, why, waits)
	}
	if s := r.snaps[u]; s.Rev != 1 {
		t.Errorf("the plugin reported again: rev %d", s.Rev)
	}
}
