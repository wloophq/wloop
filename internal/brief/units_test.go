package brief

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// fakeOnDemand is a provider whose briefs another job writes.
type fakeOnDemand struct {
	GitHubSource
	mu     sync.Mutex
	coming bool
	wants  []unit.Unit
	noted  func(unit.Unit)
}

func (f *fakeOnDemand) Want(_ context.Context, u unit.Unit) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.wants = append(f.wants, u)
	return f.coming, nil
}

func (f *fakeOnDemand) Watch(noted func(unit.Unit)) { f.noted = noted }

func TestASlackUnitIsBriefedOnDemand(t *testing.T) {
	r := newRig(t, 0)
	ctx := context.Background()
	src := &fakeOnDemand{coming: true}
	r.q.Register("slack", src)
	thread := unit.Unit{Provider: "slack", Key: "T000AAA/C000AAA/1700000000.000100"}
	a := Intakes{Q: r.q}
	if rd, err := a.Ready(ctx, thread, false, nil); err != nil || !rd.Go || !rd.Without {
		t.Fatalf("a later turn without a brief: %+v %v", rd, err)
	}
	changed, release := a.Changed(thread)
	defer release()
	rd, err := a.Ready(ctx, thread, true, nil)
	if err != nil || rd.Go || rd.Wait != WaitBriefing || !rd.Next.Equal(t0.Add(DemandTimeout)) || len(src.wants) != 1 {
		t.Fatalf("a first turn waits for the coming brief: %+v %v, wants %v", rd, err, src.wants)
	}
	if rd, _ := a.Peek(ctx, thread, true, nil); rd.Go {
		t.Fatalf("peek after the want: %+v", rd)
	}
	if err := r.db.Extraction().Save(ctx, sqlite.ExtractionSave{States: map[unit.Unit]sqlite.ExtractionState{
		thread: {Brief: `{"goal":"Ship the export","kind":"request","risk":"low","tags":["domain:billing"]}`, At: t0}}}); err != nil {
		t.Fatal(err)
	}
	src.noted(thread)
	closes(t, changed)
	got, err := r.q.Ready(ctx, thread, true, nil)
	if err != nil || got.State != Ready || got.Brief == nil || got.Brief.Goal != "Ship the export" || got.Brief.Kind != "request" || got.Run == 0 {
		t.Fatalf("the written brief: %+v %v", got, err)
	}

	dm := unit.Unit{Provider: "slack", Key: "T000AAA/D000AAA/seg/1700000000000300"}
	if rd, _ := a.Ready(ctx, dm, true, nil); rd.Go {
		t.Fatalf("a want: %+v", rd)
	}
	r.now = t0.Add(DemandTimeout)
	if rd, _ := a.Ready(ctx, dm, true, nil); !rd.Go || !rd.Without || !strings.Contains(rd.Note, "in time") {
		t.Fatalf("past the wait: %+v", rd)
	}
	src.coming = false
	idle := unit.Unit{Provider: "slack", Key: "T000AAA/C000AAA/1700000000.000500"}
	if rd, _ := a.Ready(ctx, idle, true, nil); !rd.Go || !rd.Without {
		t.Fatalf("nothing to read: %+v", rd)
	}
	other := unit.Unit{Provider: "jira", Key: "ACME-1"}
	if rd, _ := a.Ready(ctx, other, true, nil); !rd.Go || !rd.Without || !strings.Contains(rd.Note, "no brief source") {
		t.Fatalf("a provider without a source: %+v", rd)
	}
	r.q.d.Enabled = false
	if rd, _ := a.Ready(ctx, thread, true, nil); rd.Cause != intake.CauseOff {
		t.Fatalf("briefs off: %+v", rd)
	}
}

func TestAFirstTurnWaitsForTheFieldsItsIntentNeeds(t *testing.T) {
	r := newRig(t, 1)
	ctx := context.Background()
	u := refN(1).Unit()
	if _, err := r.q.Run(ctx); err != nil || r.run.calls != 1 {
		t.Fatalf("the core brief: %d, %v", r.run.calls, err)
	}
	c := composed(t)
	r.q.d.Intent = func(unit.ProviderID) Intent { return Intent{Schema: c} }
	if rd, err := r.q.Ready(ctx, u, true, nil); err != nil || rd.State != Ready {
		t.Fatalf("no need: %+v %v", rd, err)
	}
	r.run.fields = map[string]any{"approach": "rewrite"}
	changed, release := r.q.Changed(u)
	defer release()
	if rd, err := r.q.Peek(ctx, u, true, []string{"approach"}); err != nil || rd.State != Wait || r.run.calls != 1 {
		t.Fatalf("peek: %+v %v, %d calls", rd, err, r.run.calls)
	}
	if rd, err := r.q.Ready(ctx, u, true, []string{"approach"}); err != nil || rd.State != Wait {
		t.Fatalf("a first turn needing approach: %+v %v", rd, err)
	}
	closes(t, changed)
	r.settled()
	rd, err := r.q.Ready(ctx, u, true, []string{"approach"})
	if err != nil || rd.State != Ready || rd.Brief.Extra["approach"] != "rewrite" || rd.Brief.Goal != "g" || r.run.calls != 2 {
		t.Fatalf("after the fields call: %+v %v, %d calls", rd, err, r.run.calls)
	}

	r2 := newRig(t, 1)
	r2.q.Run(ctx)
	r2.q.d.Intent = r.q.d.Intent
	r2.run.failFields = errors.New("boom")
	if rd, _ := r2.q.Ready(ctx, u, true, []string{"solution"}); rd.State != Wait {
		t.Fatalf("%+v", rd)
	}
	r2.settled()
	if rd, _ := r2.q.Ready(ctx, u, true, []string{"solution"}); rd.State != Ready || rd.Brief.Extra != nil {
		t.Fatalf("a failed fields call lets the turn go: %+v", rd)
	}
	if rd, _ := r2.q.Ready(ctx, u, false, []string{"solution"}); rd.State != Ready || r2.run.calls != 2 {
		t.Fatalf("a later turn: %+v, %d calls", rd, r2.run.calls)
	}
}

func TestAQueueThatCannotBriefStillBriefsOnDemand(t *testing.T) {
	r := newRig(t, 1)
	r.q.d.Runner, r.q.d.Unavailable = nil, "no agent answers"
	ctx := context.Background()
	src := &fakeOnDemand{coming: true}
	r.q.Register("slack", src)
	a := Intakes{Q: r.q}
	pr := refN(1).Unit()
	for name, ask := range map[string]func(context.Context, unit.Unit, bool, []string) (intake.Readiness, error){"ready": a.Ready, "peek": a.Peek} {
		rd, err := ask(ctx, pr, true, nil)
		if err != nil || !rd.Go || !rd.Without || !strings.Contains(rd.Note, "no agent answers") {
			t.Fatalf("%s: a pull request's first turn: %+v %v", name, rd, err)
		}
	}
	if filled, err := r.q.Fill(ctx, pr, nil); filled || err != nil {
		t.Fatalf("a fill: %v %v", filled, err)
	}
	thread := unit.Unit{Provider: "slack", Key: "T000AAA/C000AAA/1700000000.000100"}
	if rd, err := a.Ready(ctx, thread, true, nil); err != nil || rd.Go || rd.Wait != WaitBriefing || len(src.wants) != 1 {
		t.Fatalf("a Slack first turn: %+v %v", rd, err)
	}
	if r.run.calls != 0 {
		t.Fatalf("%d calls", r.run.calls)
	}
}
