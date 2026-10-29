package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/gates/gatestest"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// proposingStates are the bundled states with verb held for a person behind the bundled proposal gate.
type proposingStates struct {
	coretasks.States
	verb task.Verb
}

func (p proposingStates) Gate(v string) (gate.Gate, bool) {
	if task.Verb(v) == p.verb {
		return gate.Gate{Name: "proposal", Kind: gate.KindProposal}, true
	}
	return p.States.Gate(v)
}

func (p proposingStates) Authority(ctx context.Context, v string, a task.Actor, t task.Task) (bool, bool, error) {
	if task.Verb(v) == p.verb && a == task.ActorPerson {
		return false, true, nil
	}
	return taskstest.BundledStates.Authority(ctx, v, a, t)
}

func TestAnApprovedGateRunsABridgedOrReplyVerb(t *testing.T) {
	t.Parallel()
	c := newCatalogRig(t)
	ctx := context.Background()
	store, gs := taskstest.New(), gatestest.NewStore(func() time.Time { return catalogNow })
	svc := gates.New(gates.Deps{Store: gs, Reader: gs, Profile: profileGates{c.s.loaded().Gates}, Clock: fixedClock(catalogNow), Decider: c.s})
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
	if err != nil || len(open) != 1 || open[0].Status != gate.Open {
		t.Fatalf("gates %+v %v", open, err)
	}
	if intakes, _ := c.s.deps.Intakes.Open(ctx); len(intakes) != 0 {
		t.Fatalf("a held delegate ran: %+v", intakes)
	}
	if rec := post(t, c.s, gatePath(open[0].ID, "approve"), `{}`); rec.Code != 200 {
		t.Fatalf("approving the gate: %d %s", rec.Code, rec.Body)
	}
	intakes, err := c.s.deps.Intakes.Open(ctx)
	if err != nil || len(intakes) != 1 || intakes[0].Unit != r7u || string(intakes[0].Intent) != "review" {
		t.Fatalf("the approved delegate did not run through the bridge: %+v %v", intakes, err)
	}
	if after, _ := gs.OfTask(ctx, "t-r"); len(after) != 1 || after[0].Status != gate.Approved {
		t.Fatalf("an approved delegate opened another gate: %+v", after)
	}
	if len(store.Events) != 1 || store.Events[0].Kind != "verb_delegate" || store.Events[0].Actor != "person:cockpit" {
		t.Fatalf("events %+v", store.Events)
	}
}

// rigTaskGates are the gates a gated verb opens through the gates service, read from its store.
type rigTaskGates struct {
	*gates.Service
	r *gatestest.Store
}

func (g rigTaskGates) Open(ctx context.Context, gt gate.Gate) (gate.Gate, error) {
	return g.Service.Open(ctx, gt, nil)
}
func (g rigTaskGates) Get(ctx context.Context, id int64) (gate.Gate, error) { return g.r.Get(ctx, id) }
func (g rigTaskGates) OfTask(ctx context.Context, id task.ID) ([]gate.Gate, error) {
	return g.r.OfTask(ctx, id)
}
