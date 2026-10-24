package extract

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/links"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/link"
	"github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// poisonReply answers the thread holding "poison" with a ref no ledger holds (unfit).
func poisonReply(t *testing.T) func(int, turns.Ask) ([]byte, error) {
	return func(_ int, q turns.Ask) ([]byte, error) {
		if strings.Contains(q.Prompt, "poison") {
			return answerJSON(t, []obj{jItem("x9", ts(1))}, nil, nil, nil), nil
		}
		return answerJSON(t, nil, nil, nil, nil), nil
	}
}

func callsWith(r *rig, text string) int {
	n := 0
	for _, a := range r.asker.asks {
		if strings.Contains(a.Prompt, text) {
			n++
		}
	}
	return n
}

func TestFailingUnitDoesNotStarveOthers(t *testing.T) {
	r := newRig(t)
	r.asker.reply = poisonReply(t)
	r.facts.post(jChan, ts(1), "", jOther, "poison", jT0)
	r.facts.post(jChan, ts(50), "", jOther, "healthy", jT0.Add(20*time.Minute))
	for at := jT0.Add(2 * time.Minute); at.Before(jT0.Add(time.Hour)); at = at.Add(30 * time.Second) {
		r.run(at)
	}
	if n := callsWith(r, "healthy"); n != 1 {
		t.Fatalf("the healthy unit got %d calls, want 1", n)
	}
	for _, w := range r.job.Warnings() {
		if strings.Contains(w, "breaker") {
			t.Fatalf("an unfit answer opened the breaker: %v", w)
		}
	}
}

func TestFailingUnitGivesUp(t *testing.T) {
	r := newRig(t)
	r.asker.reply = poisonReply(t)
	r.facts.post(jChan, ts(1), "", jOther, "poison", jT0)
	for at := jT0.Add(2 * time.Minute); at.Before(jT0.Add(3 * time.Hour)); at = at.Add(30 * time.Second) {
		r.run(at)
	}
	if n := r.asker.calls(); n != 3 {
		t.Fatalf("%d calls, want 3 before giving up", n)
	}
	if w := r.job.Warnings(); len(w) != 1 || !strings.Contains(w[0], "gave up") {
		t.Fatalf("warnings %v", w)
	}
	r.facts.post(jChan, ts(2), ts(1), jOther, "poison again", jT0.Add(3*time.Hour))
	r.run(jT0.Add(3*time.Hour + 2*time.Minute))
	if n := r.asker.calls(); n != 4 {
		t.Fatalf("%d calls after a new change, want 4", n)
	}
}

type failWriter struct{}

func (failWriter) Apply(context.Context, string, string, []slack.Span) ([]unit.Unit, error) {
	return nil, errors.New("span overlap")
}

func TestApplyFailureBacksOff(t *testing.T) {
	r := newRig(t)
	r.job.Writer = failWriter{}
	r.facts.post(jDM, ts(1), "", jOther, "hi", jT0)
	for i := range 20 {
		r.clock.set(jT0.Add(2*time.Minute + time.Duration(i)*30*time.Second))
		_, _, _ = r.job.Run(context.Background())
	}
	if n := r.asker.calls(); n != 3 {
		t.Fatalf("%d paid calls for a failing apply, want 3", n)
	}
}

func TestReviewBetweenMessageAndRunResolves(t *testing.T) {
	r := newRig(t)
	u := thread(ts(1))
	pr := unit.Unit{Provider: unit.GitHub, Key: "acme/widget#527"}
	msgAt := tsTime(ts(1))
	r.facts.post(jChan, ts(1), "", jOther, "<@U000AAA> please review PR 527", msgAt)
	r.asker.reply = func(int, turns.Ask) ([]byte, error) {
		return answerJSON(t, []obj{jItem("n1", ts(1))}, nil,
			[]obj{{"item": "n1", "target": "github:" + pr.Key, "kind": "review", "evidence_ts": ts(1)}}, nil), nil
	}
	r.run(msgAt.Add(2 * time.Minute))
	at := msgAt.Add(60 * time.Second)
	ev := link.Event{Unit: pr, Kind: link.EventReview, Actor: "email:ann@example.test", At: at, Anchor: unit.Anchor{Unit: pr, Ref: "review:1", At: at}}
	res := &links.Resolver{Links: r.db.Links(), Events: map[unit.ProviderID]links.EventReader{unit.GitHub: reviewEvents{[]link.Event{ev}}},
		Ledger: r.ledger.Ledger, Clock: r.clock}
	if n, err := res.Run(context.Background(), []unit.Unit{pr}); err != nil || n != 1 {
		t.Fatalf("resolved %d %v", n, err)
	}
	if its := r.items(u); its[0].State != items.Resolved {
		t.Fatalf("item %+v", its[0])
	}
}

