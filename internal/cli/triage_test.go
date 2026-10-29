package cli

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/gates"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/outbox"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// triageRig runs the bundled workflows over a real store as serve wires them: the tasks service, the gates, the
// Outbox with a Slack publisher, and the sweep's engine, which has swept once, so the events after it are new.
type triageRig struct {
	*taskRig
	eng *automation.Engine
}

func newTriageRig(t *testing.T) *triageRig {
	t.Helper()
	r := newTaskRig(t, nil)
	r.te.newGates(r.db, t.Logf)
	mods := []pluginModule{{ID: slackPlugin, Publisher: okEffects{}, Reacts: true}}
	r.te.serveOutbox(daemon.Deps{}, r.db, r.te.cfg, mods, t.Logf)
	ed := engineDeps{DB: r.db, Modules: mods, SessionFacts: noUnits{}, WorkspaceFacts: noUnits{}, Log: t.Logf}
	w := &serveWiring{DB: r.db, Cfg: r.te.cfg, Modules: mods, Log: t.Logf, Engine: &ed}
	r.te.wireSubjects(w)
	r.te.wireDrafts(w)
	tr := &triageRig{taskRig: r, eng: r.te.newEngine(ed)}
	tr.sweep(t)
	return tr
}

// ask stores task id in the Inbox, holding Ana's ask on a Slack thread of its own.
func (r *triageRig) ask(t *testing.T, id task.ID, ts string) unit.Unit {
	t.Helper()
	ctx := context.Background()
	thread := slackThreadUnit("T000AAA", "C000AAA", ts)
	it := items.Item{ID: "i1", Unit: thread, Kind: items.Request, Summary: "Can you check the export?", State: items.Open, Basis: items.NoBasis,
		Requester: items.Person{Key: "slack:U000ANA", Name: "Ana"}, WaitingOn: []items.Person{{Key: "slack:U000ME", Name: "Me"}},
		Origin: unit.Anchor{Unit: thread, Permalink: "https://example.test/archives/C000AAA/p" + ts}, CreatedAt: testNow.Add(-time.Hour), UpdatedAt: testNow}
	if err := r.db.Items().Commit(ctx, coreitems.Commit{Items: []items.Item{it}}); err != nil {
		t.Fatal(err)
	}
	at := testNow.Add(-time.Hour)
	if err := r.db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: task.Task{ID: id, State: "inbox", Category: task.CategoryInbox,
		Title: "Check the export", Origin: task.OriginUser, Units: []unit.Unit{thread}, Items: []task.ItemRef{{Unit: thread, Item: "i1"}},
		Created: at, Updated: at}}}); err != nil {
		t.Fatal(err)
	}
	return thread
}

