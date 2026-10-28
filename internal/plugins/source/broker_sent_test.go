package source_test

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/outbox/outboxtest"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/plugins/codec"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

type memDrafts struct{ ds []draft.Draft }

func (m *memDrafts) Insert(_ context.Context, d draft.Draft) (draft.Draft, error) {
	d.ID = draft.ID(len(m.ds) + 1)
	m.ds = append(m.ds, d)
	return d, nil
}

func (m *memDrafts) Get(_ context.Context, id draft.ID) (draft.Draft, error) {
	if id < 1 || int(id) > len(m.ds) {
		return draft.Draft{}, outbox.ErrNotFound
	}
	return m.ds[id-1], nil
}

func (m *memDrafts) List(context.Context, outbox.Filter) ([]draft.Draft, error) { return m.ds, nil }

func (m *memDrafts) Update(_ context.Context, d draft.Draft, _ draft.State) (draft.Draft, error) {
	m.ds[d.ID-1] = d
	return d, nil
}

func TestSlackToolPostIsARecordedSentGate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, err := manifest.Load(filepath.Join("..", "..", "..", "plugins", "slack"))
	if err != nil {
		t.Fatal(err)
	}
	sm, err := m.Source()
	if err != nil {
		t.Fatal(err)
	}
	c, err := codec.Of("slack", sm.Codec)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	now := func() time.Time { return at }
	gs := outboxtest.NewGates(now)
	store := &memDrafts{}
	ob := gs.Bind(&outbox.Service{Store: store, Clock: clockOf(now),
		Effects: func(unit.ProviderID) (port.Effects, error) { return nil, outbox.ErrDisabled }})
	thread := unit.Unit{Provider: "slack", Key: "T000AAA/C000AAA/1700000000.000100"}
	book := dturn.Book{Unit: thread, Turns: []dturn.Turn{{Seq: 3, Intent: "reply", Session: "live"}}}
	b := &psource.Broker{ID: "slack", Units: psource.Units{Codecs: unit.NewRegistry(c)}, Manifest: sm,
		Hosted: &psource.Hosted{Drafts: ob, Sessions: gateSessions{sessions: map[string]dturn.Session{"live": {ID: "live"}}, book: book}}}
	g := b.Host().Gates
	posted := src.Draft{Unit: plugin.Unit{Provider: "slack", Key: thread.Key}, Purpose: "reply", Delivery: "message", Text: "on it",
		Evidence: src.Evidence{Ref: "C000AAA/1700000000.000200"}}

	ref, err := g.RecordSent(ctx, posted, "live", "slack_send_message")
	if err != nil {
		t.Fatal(err)
	}
	d, err := store.Get(ctx, draft.ID(ref.Draft))
	if err != nil || d.State != draft.Sent || d.Origin.Kind != draft.OriginSession || d.Origin.TurnSeq != 3 {
		t.Fatalf("recorded %+v %v", d, err)
	}
	gt, ok, err := ob.GateOf(ctx, d)
	if err != nil || !ok {
		t.Fatalf("the post has no gate: %v", err)
	}
	if gt.Kind != gate.KindDraft || gt.Status != gate.Approved || gt.By != task.ActorAgent || gt.Rule != "slack_send_message" ||
		gt.Intent != "approve" || gt.Subject != strconv.FormatInt(int64(d.ID), 10) || gt.Unit != thread {
		t.Fatalf("the post's gate %+v", gt)
	}
	if open, _ := gs.Where(ctx, outboxGateQuery()); len(open) != 0 {
		t.Fatalf("a post made at once left an open gate: %+v", open)
	}
	if _, err := g.RecordSent(ctx, posted, "live", "slack_read"); err == nil {
		t.Fatal("a tool that posts nothing recorded a sent gate")
	}
}

type clockOf func() time.Time

func (c clockOf) Now() time.Time { return c() }

func outboxGateQuery() gates.Query { return gates.Query{Kind: gate.KindDraft} }
