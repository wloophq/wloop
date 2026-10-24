package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func init() {
	reads = append(reads, "DB.Drafts", "draftStore.Get", "draftStore.List")
	writes = append(writes,
		writeCase{
			method: "draftStore.Insert",
			call: func(ctx context.Context, e *noteEnv) error {
				_, err := e.db.Drafts().Insert(ctx, sampleDraft(slackThread, noteAt))
				return err
			},
			topics: port.TopicDrafts, units: []unit.Unit{slackThread},
		},
		writeCase{
			method: "draftStore.Update",
			seed: func(t *testing.T, e *noteEnv) {
				if _, err := e.db.Drafts().Insert(context.Background(), sampleDraft(ghPull, noteAt)); err != nil {
					t.Fatal(err)
				}
			},
			call: func(ctx context.Context, e *noteEnv) error {
				d, err := e.db.Drafts().Get(ctx, 1)
				if err != nil {
					return err
				}
				d.State = draft.Approved
				_, err = e.db.Drafts().Update(ctx, d, draft.Proposed)
				return err
			},
			topics: port.TopicDrafts, units: []unit.Unit{ghPull},
		},
	)
	hotPlans = append(hotPlans,
		hotPlan{name: "drafts in a state", query: draftSelect + ` WHERE state IN (SELECT value FROM json_each(?))` + draftOrder, args: 1,
			want: []string{"drafts_state (state=?)"}, tempSort: "USE TEMP B-TREE FOR ORDER BY"},
		hotPlan{name: "drafts of an entry", query: draftSelect + ` WHERE entry = ?` + draftOrder, args: 1,
			want: []string{"drafts_entry (entry=?)"}},
		hotPlan{name: "drafts of a unit", query: draftSelect + ` WHERE provider = ? AND unit_key = ?` + draftOrder, args: 2,
			want: []string{"drafts_unit (provider=? AND unit_key=?)"}},
		hotPlan{name: "drafts of a rule key", query: draftSelect + ` WHERE rule = ? AND rule_key = ?` + draftOrder, args: 2,
			want: []string{"drafts_rule (rule=? AND rule_key=?)"}},
	)
}

func sampleDraft(u unit.Unit, at time.Time) draft.Draft {
	return draft.Draft{Unit: u, Items: []string{"i1", "i2"}, Entry: "e1", Kind: draft.Nudge, Target: "#general",
		Text: "ping @{slack:U000AAA}", Original: "ping", Edited: true,
		Origin:   draft.Origin{Kind: draft.OriginAuto, TurnSeq: 3, Intent: "reply", Workflow: "wf", Rule: "stalled-nudge", Key: "stalled-nudge:e1:slack:U000AAA"},
		Approval: draft.Approval{Mode: draft.Window, Window: 10 * time.Minute}, Deadline: at.Add(10 * time.Minute),
		State: draft.Proposed, Error: "", Reason: "",
		Would:     draft.Would{Policy: draft.WouldSend, Reason: "a nudge", Outcome: ""},
		CreatedAt: at, UpdatedAt: at}
}

