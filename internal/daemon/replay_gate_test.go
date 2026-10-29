package daemon

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/gates/gatestest"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// stoppedAfter is the daemon's consequences with the daemon stopping right after each ran: the context the
// gates service passed ends before it can mark the consequence run.
type stoppedAfter struct {
	s    *Server
	stop context.CancelFunc
}

func (c *stoppedAfter) Once(g gate.Gate) bool { return c.s.Once(g) }

func (c *stoppedAfter) Release(ctx context.Context, g gate.Gate) error {
	defer c.stop()
	return c.s.Release(ctx, g)
}

func (c *stoppedAfter) Decide(ctx context.Context, g gate.Gate) error {
	defer c.stop()
	return c.s.Decide(ctx, g)
}

// tickStopped ticks once in a context the first consequence ends, as a daemon that stops right after it ran,
// then twice in a live one, as the daemon after its restart.
func tickStopped(t *testing.T, c *stoppedAfter, tick func(ctx context.Context) error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c.stop = cancel
	_ = tick(ctx)
	c.stop = func() {}
	for range 2 {
		if err := tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAReplayedHeldRoundRunsOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newServer(t)
	fa := &fakeActions{}
	s.deps.Actions = fa
	holdByCap(t, s)
	st := gatestest.NewStore(s.deps.Now)
	cons := &stoppedAfter{s: s}
	svc := gates.New(gates.Deps{Store: st, Reader: st, Profile: profileGates{s.loaded().Gates}, Clock: clockFunc(s.deps.Now), Releaser: cons})
	s.deps.Gates, s.deps.GateReader = svc, st
	g, err := svc.Open(ctx, gate.Gate{Kind: gate.KindPlan, Unit: r7.Unit(), Subject: round.HeldSubject(s.deps.Now())}, holdForYou{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Resolve(ctx, g.ID, gates.Resolution{Status: gate.Approved, Intent: "run_now", By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
	tickStopped(t, cons, func(ctx context.Context) error { return svc.Tick(ctx, s.deps.Now()) })
	runs := 0
	for _, c := range fa.calls {
		if strings.HasPrefix(c, "run ") {
			runs++
		}
	}
	if runs != 1 {
		t.Fatalf("calls %v: the held round ran %d times", fa.calls, runs)
	}
	var h api.Health
	if code := get(t, s, "/api/health", &h); code != 200 {
		t.Fatalf("health %d", code)
	}
	want := fmt.Sprintf("the plan gate %d on %s was approved, and the daemon stopped while its consequence ran", g.ID, r7.Unit().Label())
	if !slices.ContainsFunc(h.Warnings, func(w string) bool { return strings.HasPrefix(w, want) }) {
		t.Fatalf("warnings %q, want one naming the gate the stop cut", h.Warnings)
	}
}

func TestAReplayedGatedDelegateRunsOnce(t *testing.T) {
	t.Parallel()
	c := newCatalogRig(t)
	ctx := context.Background()
	store, gs := taskstest.New(), gatestest.NewStore(func() time.Time { return catalogNow })
	cons := &stoppedAfter{s: c.s}
	svc := gates.New(gates.Deps{Store: gs, Reader: gs, Profile: profileGates{c.s.loaded().Gates}, Clock: fixedClock(catalogNow), Decider: cons})
	c.s.deps.Gates, c.s.deps.GateReader = svc, gs
	states := proposingStates{States: taskstest.BundledStates, verb: task.VerbDelegate}
	c.s.deps.Tasks = coretasks.New(coretasks.Deps{States: states, Authority: states.Authority, Gates: rigTaskGates{svc, gs}, Store: store,
		Facts: coretasks.CardFacts{}, Clock: fixedClock(catalogNow)})
	e := task.Task{ID: "t-r", State: "inbox", Category: task.CategoryInbox, Units: []unit.Unit{r7u}, Origin: task.OriginDerived,
		Created: catalogNow, Updated: catalogNow}
	if err := store.Apply(ctx, []task.Change{{Op: task.OpCreate, Task: e}}); err != nil {
		t.Fatal(err)
	}
	if rec := post(t, c.s, api.TaskPath("t-r", "delegate"), `{"source":"cockpit"}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	open, err := gs.OfTask(ctx, "t-r")
	if err != nil || len(open) != 1 {
		t.Fatalf("gates %+v %v", open, err)
	}
	if _, err := gs.Resolve(ctx, open[0].ID, gates.Resolution{Status: gate.Approved, Intent: "approve", By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
	tickStopped(t, cons, func(ctx context.Context) error { return svc.Tick(ctx, catalogNow) })
	if intakes, err := c.s.deps.Intakes.Open(ctx); err != nil || len(intakes) != 1 {
		t.Fatalf("the approved delegate ran %d times: %+v %v", len(intakes), intakes, err)
	}
	if len(store.Events) != 1 || store.Events[0].Kind != "verb_delegate" {
		t.Fatalf("events %+v", store.Events)
	}
}

// storeTaskGates are the gates a gated verb opens through the gates service, read from the sqlite store.
type storeTaskGates struct {
	*gates.Service
	r gates.Reader
}

func (g storeTaskGates) Open(ctx context.Context, gt gate.Gate) (gate.Gate, error) {
	return g.Service.Open(ctx, gt, nil)
}
func (g storeTaskGates) Get(ctx context.Context, id int64) (gate.Gate, error) {
	return g.r.Get(ctx, id)
}
func (g storeTaskGates) OfTask(ctx context.Context, id task.ID) ([]gate.Gate, error) {
	return g.r.OfTask(ctx, id)
}

func TestAReplayedGatedReplySentAtOncePostsOnce(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	ctx := context.Background()
	st := r.db.Gates(r.clock.Now)
	cons := &stoppedAfter{s: r.s}
	svc := gates.New(gates.Deps{Store: st, Reader: st, Profile: profileGates{r.s.loaded().Gates}, Clock: r.clock, Decider: cons,
		Publisher: publishTo(func(ctx context.Context, g gate.Gate) error { return r.o.Publish(ctx, g) })})
	r.s.deps.Gates, r.s.deps.GateReader, r.o.Gates = svc, st, rigGates{svc, st}
	states := proposingStates{States: taskstest.BundledStates, verb: task.VerbReply}
	r.s.deps.Tasks = coretasks.New(coretasks.Deps{States: states, Authority: states.Authority, Gates: storeTaskGates{svc, st}, Store: r.db.Tasks(),
		Items: r.ledger, Facts: coretasks.CardFacts{Cards: r.db.Cards(), Me: func() task.Me { return meID }}, Me: meID, Clock: fixedClock(tasksNow)})
	r.put(t, unitTask("t-1", task.OriginDerived, r7u))
	if rec := post(t, r.s, api.TaskPath("t-1", "reply"), `{"source":"cockpit","args":{"text":"Shipped","send":"now"}}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if ds := r.drafts(t); len(ds) != 0 {
		t.Fatalf("a held reply drafted %+v", ds)
	}
	open, err := st.Where(ctx, gates.Query{Kind: gate.KindProposal})
	if err != nil || len(open) != 1 {
		t.Fatalf("gates %+v %v", open, err)
	}
	if _, err := st.Resolve(ctx, open[0].ID, gates.Resolution{Status: gate.Approved, Intent: "approve", By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
	tickStopped(t, cons, func(ctx context.Context) error { return svc.Tick(ctx, r.clock.Now()) })
	if _, _, err := r.o.Send(ctx); err != nil {
		t.Fatal(err)
	}
	if ds := r.drafts(t); len(ds) != 1 || len(r.fx.seen()) != 1 {
		t.Fatalf("drafts %+v, posts %v: the approved reply went out more than once", ds, r.fx.seen())
	}
}
