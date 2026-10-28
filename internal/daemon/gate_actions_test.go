package daemon

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/gates/gatestest"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/gate"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

func TestRunNowResolvesThePlanGate(t *testing.T) {
	t.Parallel()
	r := newPlanRig(t)
	r.sweep(t)
	it := r.pending7(t)
	g := r.planGate(t, it)
	row, _ := row7(t, r.s, api.ViewAll)
	st, ok := offerOf(row, "run_now")
	if !ok || !st.Enabled || row.Pending == nil || !row.Pending.Deadline.Equal(g.Deadline) {
		t.Fatalf("the row of the pending round offers %+v %v, pending %+v; want run_now until the gate's deadline %s", st, ok, row.Pending, g.Deadline)
	}
	path := "/api/units/github/acme%2Fapi%237/run_now"
	body := fmt.Sprintf(`{"source":"cockpit","basis":%q}`, st.Basis)
	if rec := postFrom(t, r.s, "term_1", path, body); rec.Code != http.StatusForbidden {
		t.Fatalf("an agent session's run_now: %d %s", rec.Code, rec.Body)
	}
	if got, _ := r.gr.Get(context.Background(), g.ID); got.Status != gate.Open || len(r.steps.Ran()) != 0 {
		t.Fatalf("the refused run_now left the gate %s and ran %+v", got.Status, r.steps.Ran())
	}
	if rec := post(t, r.s, path, body); rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	got, _ := r.gr.Get(context.Background(), g.ID)
	if got.Status != gate.Approved || got.Intent != "run_now" || got.By != task.ActorPerson || got.Rule != "" {
		t.Fatalf("the gate = %+v, want approved run_now by a person", got)
	}
	if ran := r.steps.Ran(); len(ran) != 1 || ran[0].Unit != u7 || !ran[0].Run.ByHand {
		t.Fatalf("ran %+v, want the round once by hand", ran)
	}
	if run, ok := r.marked(t, it); !ok || run.Status != workflow.RunDone {
		t.Fatalf("run %+v %v", run, ok)
	}
	if row, _ := row7(t, r.s, api.ViewAll); row.Pending != nil {
		t.Fatalf("the round still shows pending: %+v", row.Pending)
	}
	if rec := post(t, r.s, path, `{"source":"cockpit"}`); rec.Code == http.StatusOK {
		t.Fatalf("a second run_now with nothing pending or held: %d %s", rec.Code, rec.Body)
	}
}

func TestTrustRunResolvesTheTrustGate(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		intent, call, kind, data string
		status                   gate.Status
	}{
		{"trust_run", "trust_run acme/api#7", dinv.KindRoundSent, "trusted its folder and ran", gate.Approved},
		{"unblock", "unblock acme/api#7", dinv.KindRoundUnblocked, "dismissed by you", gate.Dismissed},
	} {
		t.Run(c.intent, func(t *testing.T) {
			t.Parallel()
			r := newPlanRig(t)
			fa := &fakeActions{}
			r.s.deps.Actions = fa
			ctx := context.Background()
			b := inventory.StartBlock{Question: turns.QuestionTrust, Root: "/src/api", Folder: "/src/api", Homes: []string{"/home/.claude"}, Round: "triage"}
			at := r.clock.Now().Add(-time.Minute)
			r.s.store().AppendEvents(ctx, []dinv.Event{{PR: r7, Kind: dinv.KindRoundBlocked, Key: b.Key(at), At: at, Data: "could not start: /src/api is not trusted"}})
			g, err := r.gates.Open(ctx, gate.Gate{Kind: gate.KindTrust, Unit: u7, Subject: b.Key(at)}, holdGate{})
			if err != nil {
				t.Fatal(err)
			}
			path := "/api/units/github/acme%2Fapi%237/" + c.intent
			if rec := postFrom(t, r.s, "term_1", path, `{"source":"cockpit"}`); rec.Code != http.StatusForbidden || len(fa.calls) != 0 {
				t.Fatalf("an agent session's %s: %d %s, calls %v", c.intent, rec.Code, rec.Body, fa.calls)
			}
			if rec := post(t, r.s, path, `{"source":"cockpit"}`); rec.Code != http.StatusOK {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
			got, _ := r.gr.Get(ctx, g.ID)
			if got.Status != c.status || got.Intent != c.intent || got.By != task.ActorPerson {
				t.Fatalf("the trust gate = %+v, want %s %s by a person", got, c.status, c.intent)
			}
			if !slices.Equal(fa.calls, []string{c.call}) {
				t.Fatalf("calls %v, want %s", fa.calls, c.call)
			}
			evs, _ := r.s.store().Events(ctx, r7)
			if last := evs[len(evs)-1]; last.Kind != c.kind || last.Data != c.data {
				t.Fatalf("last event %+v", last)
			}
			if open, _ := r.gr.Where(ctx, gates.Query{Kind: gate.KindTrust}); len(open) != 0 {
				t.Fatalf("trust gates still open: %+v", open)
			}
		})
	}
}