func TestDraftsStoreRoundTrips(t *testing.T) {
	ctx := context.Background()
	db, _ := openTemp(t)
	s := db.Drafts()
	at := time.Date(2026, 10, 22, 12, 0, 0, 500, time.UTC)
	first, err := s.Insert(ctx, sampleDraft(slackThread, at))
	if err != nil || first.ID != 1 {
		t.Fatalf("insert %+v %v", first, err)
	}
	if got, err := s.Get(ctx, first.ID); err != nil || !reflect.DeepEqual(got, first) {
		t.Fatalf("get\n %+v %v\nwant\n %+v", got, err, first)
	}

	full := first
	full.State, full.Reason, full.Error = draft.Sent, "", "may have been posted"
	full.Evidence = draft.Evidence{Ref: "1.5", Permalink: "https://example.test/p", At: at.Add(time.Second)}
	full.SendingAt, full.UpdatedAt = at.Add(time.Millisecond), at.Add(2*time.Second)
	full.Would.Outcome = draft.OutcomeEdited
	full.Reaction, full.Items = "eyes", nil
	if _, err := s.Update(ctx, full, draft.Proposed); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	full.Items, full.Rev = []string{}, 1
	if !reflect.DeepEqual(got, full) {
		t.Fatalf("after update\n %+v\nwant\n %+v", got, full)
	}
	if _, err := s.Update(ctx, full, draft.Proposed); !errors.Is(err, outbox.ErrConflict) {
		t.Fatalf("stale update: %v", err)
	}
	stale := full
	stale.Rev = 0
	if _, err := s.Update(ctx, stale, draft.Sent); !errors.Is(err, outbox.ErrConflict) {
		t.Fatalf("update of an older rev in the same state: %v", err)
	}
	if _, err := s.Update(ctx, draft.Draft{ID: 99, Unit: slackThread}, draft.Proposed); !errors.Is(err, outbox.ErrNotFound) {
		t.Fatalf("update of a missing draft: %v", err)
	}
	if _, err := s.Get(ctx, 99); !errors.Is(err, outbox.ErrNotFound) {
		t.Fatalf("get of a missing draft: %v", err)
	}

	discarded := sampleDraft(ghPull, at)
	discarded.State, discarded.Reason, discarded.Entry = draft.Discarded, "observe", ""
	discarded.Origin = draft.Origin{Kind: draft.OriginTurn, TurnSeq: 8}
	discarded.Approval, discarded.Deadline = draft.Approval{Mode: draft.Observe}, time.Time{}
	d2, _ := s.Insert(ctx, discarded)
	d3, _ := s.Insert(ctx, sampleDraft(slackThread, at))

	ids := func(f outbox.Filter) []draft.ID {
		ds, err := s.List(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		var out []draft.ID
		for _, d := range ds {
			out = append(out, d.ID)
		}
		return out
	}
	for name, c := range map[string]struct {
		f    outbox.Filter
		want []draft.ID
	}{
		"all":       {outbox.Filter{}, []draft.ID{d3.ID, d2.ID, first.ID}},
		"states":    {outbox.Filter{States: []draft.State{draft.Sent, draft.Discarded}}, []draft.ID{d2.ID, first.ID}},
		"unit":      {outbox.Filter{Unit: &slackThread}, []draft.ID{d3.ID, first.ID}},
		"entry":     {outbox.Filter{Entry: "e1"}, []draft.ID{d3.ID, first.ID}},
		"rule":      {outbox.Filter{Rule: "stalled-nudge"}, []draft.ID{d3.ID, first.ID}},
		"key":       {outbox.Filter{Rule: "stalled-nudge", Key: "stalled-nudge:e1:slack:U000AAA", States: draft.Open}, []draft.ID{d3.ID}},
		"other key": {outbox.Filter{Key: "nope"}, nil},
	} {
		if got := ids(c.f); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
	if got, _ := s.Get(ctx, d2.ID); !reflect.DeepEqual(got, d2) {
		t.Errorf("observe draft\n %+v\nwant\n %+v", got, d2)
	}
}

func TestEvidenceNeverTouchesConfirmedFields(t *testing.T) {
	ctx := context.Background()
	db, _ := openTemp(t)
	clock := fixedNow{noteAt}
	ledger := &coreitems.Ledger{Store: db.Items(), Clock: clock}
	summary, kind := "the user's wording", items.Request
	create := items.Op{Op: items.OpCreate, Unit: slackThread, Item: "i1", Anchor: unit.Anchor{Unit: slackThread, Ref: "C000AAA/1.2"},
		Fields: &items.Fields{Kind: &kind, Summary: &summary}}
	if _, err := ledger.Apply(ctx, items.User, []items.Op{create}); err != nil {
		t.Fatal(err)
	}
	before, err := ledger.Items(ctx, slackThread)
	if err != nil || len(before) != 1 || !before[0].Confirmed {
		t.Fatalf("seed %+v %v", before, err)
	}

	svc := &outbox.Service{Store: db.Drafts(), Clock: clock,
		Effects: func(unit.ProviderID) (port.Effects, error) { return postAll{}, nil },
		Evidence: outbox.EvidenceTo(func(ctx context.Context, prov items.Provenance, ops []items.Op) error {
			_, err := ledger.Apply(ctx, prov, ops)
			return err
		}),
		Items: ledger,
	}
	d, err := svc.Propose(ctx, outbox.Proposal{Unit: slackThread, Items: []string{"i1"}, Kind: draft.Reply, Text: "done",
		Origin: draft.Origin{Kind: draft.OriginTurn, TurnSeq: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Approve(ctx, d.ID, nil); err != nil {
		t.Fatal(err)
	}
	if sent, _, err := svc.Send(ctx); sent != 1 || err != nil {
		t.Fatalf("send %d %v", sent, err)
	}
	after, err := ledger.Items(ctx, slackThread)
	if err != nil || len(after) != 1 {
		t.Fatalf("after %+v %v", after, err)
	}
	got, was := after[0], before[0]
	if len(got.Trail) != 1 || got.Trail[0].Kind != items.EvidenceSentDraft || got.Trail[0].Anchor.Ref != fmt.Sprintf("draft:%d:reply", d.ID) {
		t.Fatalf("trail %+v", got.Trail)
	}
	got.Trail, was.Trail = nil, nil
	got.UpdatedAt, was.UpdatedAt = time.Time{}, time.Time{}
	if !reflect.DeepEqual(got, was) {
		t.Fatalf("evidence changed the item\n %+v\nwas\n %+v", got, was)
	}
	log, err := db.Items().Log(ctx, slackThread, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	last := log[len(log)-1]
	if last.Provenance != items.Effects || last.Outcome != coreitems.OutcomeApplied || last.Op.Op != items.OpEvidence {
		t.Fatalf("last op %+v", last)
	}
}

type fixedNow struct{ t time.Time }

func (c fixedNow) Now() time.Time { return c.t }

type postAll struct{}

func (postAll) Publish(ctx context.Context, d draft.Draft) (draft.Evidence, error) {
	return draft.Evidence{Ref: "1.1", Permalink: "https://example.test/1"}, nil
}

func (postAll) Verify(ctx context.Context, d draft.Draft, startedAt time.Time) (draft.Evidence, bool, error) {
	return draft.Evidence{}, false, nil
}

type gatedFx struct {
	entered, release chan struct{}
	mu               sync.Mutex
	published        map[draft.ID][]string
}

func (g *gatedFx) Validate(ctx context.Context, d draft.Draft) error {
	if g.entered != nil {
		g.entered <- struct{}{}
		<-g.release
	}
	return nil
}

func (g *gatedFx) Publish(ctx context.Context, d draft.Draft) (draft.Evidence, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.published[d.ID] = append(g.published[d.ID], d.Text)
	return draft.Evidence{Ref: fmt.Sprint(d.ID)}, nil
}

func (g *gatedFx) Verify(ctx context.Context, d draft.Draft, startedAt time.Time) (draft.Evidence, bool, error) {
	return draft.Evidence{}, false, nil
}

func draftService(db *DB, fx port.Effects) *outbox.Service {
	return &outbox.Service{Store: db.Drafts(), Clock: fixedNow{noteAt},
		Effects: func(unit.ProviderID) (port.Effects, error) { return fx, nil }}
}

func TestEditDuringApproveConflicts(t *testing.T) {
	ctx := context.Background()
	db, _ := openTemp(t)
	fx := &gatedFx{published: map[draft.ID][]string{}}
	svc := draftService(db, fx)
	d, err := svc.Propose(ctx, outbox.Proposal{Unit: slackThread, Kind: draft.Reply, Text: "A", Origin: draft.Origin{Kind: draft.OriginTurn}})
	if err != nil {
		t.Fatal(err)
	}
	fx.entered, fx.release = make(chan struct{}), make(chan struct{})
	approved := make(chan error)
	go func() { _, err := svc.Approve(ctx, d.ID, nil); approved <- err }()
	<-fx.entered
	fx.entered = nil
	if _, err := svc.Edit(ctx, d.ID, "B"); err != nil {
		t.Fatal(err)
	}
	close(fx.release)
	if err := <-approved; !errors.Is(err, outbox.ErrConflict) {
		t.Fatalf("approve over a newer edit: %v", err)
	}
	svc.Send(ctx)
	got, _ := db.Drafts().Get(ctx, d.ID)
	if got.State != draft.Proposed || got.Text != "B" || len(fx.published) != 0 {
		t.Fatalf("%+v published %v", got, fx.published)
	}
	if _, err := svc.Approve(ctx, d.ID, nil); err != nil {
		t.Fatal(err)
	}
	svc.Send(ctx)
	if !slices.Equal(fx.published[d.ID], []string{"B"}) {
		t.Fatalf("published %v", fx.published)
	}
}

func TestStaleSendClaimConflicts(t *testing.T) {
	ctx := context.Background()
	db, _ := openTemp(t)
	fx := &gatedFx{published: map[draft.ID][]string{}}
	svc := draftService(db, fx)
	d, _ := svc.Propose(ctx, outbox.Proposal{Unit: slackThread, Kind: draft.Reply, Text: "old", Origin: draft.Origin{Kind: draft.OriginTurn}})
	snap, err := svc.Approve(ctx, d.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	cur := snap
	cur.Text = "new"
	if _, err := db.Drafts().Update(ctx, cur, draft.Approved); err != nil {
		t.Fatal(err)
	}
	snap.State = draft.Sending
	if _, err := db.Drafts().Update(ctx, snap, draft.Approved); !errors.Is(err, outbox.ErrConflict) {
		t.Fatalf("claim from a stale snapshot: %v", err)
	}
	svc.Send(ctx)
	if !slices.Equal(fx.published[d.ID], []string{"new"}) {
		t.Fatalf("published %v", fx.published)
	}
}

func TestConcurrentSendAcrossHandlesPublishesOnce(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state", "store.db")
	fx := &gatedFx{published: map[draft.ID][]string{}}
	var svcs []*outbox.Service
	for range 3 {
		db, err := Open(path, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		svcs = append(svcs, draftService(db, fx))
	}
	const n = 100
	for i := range n {
		d, err := svcs[0].Propose(ctx, outbox.Proposal{Unit: slackThread, Kind: draft.Reply, Text: fmt.Sprint(i), Origin: draft.Origin{Kind: draft.OriginTurn}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svcs[0].Approve(ctx, d.ID, nil); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	total := 0
	for i := range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sent, _, err := svcs[i%3].Send(ctx)
			if err != nil {
				t.Error(err)
			}
			mu.Lock()
			total += sent
			mu.Unlock()
		}()
	}
	wg.Wait()
	if total != n || len(fx.published) != n {
		t.Fatalf("sent %d, published %d drafts", total, len(fx.published))
	}
	for id, texts := range fx.published {
		if len(texts) != 1 {
			t.Errorf("draft %d published %d times", id, len(texts))
		}
	}
}

func TestADraftStoresItsShapeAndEffects(t *testing.T) {
	ctx := context.Background()
	db, _ := openTemp(t)
	s := db.Drafts()
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	byKind := sampleDraft(slackThread, at)
	byKind.Kind = draft.Nudge
	d, err := s.Insert(ctx, byKind)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, d.ID); got.Purpose != "nudge" || got.Delivery != draft.Message || got.Effects == nil || len(got.Effects) > 0 || !reflect.DeepEqual(got, d) {
		t.Fatalf("shaped from its kind: %+v\ninsert returned %+v", got, d)
	}
	shaped := sampleDraft(slackThread, at)
	shaped.Kind, shaped.Purpose, shaped.Delivery, shaped.Effects = draft.React, "close", draft.Reaction, []string{"unit.closed"}
	d, err = s.Insert(ctx, shaped)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, d.ID); got.Purpose != "close" || got.Delivery != draft.Reaction || !slices.Equal(got.Effects, []string{"unit.closed"}) {
		t.Fatalf("%+v", got)
	}
}
