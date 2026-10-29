package gates_test

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/gates/gatestest"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type prItems map[unit.Unit][]items.Item

func (m prItems) Items(_ context.Context, u unit.Unit) ([]items.Item, error) { return m[u], nil }

type prCards struct{}

func (prCards) UnitFacts(_ context.Context, u unit.Unit) (task.UnitFacts, bool, error) {
	return task.UnitFacts{Unit: u, Cares: true, Carded: true, Kind: "pull-request"}, true, nil
}

func TestAPlanGateOnAPullRequestOpensNoCaptureTask(t *testing.T) {
	ctx := context.Background()
	me := items.Person{Key: "github:me"}
	follow := func(u unit.Unit) items.Item {
		return items.Item{ID: "gh:follow", Unit: u, Summary: "Follow " + u.Key, Requester: me, WaitingOn: []items.Person{{Key: "system:loop"}},
			State: items.Open, TaskKey: u.String()}
	}
	early := unit.Unit{Provider: unit.GitHub, Key: "acme/api#2"}
	store := taskstest.New()
	svc := coretasks.New(coretasks.Deps{Store: store, Items: prItems{acme: {follow(acme)}, early: {follow(early)}}, Facts: prCards{},
		Me: task.Me{Keys: []string{me.Key}}, Clock: clock{fixedNow}, States: taskstest.BundledStates})
	if err := svc.Sync(ctx, []unit.Unit{acme}); err != nil {
		t.Fatal(err)
	}
	gs := gatestest.NewStore(fixedNow)
	g := gates.New(gates.Deps{Store: gs, Reader: gs, Profile: bundled, Tasks: svc, Clock: clock{fixedNow}})
	round, err := g.Open(ctx, gate.Gate{Kind: gate.KindPlan, Unit: acme, Subject: "7"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts, err := store.TasksOf(ctx, acme)
	if err != nil || len(ts) != 1 || round.Task != ts[0].ID {
		t.Fatalf("the round's gate on %s, the pull request's tasks %+v %v", round.Task, ts, err)
	}
	early1, err := g.Open(ctx, gate.Gate{Kind: gate.KindPlan, Unit: early, Subject: "8"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Sync(ctx, []unit.Unit{early}); err != nil {
		t.Fatal(err)
	}
	ts, err = store.TasksOf(ctx, early)
	if err != nil || len(ts) != 1 || ts[0].ID != early1.Task || len(ts[0].Items) != 1 || ts[0].Origin != task.OriginDerived || ts[0].Title != "Follow acme/api#2" {
		t.Fatalf("a round gated before the pull request's items came: gate on %s, tasks %+v %v", early1.Task, ts, err)
	}
}

func TestAGateOnAPullRequestWhoseTaskIsDoneReopensIt(t *testing.T) {
	ctx := context.Background()
	store := taskstest.New()
	done := task.Task{ID: "t-00000000000000d1", State: "done", Category: task.CategoryDone, Title: "Review acme/api#7", Units: []unit.Unit{acme},
		Resolved: []task.ItemRef{{Unit: acme, Item: "gh:review"}}, Origin: task.OriginDerived, Created: fixedNow(), Updated: fixedNow()}
	if err := store.Apply(ctx, []task.Change{{Op: task.OpCreate, Task: done}}); err != nil {
		t.Fatal(err)
	}
	review := items.Item{ID: "gh:review", Unit: acme, State: items.Resolved, TaskKey: acme.String()}
	svc := coretasks.New(coretasks.Deps{Store: store, Items: prItems{acme: {review}}, Facts: prCards{}, Clock: clock{fixedNow}, States: taskstest.BundledStates})
	gs := gatestest.NewStore(fixedNow)
	g := gates.New(gates.Deps{Store: gs, Reader: gs, Profile: bundled, Tasks: svc, Clock: clock{fixedNow}})
	q, err := g.Open(ctx, gate.Gate{Kind: gate.KindQuestion, Unit: acme}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts, err := store.TasksOf(ctx, acme)
	if err != nil || len(ts) != 1 || q.Task != done.ID || ts[0].Category != task.CategoryReady || !ts[0].Changed {
		t.Fatalf("the question's gate on %s, the pull request's tasks %+v %v", q.Task, ts, err)
	}
}
