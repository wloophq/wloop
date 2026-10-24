package extract

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type deadlineAsker struct {
	*jobAsker
	deadlines []time.Duration
	slow      func(q turns.Ask) bool
	clock     *jobClock
}

func (a *deadlineAsker) Ask(ctx context.Context, q turns.Ask) (turns.Answer, error) {
	if d, ok := ctx.Deadline(); ok {
		a.deadlines = append(a.deadlines, time.Until(d))
	} else {
		a.deadlines = append(a.deadlines, 0)
	}
	if a.slow(q) {
		a.clock.set(a.clock.Now().Add(PassBudget + time.Minute))
	}
	return a.jobAsker.Ask(ctx, q)
}

func TestLongCallOutlivesThePassBudget(t *testing.T) {
	r := newRig(t)
	a := &deadlineAsker{jobAsker: r.asker, clock: r.clock, slow: func(q turns.Ask) bool { return strings.Contains(q.Prompt, "marathon") }}
	r.job.Asker = a
	r.facts.post(jChan, ts(1), "", jOther, "a marathon conversation", jT0)
	r.facts.post(jChan, ts(2), "", jOther, "a short one", jT0.Add(time.Second))
	_, skipped := r.run(jT0.Add(2 * time.Minute))
	if n := r.asker.calls(); n != 1 {
		t.Fatalf("%d calls in the first pass, want the long one only", n)
	}
	if !strings.Contains(skipped, "pass budget") || !strings.Contains(skipped, "1 units wait") {
		t.Fatalf("skipped %q", skipped)
	}
	if d := a.deadlines[0]; d <= CallTimeout-time.Minute || d > CallTimeout {
		t.Fatalf("the call's deadline is %v away, want its own timeout %v", d, CallTimeout)
	}
	calls, _, err := r.db.Extraction().Day(context.Background(), day(r.clock.Now()))
	if err != nil || calls != 1 {
		t.Fatalf("recorded %d calls: %v", calls, err)
	}
	r.run(r.clock.Now().Add(30 * time.Second))
	r.run(r.clock.Now().Add(30 * time.Second))
	if n, long := r.asker.calls(), callsWith(r, "marathon"); n != 2 || long != 1 {
		t.Fatalf("%d calls, %d of the long unit; want the short one next and the long one paid once", n, long)
	}
}

func TestLinkToDisabledProviderIsDropped(t *testing.T) {
	r := newRig(t)
	r.ledger.Ledger.Enabled = func(p unit.ProviderID) bool { return p != unit.GitHub }
	pr := unit.Unit{Provider: unit.GitHub, Key: "acme/widget#527"}
	r.facts.post(jChan, ts(1), "", jOther, "<@U000AAA> please review PR 527", jT0)
	r.asker.reply = func(int, turns.Ask) ([]byte, error) {
		return answerJSON(t, []obj{jItem("n1", ts(1))}, nil,
			[]obj{{"item": "n1", "target": "github:" + pr.Key, "kind": "review", "evidence_ts": ts(1)}}, nil), nil
	}
	for at := jT0.Add(2 * time.Minute); at.Before(jT0.Add(2 * time.Hour)); at = at.Add(30 * time.Second) {
		r.run(at)
	}
	if n := r.asker.calls(); n != 1 {
		t.Fatalf("%d calls, want 1", n)
	}
	its := r.items(thread(ts(1)))
	if len(its) != 1 || its[0].State != items.Open {
		t.Fatalf("items %+v", its)
	}
	ls, err := r.db.Links().From(context.Background(), thread(ts(1)))
	if err != nil || len(ls) != 0 {
		t.Fatalf("links %+v %v", ls, err)
	}
	if !strings.Contains(strings.Join(r.logs, "\n"), "1 links to github dropped") {
		t.Fatalf("logs %v", r.logs)
	}
}