func TestTransientConversationErrorKeepsTheChange(t *testing.T) {
	r := newRig(t)
	r.facts.convErrs = 1
	r.facts.post(jChan, ts(1), "", jOther, "please review", jT0)
	r.clock.set(jT0.Add(2 * time.Minute))
	if _, _, err := r.job.Run(context.Background()); err == nil {
		t.Fatal("a transient error passed silently")
	}
	r.run(jT0.Add(3 * time.Minute))
	if n := r.asker.calls(); n != 1 {
		t.Fatalf("%d calls after the error cleared, want the change extracted", n)
	}
}

func TestOutageGivesUpOnNothing(t *testing.T) {
	r := newRig(t)
	down := true
	r.asker.reply = func(int, turns.Ask) ([]byte, error) {
		if down {
			return nil, errors.New("agent down")
		}
		return answerJSON(t, nil, nil, nil, nil), nil
	}
	postUnits(r, 6)
	at := jT0.Add(2 * time.Minute)
	for ; at.Before(jT0.Add(2 * time.Hour)); at = at.Add(30 * time.Second) {
		r.run(at)
	}
	down = false
	before := r.asker.calls()
	for ; at.Before(jT0.Add(5 * time.Hour)); at = at.Add(30 * time.Second) {
		r.run(at)
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

func TestSystemicUnfitOpensTheBreaker(t *testing.T) {
	r := newRig(t)
	r.asker.reply = func(int, turns.Ask) ([]byte, error) {
		return answerJSON(t, []obj{jItem("x9", ts(1))}, nil, nil, nil), nil
	}
	postUnits(r, 6)
	r.run(jT0.Add(2 * time.Minute))
	if n := r.asker.calls(); n != 3 {
		t.Fatalf("%d calls, want the breaker open after 3 units answered unfit", n)
	}
	if w := r.job.Warnings(); len(w) == 0 || !strings.Contains(w[0], "breaker") {
		t.Fatalf("warnings %v", w)
	}
}

func TestOldReviewCannotResolveANewAsk(t *testing.T) {
	r := newRig(t)
	u := thread(ts(1))
	pr := unit.Unit{Provider: unit.GitHub, Key: "acme/widget#527"}
	msgAt := tsTime(ts(1))
	r.facts.post(jChan, ts(1), "", jOther, "<@U000AAA> please re-review PR 527", msgAt)
	r.asker.reply = func(int, turns.Ask) ([]byte, error) {
		return answerJSON(t, []obj{jItem("n1", ts(1))}, nil,
			[]obj{{"item": "n1", "target": "github:" + pr.Key, "kind": "review", "evidence_ts": "1000000000.000000"}}, nil), nil
	}
	r.run(msgAt.Add(2 * time.Minute))
	if len(r.asker.asks) != 1 {
		t.Fatalf("%d calls", len(r.asker.asks))
	}
	at := msgAt.Add(-24 * time.Hour)
	ev := link.Event{Unit: pr, Kind: link.EventReview, Actor: "email:ann@example.test", At: at, Anchor: unit.Anchor{Unit: pr, Ref: "review:old", At: at}}
	res := &links.Resolver{Links: r.db.Links(), Events: map[unit.ProviderID]links.EventReader{unit.GitHub: reviewEvents{[]link.Event{ev}}},
		Ledger: r.ledger.Ledger, Clock: r.clock}
	if n, err := res.Run(context.Background(), []unit.Unit{pr}); err != nil || n != 0 {
		t.Fatalf("an old review resolved %d items: %v", n, err)
	}
	for _, it := range r.items(u) {
		if it.State == items.Resolved {
			t.Fatalf("resolved by an old review: %+v", it)
		}
	}
}

func TestLinkEvidenceOutsideTheWindowIsDropped(t *testing.T) {
	in := Input{Unit: thread(ts(1)), Messages: []slack.Message{{TS: ts(1), User: jOther, Text: "x"}}}
	raw := answerJSON(t, []obj{jItem("n1", ts(1))}, nil,
		[]obj{{"item": "n1", "target": "github:acme/widget#527", "kind": "review", "evidence_ts": "1000000000.000000"}}, nil)
	if r, err := decode(raw, in); err != nil || len(r.Links) != 0 || len(r.Items) != 1 || r.Dropped != 1 {
		t.Fatalf("a link dated outside the window must be dropped alone: %+v %v", r, err)
	}
	ok := answerJSON(t, []obj{jItem("n1", ts(1))}, nil,
		[]obj{{"item": "n1", "target": "github:acme/widget#527", "kind": "review", "evidence_ts": ""}}, nil)
	if _, err := decode(ok, in); err != nil {
		t.Fatalf("a link without evidence_ts: %v", err)
	}
}