func TestAnAgentSessionCannotApplyAGatedPlanItem(t *testing.T) {
	t.Parallel()
	r := newPlanRig(t)
	r.sweep(t)
	it := r.pending7(t)
	g := r.planGate(t, it)
	sess := r.items(t, r.sess, workflow.DecisionPending)[0]
	for _, c := range []struct{ id, action string }{{it.ID, "apply"}, {it.ID, "guide"}, {it.ID, "dismiss"}, {sess.ID, "apply"}} {
		body := fmt.Sprintf(`{"ids":[%q],"action":%q,"guidance":"look"}`, c.id, c.action)
		if rec := postFrom(t, r.s, "term_1", api.Plan, body); rec.Code != http.StatusForbidden {
			t.Fatalf("an agent session's %s on %s: %d %s", c.action, c.id, rec.Code, rec.Body)
		}
	}
	if got, _ := r.gr.Get(context.Background(), g.ID); got.Status != gate.Open {
		t.Fatalf("the agent's apply left the gate %+v", got)
	}
	if len(r.steps.Ran()) != 0 || len(r.items(t, u7, workflow.DecisionPending)) != 1 {
		t.Fatalf("the agent's apply ran %+v", r.steps.Ran())
	}
	if run, ok := r.marked(t, it); ok {
		t.Fatalf("the agent's dismiss marked %+v", run)
	}
}

func TestAnAgentSessionCannotRunAHeldRound(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	fa := &fakeActions{}
	s.deps.Actions = fa
	holdByCap(t, s)
	for _, name := range []string{"run_now", "guide", "skip", "trust_run", "unblock"} {
		if rec := postFrom(t, s, "term_1", "/api/units/github/acme%2Fapi%237/"+name, `{"source":"cockpit","guidance":"x"}`); rec.Code != http.StatusForbidden {
			t.Fatalf("an agent session's %s on a held round: %d %s", name, rec.Code, rec.Body)
		}
	}
	if len(fa.calls) != 0 {
		t.Fatalf("calls %v", fa.calls)
	}
	if o, _ := s.store().Override(context.Background(), r7); o.NextGuidance != "" {
		t.Fatalf("the agent kept guidance %q", o.NextGuidance)
	}
}

func TestAWindowEndsAtItsDeadlineOnTheGatesTick(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		st := gatestest.NewStore(time.Now)
		cons := &releases{}
		svc := gates.New(gates.Deps{Store: st, Reader: st, Profile: profileGates{(&Server{}).loaded().Gates}, Clock: clockFunc(time.Now), Releaser: cons})
		g, err := svc.Open(ctx, gate.Gate{Kind: gate.KindPlan, Unit: u7, Subject: "review-follow-up/github/acme%2Fapi%237/k"}, delayFor{45 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		s := &Server{deps: Deps{Gates: svc, Log: t.Logf}, draftClock: wallClock{}}
		done := make(chan struct{})
		go func() { defer close(done); s.tickGates(ctx) }()
		defer func() { cancel(); <-done }()
		time.Sleep(45*time.Second - time.Nanosecond)
		synctest.Wait()
		if got, _ := st.Get(ctx, g.ID); got.Status != gate.Open || len(cons.ids()) != 0 {
			t.Fatalf("before its deadline the gate is %s, released %v", got.Status, cons.ids())
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		got, _ := st.Get(ctx, g.ID)
		if got.Status != gate.Approved || got.Rule != gates.RuleWindow || !got.ClosedAt.Equal(g.Deadline) {
			t.Fatalf("at its deadline the gate = %+v, want approved by its window at %s", got, g.Deadline)
		}
		if got := cons.ids(); !slices.Equal(got, []int64{g.ID}) {
			t.Fatalf("released %v, want gate %d once", got, g.ID)
		}
	})
}

// delayFor opens a gate with a window of its duration.
type delayFor struct{ window time.Duration }

func (d delayFor) Decide(context.Context, gate.Gate) (gates.Decision, error) {
	return gates.Decision{Mode: gate.ModeDelay, Window: d.window}, nil
}

// releases records the plan gates released, read by the test while the
// tick runs.
type releases struct {
	mu  sync.Mutex
	got []int64
}

func (r *releases) Release(_ context.Context, g gate.Gate) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, g.ID)
	return nil
}

func (r *releases) ids() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.got)
}