func TestDisabledUnitGivesUp(t *testing.T) {
	r := newRig(t)
	r.ledger.Ledger.Enabled = func(p unit.ProviderID) bool { return p != slack.Provider }
	r.facts.post(jChan, ts(1), "", jOther, "please review", jT0)
	r.asker.reply = func(int, turns.Ask) ([]byte, error) {
		return answerJSON(t, []obj{jItem("n1", ts(1))}, nil, nil, nil), nil
	}
	for at := jT0.Add(2 * time.Minute); at.Before(jT0.Add(3 * time.Hour)); at = at.Add(30 * time.Second) {
		r.clock.set(at)
		_, _, _ = r.job.Run(context.Background())
	}
	if n := r.asker.calls(); n != 3 {
		t.Fatalf("%d calls, want 3 before giving up", n)
	}
	if w := r.job.Warnings(); len(w) != 1 || !strings.Contains(w[0], "gave up") {
		t.Fatalf("warnings %v", w)
	}
}

func timingOut(t *testing.T, r *rig, out func(q turns.Ask) bool) func(int, turns.Ask) ([]byte, error) {
	return func(_ int, q turns.Ask) ([]byte, error) {
		if out(q) {
			r.clock.set(r.clock.Now().Add(CallTimeout))
			return nil, fmt.Errorf("claude: %w", context.DeadlineExceeded)
		}
		return answerJSON(t, nil, nil, nil, nil), nil
	}
}

func TestTimingOutUnitGivesUpWhileOthersSucceed(t *testing.T) {
	r := newRig(t)
	r.asker.reply = timingOut(t, r, func(q turns.Ask) bool { return strings.Contains(q.Prompt, "marathon") })
	r.facts.post(jDM, ts(1), "", jOther, "a marathon conversation", jT0)
	end := jT0.Add(11 * time.Hour)
	for i := 0; jT0.Add(time.Duration(i) * 10 * time.Minute).Before(end); i++ {
		r.facts.post(jChan, ts(100+i), "", jOther, fmt.Sprintf("ask %d", i), jT0.Add(time.Duration(i)*10*time.Minute))
	}
	for r.clock.Now().Before(end) {
		r.run(r.clock.Now().Add(30 * time.Second))
	}
	dm, all := callsWith(r, "marathon"), r.asker.calls()
	if dm > 5 {
		t.Fatalf("the timing-out DM made %d of %d calls", dm, all)
	}
	if logs := strings.Join(r.logs, "\n"); strings.Contains(logs, EventBreakerOpened) {
		t.Fatalf("one timing-out unit opened the breaker:\n%s", logs)
	}
	if healthy := all - dm; healthy < 25 {
		t.Fatalf("healthy units got %d calls", healthy)
	}
	w := strings.Join(r.job.Warnings(), "\n")
	if !strings.Contains(w, "gave up") || !strings.Contains(w, "timed out") {
		t.Fatalf("warnings %q", w)
	}
}

func TestTimeoutOutageGivesUpOnNothing(t *testing.T) {
	r := newRig(t)
	down := true
	r.asker.reply = timingOut(t, r, func(turns.Ask) bool { return down })
	postUnits(r, 6)
	for r.clock.Now().Before(jT0.Add(4 * time.Hour)) {
		r.run(r.clock.Now().Add(30 * time.Second))
	}
	if w := strings.Join(r.job.Warnings(), "\n"); !strings.Contains(w, "timed out") {
		t.Fatalf("warnings during the outage %q", w)
	}
	down = false
	before := r.asker.calls()
	for r.clock.Now().Before(jT0.Add(8 * time.Hour)) {
		r.run(r.clock.Now().Add(30 * time.Second))
	}
	if n := r.asker.calls() - before; n != 6 {
		t.Fatalf("%d units extracted after the outage, want 6", n)
	}
	for _, w := range r.job.Warnings() {
		if strings.Contains(w, "gave up") {
			t.Fatalf("an outage gave up on units: %v", w)
		}
	}
}
