package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
)

func TestAGatedVerbOpensItsGateThroughServe(t *testing.T) {
	r := newProfileRig(t, "[verbs.done]\nby = { person = \"gate\", agent = \"false\" }\ngate = \"proposal\"\n")
	ctx := context.Background()
	r.put(t, "t-0000000000000001", "next", task.CategoryReady)
	g := r.te.newTasks(r.db, r.te.cfg, nil)

	res, err := g.Do(ctx, "t-0000000000000001", coretasks.Request{Verb: task.VerbDone, Actor: task.ActorPerson, Source: "cockpit"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Task.Category != task.CategoryReady || r.category(t, "t-0000000000000001") != task.CategoryReady {
		t.Errorf("a gated done closed the task")
	}
	got, err := r.db.Gates(func() time.Time { return testNow }).OfTask(ctx, "t-0000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != gate.KindProposal || got[0].Name != "proposal" || got[0].Status != gate.Open || len(got[0].Intents) == 0 {
		t.Fatalf("gates %+v", got)
	}
	if req, ok := coretasks.ProposedVerb(got[0]); !ok || req.Verb != task.VerbDone {
		t.Errorf("the gate proposes %+v %v", req, ok)
	}
	if _, err := g.Do(ctx, "t-0000000000000001", coretasks.Request{Verb: task.VerbDone, Actor: task.ActorAgent, Source: "claude"}); !errors.Is(err, coretasks.ErrForbidden) {
		t.Errorf("an agent on done, which by.agent refuses: %v", err)
	}
}

func TestAGatedVerbOpensItsGateThroughTheService(t *testing.T) {
	const tasksTOML = "[verbs.done]\nby = { person = \"gate\" }\ngate = \"proposal\"\n"
	r := newProfileRig(t, tasksTOML)
	if err := os.WriteFile(filepath.Join(r.dir, "gates.toml"), []byte("[[approval.rule]]\nwhen = 'gate.kind == \"proposal\"'\nmode = \"send\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.write(t, tasksTOML)
	ctx := context.Background()
	r.put(t, "t-0000000000000001", "next", task.CategoryReady)
	decided := &recordedDecisions{}
	r.te.Env.consequences = decided
	g := r.te.newTasks(r.db, r.te.cfg, nil)
	if _, err := g.Do(ctx, "t-0000000000000001", coretasks.Request{Verb: task.VerbDone, Actor: task.ActorPerson, Source: "cockpit"}); err != nil {
		t.Fatal(err)
	}
	if len(decided.gates) != 1 || decided.gates[0].Status != gate.Approved {
		t.Fatalf("the released gate's decisions %+v", decided.gates)
	}
	got, err := r.db.Gates(func() time.Time { return testNow }).OfTask(ctx, "t-0000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Status != gate.Approved || got[0].By != task.ActorWorkflow || got[0].Rule != "gates.toml approval.rule[0]" {
		t.Fatalf("a policy that releases proposals releases the gate at open: %+v", got)
	}
}

// recordedDecisions record the proposal gates decided and apply no other consequence.
type recordedDecisions struct{ gates []gate.Gate }

func (r *recordedDecisions) Release(context.Context, gate.Gate) error { return nil }
func (r *recordedDecisions) Unblock(context.Context, gate.Gate) error { return nil }
func (r *recordedDecisions) Deliver(context.Context, gate.Gate) error { return nil }
func (r *recordedDecisions) Decide(_ context.Context, g gate.Gate) error {
	r.gates = append(r.gates, g)
	return nil
}

func TestATaskAPersonEndsClosesTheGatesGatesTomlClosesWithIt(t *testing.T) {
	r := newProfileRig(t, "")
	ctx := context.Background()
	r.put(t, "t-0000000000000001", "next", task.CategoryReady)
	r.put(t, "t-0000000000000002", "next", task.CategoryReady)
	r.te.Env.consequences = &recordedDecisions{}
	g := r.te.newTasks(r.db, r.te.cfg, nil)
	svc := r.te.newGates(r.db, t.Logf)
	asked := map[task.ID]gate.Gate{}
	for _, id := range []task.ID{"t-0000000000000001", "t-0000000000000002"} {
		q, err := svc.Open(ctx, gate.Gate{Task: id, Unit: task.UnitOf(id), Kind: gate.KindQuestion, Subject: "9"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		asked[id] = q
	}
	if _, err := g.Do(ctx, "t-0000000000000001", coretasks.Request{Verb: task.VerbDone, Actor: task.ActorWorkflow, Source: "workflow"}); !errors.Is(err, coretasks.ErrVerb) {
		t.Fatalf("a workflow's done on a task its question holds: %v, want ErrVerb", err)
	}
	read := r.db.Gates(func() time.Time { return testNow })
	if got, _ := read.Get(ctx, asked["t-0000000000000001"].ID); got.Status != gate.Open {
		t.Fatalf("the question after the workflow's refused done = %+v, want open", got)
	}
	if _, err := g.Do(ctx, "t-0000000000000001", coretasks.Request{Verb: task.VerbDone, Actor: task.ActorPerson, Source: "cockpit"}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Do(ctx, "t-0000000000000002", coretasks.Request{Verb: task.VerbSomeday, Actor: task.ActorPerson, Source: "cockpit"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := read.Get(ctx, asked["t-0000000000000001"].ID); got.Status != gate.Dismissed || got.By != task.ActorPerson {
		t.Errorf("the question of the task a person ended = %+v, want dismissed by the person", got)
	}
	if got, _ := read.Get(ctx, asked["t-0000000000000002"].ID); got.Status != gate.Open {
		t.Errorf("the question of a task still open = %+v, want open", got)
	}
}
