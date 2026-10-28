package daemon

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/gates"
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
