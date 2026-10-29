package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/gates/gatestest"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// postFrom posts body to path as a request from the agent session handle carries.
func postFrom(t *testing.T, s *Server, handle, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := apiRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(api.SessionHeader, handle)
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// agentsClose is the bundled authority with by.agent = "true" on done.
func agentsClose(ctx context.Context, verb string, a task.Actor, t task.Task) (bool, bool, error) {
	if verb == string(task.VerbDone) && a == task.ActorAgent {
		return true, false, nil
	}
	return taskstest.BundledStates.Authority(ctx, verb, a, t)
}

func (r *tasksRig) doneEvents(t *testing.T) []workflow.Event {
	t.Helper()
	evs, err := r.db.Events().Since(context.Background(), 0, []string{"verb_done"})
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

func TestDoneFromAnAgentSessionNeedsTheProfilesByAgent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newTasksRig(t, nil)
	r.put(t, task.Task{ID: "e-1", State: "next", Category: task.CategoryReady})
	if rec := postFrom(t, r.s, "term_1", api.TaskPath("e-1", "done"), `{"source":"claude"}`); rec.Code != 403 {
		t.Fatalf("an agent's done under the bundled profile: %d %s", rec.Code, rec.Body)
	}
	if e, _ := r.db.Tasks().Task(ctx, "e-1"); e.Category != task.CategoryReady {
		t.Fatalf("the refused done moved the task to %s", e.State)
	}

	r.s.deps.Tasks = coretasks.New(coretasks.Deps{States: taskstest.BundledStates, Authority: agentsClose, Store: r.db.Tasks(), Items: r.ledger,
		Facts: coretasks.CardFacts{Cards: r.db.Cards()}, Me: meID, Clock: fixedClock(tasksNow)})
	if rec := postFrom(t, r.s, "term_1", api.TaskPath("e-1", "done"), `{"source":"claude"}`); rec.Code != 200 {
		t.Fatalf("an agent's done with by.agent: %d %s", rec.Code, rec.Body)
	}
	if evs := r.doneEvents(t); len(evs) != 1 || evs[0].Actor != "agent:claude" {
		t.Fatalf("events %+v", evs)
	}

	r.put(t, task.Task{ID: "e-2", State: "next", Category: task.CategoryReady})
	if rec := post(t, r.s, api.TaskPath("e-2", "done"), `{"source":"cockpit"}`); rec.Code != 200 {
		t.Fatalf("a person's done: %d %s", rec.Code, rec.Body)
	}
	if evs := r.doneEvents(t); len(evs) != 2 || evs[1].Actor != "person:cockpit" {
		t.Fatalf("events %+v", evs)
	}
}

// gatingStates are the bundled states with verb gated for a person behind the proposal gate.
type gatingStates struct {
	coretasks.States
	verb task.Verb
}

func (g gatingStates) Gate(v string) (gate.Gate, bool) {
	if task.Verb(v) == g.verb {
		return gate.Gate{Name: "proposal", Kind: gate.KindProposal, Intents: []string{"accept", "reject"}}, true
	}
	return g.States.Gate(v)
}

func (g gatingStates) Authority(ctx context.Context, v string, a task.Actor, t task.Task) (bool, bool, error) {
	if task.Verb(v) == g.verb && a == task.ActorPerson {
		return false, true, nil
	}
	return taskstest.BundledStates.Authority(ctx, v, a, t)
}

func TestAGatedDelegateOnABridgedTaskOpensTheGateAndRunsNothing(t *testing.T) {
	t.Parallel()
	c := newCatalogRig(t)
	store, gs := taskstest.New(), gatestest.NewStore(func() time.Time { return catalogNow })
	states := gatingStates{States: taskstest.BundledStates, verb: task.VerbDelegate}
	c.s.deps.Tasks = coretasks.New(coretasks.Deps{States: states, Authority: states.Authority, Gates: gs, Store: store,
		Facts: coretasks.CardFacts{}, Clock: fixedClock(catalogNow)})
	e := task.Task{ID: "e-r", Kind: task.KindAction, State: "inbox", Category: task.CategoryInbox, Units: []unit.Unit{r7u}, Origin: task.OriginDerived,
		Created: catalogNow, Updated: catalogNow}
	if err := store.Apply(context.Background(), []task.Change{{Op: task.OpCreate, Task: e}}); err != nil {
		t.Fatal(err)
	}
	rec := post(t, c.s, api.TaskPath("e-r", "delegate"), `{"source":"cockpit"}`)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "accepted as intake") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	got, err := gs.OfTask(context.Background(), "e-r")
	if err != nil || len(got) != 1 || got[0].Status != gate.Open {
		t.Fatalf("gates %+v %v", got, err)
	}
	if len(store.Events) != 0 {
		t.Errorf("a gated delegate recorded %+v", store.Events)
	}
}

func TestAnAgentSessionWritesNoChat(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, task.Task{ID: "e-1", State: "next", Category: task.CategoryReady})
	if rec := postFrom(t, r.s, "term_1", api.TaskPath("e-1", "chat"), `{"text":"Ship it."}`); rec.Code != http.StatusForbidden {
		t.Fatalf("an agent session's chat: %d %s", rec.Code, rec.Body)
	}
}
