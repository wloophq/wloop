package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	slacksource "github.com/wloophq/wloop/internal/adapter/source/slack"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/source"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

var (
	draftThread = unit.Unit{Provider: "slack", Key: "T000AAA/C000AAA/1.1"}
	draftEntry  = unit.Unit{Provider: unit.Entry, Key: "e1"}
	ana         = items.Person{Key: "slack:U000ANA", Name: "Ana"}
	bob         = items.Person{Key: "slack:U000BOB", Name: "Bob"}
)

type memEntries struct {
	mu sync.Mutex
	m  map[gtd.ID]gtd.Entry
}

func (m *memEntries) Entry(_ context.Context, id gtd.ID) (gtd.Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.m[id]
	if !ok {
		return gtd.Entry{}, fmt.Errorf("entry %s: %w", id, coregtd.ErrNoEntry)
	}
	return e, nil
}

type memItems struct {
	mu sync.Mutex
	m  map[unit.Unit][]items.Item
}

func (m *memItems) Items(_ context.Context, u unit.Unit) ([]items.Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]items.Item(nil), m.m[u]...), nil
}

func (m *memItems) set(u unit.Unit, its ...items.Item) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.m[u] = its
}

type okEffects struct{}

func (okEffects) Publish(_ context.Context, d draft.Draft) (draft.Evidence, error) {
	return draft.Evidence{Ref: fmt.Sprint(d.ID), Permalink: "https://example.test/p"}, nil
}

func (okEffects) Verify(context.Context, draft.Draft, time.Time) (draft.Evidence, bool, error) {
	return draft.Evidence{}, false, nil
}

// draftRig runs draft.propose over a real store's Outbox, a user profile
// layer in a temporary directory and fake entries and items.
type draftRig struct {
	t       *testing.T
	s       *stepSet
	db      *sqlite.DB
	svc     *outbox.Service
	entries *memEntries
	items   *memItems
	units   *automationtest.Units
	deps    *draftDeps
	wf      workflow.Workflow
	cfg     config.Config
	now     time.Time
	writes  int
}