func (r *triageRig) verb(t *testing.T, id task.ID, verb string, args map[string]string) {
	t.Helper()
	if _, err := r.te.tasks.Do(context.Background(), id, coretasks.Request{Verb: task.Verb(verb), Args: args, Source: "cockpit", Actor: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
}

func (r *triageRig) sweep(t *testing.T) {
	t.Helper()
	for range 2 {
		if _, _, err := r.eng.Sweep(context.Background(), &sync.Mutex{}); err != nil {
			t.Fatal(err)
		}
	}
}

// drafts are the drafts the workflow proposed, each with its gate.
func (r *triageRig) drafts(t *testing.T, workflow string) ([]draft.Draft, []gate.Gate) {
	t.Helper()
	ctx := context.Background()
	ds, err := r.te.outbox.List(ctx, outbox.Filter{Workflow: workflow})
	if err != nil {
		t.Fatal(err)
	}
	var gs []gate.Gate
	for _, d := range ds {
		g, ok, err := r.te.outbox.GateOf(ctx, d)
		if err != nil || !ok {
			t.Fatalf("draft %d has no gate: %v", d.ID, err)
		}
		gs = append(gs, g)
	}
	return ds, gs
}

func TestAcknowledgeSetsPromisedAndProposesAReply(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newTriageRig(t)
	thread := r.ask(t, "t-by", "1.1")
	r.ask(t, "t-react", "2.2")

	r.verb(t, "t-by", "acknowledge", map[string]string{"by": "17:00"})
	r.verb(t, "t-react", "acknowledge", nil)
	r.sweep(t)

	got, err := r.db.Tasks().Task(ctx, "t-by")
	if err != nil {
		t.Fatal(err)
	}
	by := time.Date(testNow.Year(), testNow.Month(), testNow.Day(), 17, 0, 0, 0, testNow.Location())
	if got.State != "next" || !got.Prop("promised").Time.Equal(by) || got.Written["promised"].By != task.WriterPerson {
		t.Fatalf("acknowledged by 17:00: state %s, promised %v by %q", got.State, got.Prop("promised").Time, got.Written["promised"].By)
	}
	ds, gs := r.drafts(t, "task-acknowledge-reply")
	if len(ds) != 1 {
		t.Fatalf("replies %+v, want one", ds)
	}
	if want := "Looking into it, by " + by.Local().Format("15:04") + "."; ds[0].Delivery != draft.Message || ds[0].Unit != thread ||
		ds[0].Task != "t-by" || ds[0].Text != want+"\n" && ds[0].Text != want {
		t.Errorf("the reply: %+v, want %q on %s", ds[0], want, thread)
	}
	if gs[0].Status != gate.Open || gs[0].Kind != gate.KindDraft {
		t.Errorf("the reply's gate %+v, want it open, waiting for you", gs[0])
	}

	if reacted, _ := r.db.Tasks().Task(ctx, "t-react"); reacted.State != "next" || !reacted.Prop("promised").Time.IsZero() {
		t.Fatalf("acknowledged with no time: state %s, promised %v", reacted.State, reacted.Prop("promised").Time)
	}
	ds, gs = r.drafts(t, "task-acknowledge-react")
	if len(ds) != 1 || ds[0].Delivery != draft.Reaction || ds[0].Reaction != "eyes" || ds[0].Task != "t-react" ||
		ds[0].Target != "https://example.test/archives/C000AAA/p2.2" || gs[0].Status != gate.Open {
		t.Fatalf("the reaction: %+v %+v", ds, gs)
	}
	if ds, _ := r.drafts(t, "task-acknowledge-reply"); len(ds) != 1 {
		t.Errorf("an acknowledgment with no time proposed a reply: %+v", ds)
	}
}

func TestNotMineProposesARedirect(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newTriageRig(t)
	thread := r.ask(t, "t-1", "1.1")

	r.verb(t, "t-1", "not-mine", nil)
	r.sweep(t)

	if got, _ := r.db.Tasks().Task(ctx, "t-1"); got.Category != task.CategoryDropped {
		t.Fatalf("not mine left the task in %s", got.State)
	}
	ds, gs := r.drafts(t, "task-not-mine")
	if len(ds) != 1 || ds[0].Delivery != draft.Message || ds[0].Unit != thread || ds[0].Task != "t-1" || ds[0].Text == "" {
		t.Fatalf("the redirect: %+v", ds)
	}
	if gs[0].Status != gate.Open {
		t.Errorf("the redirect's gate %+v, want it waiting for you", gs[0])
	}
	r.sweep(t)
	if ds, _ := r.drafts(t, "task-not-mine"); len(ds) != 1 {
		t.Errorf("a second sweep proposed another redirect: %+v", ds)
	}
}

func TestStartOpeningAPROpensReviewTheDiff(t *testing.T) {
	te, _, _, _ := startEnv(t)
	ctx := context.Background()
	if code := te.run("start", "tell the team the export ships Friday"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	tk := capturedTask.FindStringSubmatch(te.out.String())
	if tk == nil {
		t.Fatalf("no task captured:\n%s", te.out)
	}
	id := task.ID(strings.TrimPrefix(tk[1], "task:"))
	svc := te.newGates(te.db, t.Logf)
	ed := engineDeps{DB: te.db, SessionFacts: noUnits{}, WorkspaceFacts: noUnits{}, Log: t.Logf}
	te.wireSubjects(&serveWiring{DB: te.db, Cfg: te.cfg, Log: t.Logf, Engine: &ed})
	eng := te.newEngine(ed)
	sweep := func() {
		t.Helper()
		for range 2 {
			if _, _, err := eng.Sweep(ctx, &sync.Mutex{}); err != nil {
				t.Fatal(err)
			}
		}
	}
	reviews := func() []gate.Gate {
		t.Helper()
		gs, err := te.db.Gates(te.Now).OfTask(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return slices.DeleteFunc(gs, func(g gate.Gate) bool { return g.Name != "review-diff" })
	}

	sweep()
	if gs := reviews(); len(gs) != 0 {
		t.Fatalf("a start that opened no pull request asks to review a diff: %+v", gs)
	}

	books, err := te.Turns.Store.Books(ctx, task.UnitOf(id))
	if err != nil || len(books[task.UnitOf(id)].Turns) != 1 {
		t.Fatalf("books %+v, %v", books, err)
	}
	sent := books[task.UnitOf(id)].Turns[0]
	rec := record.Record{State: record.Done, Summary: "opened the pull request", Outcome: &record.Outcome{URL: "https://github.com/acme/api/pull/9"}}
	if _, err := te.Turns.RecordTurnEnd(ctx, turns.RecordRequest{Session: sent.SentTo, Turn: sent.ID, Unit: task.UnitOf(id), Record: rec}); err != nil {
		t.Fatal(err)
	}
	sweep()
	gs := reviews()
	if len(gs) != 1 || gs[0].Kind != gate.KindStep || gs[0].Status != gate.Open || gs[0].Subject != "start-review-diff" {
		t.Fatalf("review the diff: %+v, want one open gate of start-review-diff", gs)
	}
	if err := svc.Resolve(ctx, gs[0].ID, gates.Resolution{Intent: "dismiss", By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
	later, err := te.db.Tasks().Task(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	later.Title, later.Updated = "tell the team the export ships Monday", later.Updated.Add(time.Hour)
	if err := te.db.Tasks().Apply(ctx, []task.Change{{Op: task.OpUpdate, Task: later}}); err != nil {
		t.Fatal(err)
	}
	sweep()
	if gs := reviews(); len(gs) != 1 {
		t.Fatalf("the review of the same pull request opened again: %+v", gs)
	}
}
