package cli

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/ingest/ingesttest"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/proposals"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/source"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

type proposalPublisher struct {
	mu   sync.Mutex
	sent []draft.ID
	now  func() time.Time
}

func (p *proposalPublisher) Publish(ctx context.Context, d draft.Draft) (draft.Evidence, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sent = append(p.sent, d.ID)
	return draft.Evidence{Ref: "1.2", At: p.now()}, nil
}

func (p *proposalPublisher) Verify(ctx context.Context, d draft.Draft, startedAt time.Time) (draft.Evidence, bool, error) {
	return draft.Evidence{}, false, nil
}

func withPublisher(p source.Plugin, pub *proposalPublisher) source.Plugin {
	build := p.Build
	p.Build = func(cfg config.Config, d source.Deps) (source.Module, error) {
		m, err := build(cfg, d)
		m.Publisher = pub
		return m, err
	}
	return p
}

func TestServedProposalsNudgeAStaleWaitingOnTheRealStore(t *testing.T) {
	ctx := context.Background()
	te := newTestEnv(t, &fake.Manager{})
	now := testNow
	te.Now = func() time.Time { return now }
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	th := unit.Unit{Provider: ingesttest.Provider, Key: "C1 0001"}
	me, alice := items.Person{Key: "slack:U000ME"}, items.Person{Key: "slack:U000AAA", Name: "alice"}
	pub := &proposalPublisher{now: te.Now}
	cfg := config.Defaults()
	build := func(cfg config.Config) []source.Module {
		mods, err := buildSources([]source.Plugin{withPublisher(threadModule(th), pub)}, cfg, source.Deps{DB: db, Inventory: inventory.New(db), Clock: clockFunc(te.Now)})
		if err != nil {
			t.Fatal(err)
		}
		return mods
	}
	ledger := te.itemsLedger(db, cfg, t.Logf)
	kind, summary := items.Request, "Ship the release notes"
	if _, err := ledger.Apply(ctx, items.Extracted, []items.Op{{Op: items.OpCreate, Unit: th, Item: "e1", Anchor: unit.Anchor{Unit: th, Ref: "C1/1.1"},
		Fields: &items.Fields{Kind: &kind, Summary: &summary, Requester: &me, WaitingOn: &[]items.Person{alice}}}}); err != nil {
		t.Fatal(err)
	}
	entries := te.entries(db, cfg, ledger)
	entries.SetMe(gtd.Me{Keys: []string{me.Key}})
	if err := entries.Sync(ctx, []unit.Unit{th}); err != nil {
		t.Fatal(err)
	}
	waitingID := gtd.DerivedID(gtd.KindWaiting, th, alice.Key)
	if e, err := db.Entries().Entry(ctx, waitingID); err != nil || e.State != gtd.Waiting {
		t.Fatalf("waiting entry %+v %v", e, err)
	}

	job := te.proposalsJob(db, cfg, build(cfg), t.Logf)
	if sum, err := job.Run(ctx); err != nil || sum.Proposed != 0 {
		t.Fatalf("fresh: %s %v", sum, err)
	}
	now = now.Add(7 * 24 * time.Hour)
	if sum, err := job.Run(ctx); err != nil || sum.Proposed != 1 {
		t.Fatalf("stale: %s %v", sum, err)
	}
	if sum, err := job.Run(ctx); err != nil || sum.Proposed != 0 || sum.Held != 1 {
		t.Fatalf("second pass: %s %v", sum, err)
	}
	got, err := db.Drafts().List(ctx, outbox.Filter{Rule: proposals.RuleStalledNudge})
	if err != nil || len(got) != 1 {
		t.Fatalf("%d drafts %v", len(got), err)
	}
	d := got[0]
	if d.Unit != th || d.Kind != draft.Nudge || d.Entry != string(waitingID) || d.Approval.Mode != draft.Always || d.State != draft.Proposed ||
		d.Origin != (draft.Origin{Kind: draft.OriginAuto, Rule: proposals.RuleStalledNudge, Key: string(waitingID) + ":" + alice.Key}) {
		t.Fatalf("%+v", d)
	}
	if len(pub.sent) != 0 {
		t.Fatalf("published before approval: %v", pub.sent)
	}

	t.Run("a disabled plugin proposes nothing and its draft cannot be approved", func(t *testing.T) {
		off := cfg
		no := false
		off.Plugins = map[string]config.Plugin{string(ingesttest.Provider): {Enabled: &no}}
		mods := build(off)
		jobOff := te.proposalsJob(db, off, mods, t.Logf)
		now = now.Add(30 * 24 * time.Hour)
		defer func() { now = now.Add(-30 * 24 * time.Hour) }()
		if sum, err := jobOff.Run(ctx); err != nil || sum.Proposed+sum.Refused+sum.Held != 0 {
			t.Fatalf("%s %v", sum, err)
		}
		sender := &outbox.Service{Store: db.Drafts(), Effects: publishersOf(mods), Clock: clockFunc(te.Now)}
		if _, err := sender.Approve(ctx, d.ID, nil); !errors.Is(err, outbox.ErrDisabled) {
			t.Fatalf("approve: %v", err)
		}
	})

	sender := &outbox.Service{Store: db.Drafts(), Effects: publishersOf(build(cfg)), Clock: clockFunc(te.Now)}
	if _, err := sender.Approve(ctx, d.ID, nil); err != nil {
		t.Fatal(err)
	}
	if sent, _, err := sender.Send(ctx); sent != 1 || err != nil {
		t.Fatalf("sent %d %v", sent, err)
	}
	if err := job.Stamp(ctx); err != nil {
		t.Fatal(err)
	}
	e, err := db.Entries().Entry(ctx, waitingID)
	if err != nil || !e.Waiting.LastNudge.Equal(now) {
		t.Fatalf("LastNudge %+v %v", e.Waiting, err)
	}
	now = now.Add(time.Hour)
	if sum, err := job.Run(ctx); err != nil || sum.Proposed != 0 {
		t.Fatalf("right after the nudge: %s %v", sum, err)
	}
	now = now.Add(24 * time.Hour)
	if sum, err := job.Run(ctx); err != nil || sum.Proposed != 1 {
		t.Fatalf("after renudge_after: %s %v", sum, err)
	}
}