func newDraftRig(t *testing.T, wf workflow.Workflow, text string) *draftRig {
	t.Helper()
	te := newTestEnv(t, &fake.Manager{})
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "d.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	dir := t.TempDir()
	if text != "" {
		wd := filepath.Join(dir, "workflows", wf.Name)
		if err := os.MkdirAll(wd, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wd, defaultDraftText), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r := &draftRig{t: t, db: db, wf: wf, cfg: te.cfg, now: time.Date(2026, 10, 14, 15, 0, 0, 0, time.UTC),
		entries: &memEntries{m: map[gtd.ID]gtd.Entry{}}, items: &memItems{m: map[unit.Unit][]items.Item{}}, units: automationtest.NewUnits()}
	r.cfg.Profile = dir
	clock := clockFunc(func() time.Time { return r.now })
	r.svc = &outbox.Service{Store: countingDrafts{Store: db.Drafts(), n: &r.writes}, Clock: clock,
		Effects: func(unit.ProviderID) (port.Effects, error) { return okEffects{}, nil }}
	r.deps = &draftDeps{Outbox: func() draftOutbox { return r.svc }, Entries: r.entries, Items: r.items,
		Workflow: func(name string) (workflow.Workflow, bool) { return r.wf, name == r.wf.Name },
		Config:   func() config.Config { return r.cfg }, Now: func() time.Time { return r.now }}
	r.s = newStepSet(te.Env, stepDeps{Units: r.units, Rounds: te.Rounds, Drafts: r.deps})
	return r
}

// countingDrafts counts the Outbox's writes.
type countingDrafts struct {
	outbox.Store
	n *int
}

func (c countingDrafts) Insert(ctx context.Context, d draft.Draft) (draft.Draft, error) {
	*c.n++
	return c.Store.Insert(ctx, d)
}

func (c countingDrafts) Update(ctx context.Context, d draft.Draft, from draft.State) (draft.Draft, error) {
	*c.n++
	return c.Store.Update(ctx, d, from)
}

func draftWF(name string, args ...string) workflow.Workflow {
	return workflow.Workflow{Name: name, Mode: workflow.ModeApply, Steps: []workflow.Step{wfStep("draft.propose", args...)}}
}

func (r *draftRig) check(u unit.Unit) string {
	r.t.Helper()
	refusal, err := r.s.Check(context.Background(), r.wf.Steps[0], u, workflow.Why{Workflow: r.wf.Name}, automation.CheckOpts{})
	if err != nil {
		r.t.Fatal(err)
	}
	return refusal
}

func (r *draftRig) run(u unit.Unit) string {
	r.t.Helper()
	detail, err := r.s.Run(context.Background(), r.wf.Steps[0], u, workflow.Why{Workflow: r.wf.Name}, automation.RunOpts{})
	if err != nil {
		r.t.Fatal(err)
	}
	return detail
}

// pass is a sweep's pass over u: Check, then Run when it does not refuse.
func (r *draftRig) pass(u unit.Unit) string {
	r.t.Helper()
	if refusal := r.check(u); refusal != "" {
		return refusal
	}
	return r.run(u)
}

func (r *draftRig) drafts() []draft.Draft {
	r.t.Helper()
	ds, err := r.svc.List(context.Background(), outbox.Filter{Workflow: r.wf.Name})
	if err != nil {
		r.t.Fatal(err)
	}
	return ds
}

func openItem(id, summary string, created time.Time, waiting ...items.Person) items.Item {
	return items.Item{ID: id, Unit: draftThread, Summary: summary, State: items.Open, WaitingOn: waiting, Basis: items.NoBasis,
		CreatedAt: created, Origin: unit.Anchor{Unit: draftThread, Permalink: "https://example.test/" + id}}
}

func TestDraftProposeRendersTheProfileTextWithItemsAsData(t *testing.T) {
	t.Parallel()
	r := newDraftRig(t, draftWF("asks", "purpose", "ask_owner", "items", "unowned"),
		"Who owns {{if eq (len .Items) 1}}this{{else}}these{{end}}?\n{{range .Items}}\n- {{.Line}}{{end}}")
	r.items.set(draftThread, openItem("i1", "Renew  the {{.Person.Key}}\n cert {{printf \"x\"}}", r.now.Add(-72*time.Hour)),
		openItem("i2", "Pay the invoice", r.now.Add(-48*time.Hour)))
	if got := r.run(draftThread); !strings.HasPrefix(got, "proposed draft ") {
		t.Fatalf("run: %s", got)
	}
	ds := r.drafts()
	want := "Who owns these?\n\n- Renew the {{.Person.Key}} cert {{printf \"x\"}}\n- Pay the invoice"
	if len(ds) != 1 || ds[0].Text != want || ds[0].Purpose != "ask_owner" || ds[0].Origin.Rule != "asks" || ds[0].Origin.Key != draftThread.String() {
		t.Fatalf("drafts %+v\nwant text %q", ds, want)
	}
}

func TestDraftProposeHoldsPerWorkflowSubjectAndPerson(t *testing.T) {
	t.Parallel()
	r := newDraftRig(t, draftWF("nudges", "purpose", "nudge", "items", "owed", "per", "person", "hold", "24h", "on_posted", "entry.nudged"),
		"@{{\"{\"}}{{.Person.Key}}{{\"}\"}}, any news?{{range .Items}}\n- {{.Line}}{{end}}")
	r.entries.m["e1"] = gtd.Entry{ID: "e1", Kind: gtd.KindWaiting, State: gtd.Waiting, Units: []unit.Unit{draftThread},
		Items:   []gtd.ItemRef{{Unit: draftThread, Item: "i1"}, {Unit: draftThread, Item: "i2"}},
		Waiting: &gtd.WaitingOn{On: []items.Person{ana, bob}}}
	r.items.set(draftThread, openItem("i1", "The deck", r.now.Add(-96*time.Hour), ana), openItem("i2", "The budget", r.now.Add(-96*time.Hour), bob))
	r.run(draftEntry)
	ds := r.drafts()
	if len(ds) != 2 {
		t.Fatalf("%d drafts, want one per person", len(ds))
	}
	byKey := map[string]draft.Draft{}
	for _, d := range ds {
		byKey[d.Origin.Key] = d
	}
	anas := byKey["e1:"+ana.Key]
	if anas.Text != "@{"+ana.Key+"}, any news?\n- The deck" || anas.Entry != "e1" || len(anas.Effects) != 1 {
		t.Fatalf("ana's draft %+v", anas)
	}
	if got := r.check(draftEntry); !strings.Contains(got, "held by draft") {
		t.Fatalf("an open draft does not hold: %q", got)
	}
	if _, err := r.svc.Approve(context.Background(), anas.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.svc.Send(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Discard(context.Background(), byKey["e1:"+bob.Key].ID, draft.WithdrawMoot); err != nil {
		t.Fatal(err)
	}
	r.now = r.now.Add(time.Hour)
	r.run(draftEntry)
	if ds := r.drafts(); len(ds) != 3 || ds[0].Origin.Key != "e1:"+bob.Key {
		t.Fatalf("a withdrawn draft held its person, or a sent one did not: %+v", ds)
	}
	other := r.wf
	other.Name = "other-nudges"
	r.wf = other
	if err := os.MkdirAll(filepath.Join(r.cfg.Profile, "workflows", other.Name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.cfg.Profile, "workflows", other.Name, defaultDraftText), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := r.check(draftEntry); got != "" {
		t.Fatalf("another workflow's drafts hold this one: %q", got)
	}
	r.wf.Name = "nudges"
	r.now = r.now.Add(24 * time.Hour)
	r.run(draftEntry)
	if ds := r.drafts(); len(ds) != 4 || ds[0].Origin.Key != "e1:"+ana.Key {
		t.Fatalf("past ana's hold, only ana is asked again: %+v", ds)
	}

	forever := newDraftRig(t, draftWF("closes", "purpose", "close", "items", "resolved", "hold_after_sent", "forever"), "Closing.")
	done := openItem("i1", "Done", r.now)
	done.State = items.Resolved
	forever.items.set(draftThread, done)
	forever.run(draftThread)
	d := forever.drafts()[0]
	if _, err := forever.svc.Approve(context.Background(), d.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := forever.svc.Send(context.Background()); err != nil {
		t.Fatal(err)
	}
	forever.now = forever.now.Add(365 * 24 * time.Hour)
	if got := forever.check(draftThread); !strings.Contains(got, "held by draft") {
		t.Fatalf("hold_after_sent forever: %q", got)
	}
}

func TestAReactionWithNoPermalinkIsRefusedAtPropose(t *testing.T) {
	t.Parallel()
	r := newDraftRig(t, draftWF("acks", "purpose", "close", "delivery", "reaction", "reaction", "white_check_mark", "items", "resolved", "target", "item"), "")
	done := openItem("i1", "Done", r.now)
	done.State, done.Origin.Permalink = items.Resolved, ""
	r.items.set(draftThread, done)
	if got := r.check(draftThread); got != "its item has no permalink to react to" {
		t.Fatalf("refusal %q", got)
	}
	done.Origin.Permalink = "https://example.test/i1"
	r.items.set(draftThread, done)
	r.run(draftThread)
	if ds := r.drafts(); len(ds) != 1 || ds[0].Delivery != draft.Reaction || ds[0].Target != "https://example.test/i1" || ds[0].Reaction != "white_check_mark" {
		t.Fatalf("drafts %+v", ds)
	}
}

func TestDraftProposeOnAFreshSubjectProposesOnItsFirstSweep(t *testing.T) {
	t.Parallel()
	r := newDraftRig(t, draftWF("asks", "purpose", "ask_owner", "items", "unowned_aged"), "Who owns this?")
	if got := r.check(draftThread); got != "no unowned_aged items" {
		t.Fatalf("no items yet: %q", got)
	}
	r.items.set(draftThread, openItem("i1", "Fresh", r.now.Add(-time.Hour)))
	if got := r.check(draftThread); got != "no unowned_aged items" {
		t.Fatalf("an item younger than a working day: %q", got)
	}
	r.items.set(draftThread, openItem("i1", "Old enough", r.now.Add(-25*time.Hour)))
	if got := r.pass(draftThread); !strings.HasPrefix(got, "proposed draft") {
		t.Fatalf("the first pass over a fresh subject: %q", got)
	}
}

func TestASubjectMatchingWhenAndWithdrawWhenIsProposedOnce(t *testing.T) {
	t.Parallel()
	wf := draftWF("asks", "purpose", "ask_owner", "items", "unowned")
	wf.Withdraw = automationtest.MatchFunc(func(v workflow.Vars) bool { return v.Facts["slack.closure_posted"] == true }, "slack.closure_posted")
	r := newDraftRig(t, wf, "Who owns this?")
	r.units.Put(draftThread, workflow.Facts{"slack.closure_posted": true, "slack.fingerprint": "f"})
	r.items.set(draftThread, openItem("i1", "Old", r.now.Add(-72*time.Hour)))
	for pass := range 3 {
		before := r.writes
		got := r.pass(draftThread)
		if got != "its withdraw when holds" {
			t.Fatalf("pass %d: %q", pass, got)
		}
		if r.writes != before {
			t.Fatalf("pass %d wrote %d drafts", pass, r.writes-before)
		}
	}
	r.units.Put(draftThread, workflow.Facts{"slack.closure_posted": false, "slack.fingerprint": "f"})
	if got := r.pass(draftThread); !strings.HasPrefix(got, "proposed draft") {
		t.Fatalf("once withdraw when no longer holds: %q", got)
	}
	before := r.writes
	if got := r.pass(draftThread); !strings.Contains(got, "held by draft") || r.writes != before {
		t.Fatalf("the second pass: %q, %d writes", got, r.writes-before)
	}
}

func TestUnitClosedClosesTheEntryAndUnwatchesAfterWarmFor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "s.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Date(2026, 10, 14, 15, 0, 0, 0, time.UTC)
	clock := clockFunc(func() time.Time { return now })
	m, err := slacksource.Module(config.Defaults(), source.Deps{DB: db, Clock: clock}, slacksource.Slack{})
	if err != nil {
		t.Fatal(err)
	}
	newSvc := func() *outbox.Service {
		return &outbox.Service{Store: db.Drafts(), Clock: clock,
			Effects: func(unit.ProviderID) (port.Effects, error) { return okEffects{}, nil },
			Applier: outbox.EffectApplier{Store: db.DraftEffects(), Clock: clock, Effects: effectLookup(effectDeps{Modules: []source.Module{m}})}}
	}
	svc := newSvc()
	if err := db.Extraction().Save(ctx, sqlite.ExtractionSave{States: map[unit.Unit]sqlite.ExtractionState{
		draftThread: {Verdict: "needs_closure", StartedByViewer: true, Watched: true, At: now}}}); err != nil {
		t.Fatal(err)
	}
	closeOf := func(closed bool) []gtd.Change {
		done := items.Item{ID: "i1", Unit: draftThread, State: items.Resolved, Requester: ana}
		f := gtd.UnitFacts{Unit: draftThread, StartedByMe: true, ClosurePosted: closed}
		return gtd.Reconcile(f, []items.Item{done}, gtd.Me{Keys: []string{ana.Key}}, nil, now)
	}
	if cs := closeOf(false); len(cs) != 1 || cs[0].Entry.Kind != gtd.KindClose {
		t.Fatalf("no close entry before the closure: %+v", cs)
	}
	plain, err := svc.Propose(ctx, outbox.Proposal{Unit: draftThread, Kind: draft.Close, Text: "Closing.", Origin: draft.Origin{Kind: draft.OriginUser}})
	if err != nil {
		t.Fatal(err)
	}
	react, err := svc.Propose(ctx, outbox.Proposal{Unit: draftThread, Purpose: "close", Delivery: draft.Reaction, Reaction: "white_check_mark",
		Effects: []string{"unit.closed"}, Origin: draft.Origin{Kind: draft.OriginUser}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Approve(ctx, plain.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Send(ctx); err != nil {
		t.Fatal(err)
	}
	if st, _ := db.Extraction().States(ctx, []unit.Unit{draftThread}); st[draftThread].ClosureMarked {
		t.Fatal("a close draft that declares no effect marked the closure")
	}
	if _, err := svc.Approve(ctx, react.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Send(ctx); err != nil {
		t.Fatal(err)
	}
	st, err := db.Extraction().States(ctx, []unit.Unit{draftThread})
	if err != nil || !st[draftThread].ClosureMarked {
		t.Fatalf("the reaction's unit.closed: %+v %v", st[draftThread], err)
	}
	if cs := closeOf(st[draftThread].ClosurePosted || st[draftThread].ClosureMarked); len(cs) != 0 {
		t.Fatalf("the close entry outlives the closure: %+v", cs)
	}
	if err := newSvc().Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if pending, _ := db.DraftEffects().Unapplied(ctx); len(pending) != 0 {
		t.Fatalf("applied again after a restart: %+v", pending)
	}
}
