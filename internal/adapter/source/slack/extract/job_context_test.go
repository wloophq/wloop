package extract

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/slack"
)

func (f *fakeFacts) dmBefore(_ context.Context, conv, before string, n int) ([]slack.Message, error) {
	var out []slack.Message
	for _, m := range f.msgs[conv] {
		if slack.CompareTS(m.Anchor(), before) < 0 {
			out = append(out, m)
		}
	}
	return out[max(len(out)-n, 0):], nil
}

func dmWithEarlierTopic(r *rig) {
	r.facts.post(jDM, ts(0), "", jOther, "hello", jT0)
	r.facts.post(jDM, ts(1), "", jOther, "the lunch place is booked", jT0)
	r.facts.post(jDM, ts(2), "", jViewer, "thanks", jT0)
	r.facts.post(jDM, ts(3), "", jOther, "lunch is at noon on friday", jT0)
	r.facts.post(jDM, ts(5), "", jOther, "can you review the deck before friday?", jT0)
	if _, err := r.segs.Apply(context.Background(), jTeam, jDM, []slack.Span{{FromTS: ts(0), ToTS: ts(2)}, {FromTS: ts(3), ToTS: ts(3)}, {FromTS: ts(5)}}); err != nil {
		r.t.Fatal(err)
	}
}

func TestDMWindowShowsEarlierMessagesAsContext(t *testing.T) {
	r := newRig(t)
	r.job.DMBefore = r.facts.dmBefore
	r.cfg.Slack.Extraction.ContextMessages = 2
	dmWithEarlierTopic(r)
	r.run(jT0.Add(2 * time.Minute))
	if r.asker.calls() != 1 {
		t.Fatalf("%d calls", r.asker.calls())
	}
	p := r.asker.asks[0].Prompt
	if !strings.Contains(p, ContextHeading) || !strings.Contains(p, "[c1 ts="+ts(1)) || !strings.Contains(p, "[c2 ts="+ts(2)) || strings.Contains(p, "ts="+ts(0)+" ") {
		t.Fatalf("the prompt lacks the 2 messages before the window as context:\n%s", p)
	}
	if !strings.Contains(p, "[m1 ts="+ts(3)) {
		t.Fatalf("the window's numbering moved:\n%s", p)
	}

	r.cfg.Slack.Extraction.ContextMessages = -1
	r.facts.post(jDM, ts(6), "", jOther, "any news?", jT0.Add(5*time.Minute))
	r.run(jT0.Add(8 * time.Minute))
	if p := r.asker.asks[len(r.asker.asks)-1].Prompt; strings.Contains(p, ContextHeading) {
		t.Fatalf("context_messages -1 still shows context:\n%s", p)
	}
}

func TestLongUnitEscalatesToFrontierWithinTheCaps(t *testing.T) {
	r := newRig(t)
	r.cfg.Slack.Extraction.EscalateOverChars = 400
	r.cfg.Slack.Extraction.PerCallUSD = 0.07
	r.facts.post(jChan, ts(1), "", jOther, "short ask", jT0)
	r.facts.post(jChan, ts(2), "", jOther, strings.Repeat("a long ask with many details ", 30), jT0)
	r.run(jT0.Add(2 * time.Minute))
	tiers := map[turns.Tier]int{}
	for _, a := range r.asker.asks {
		tiers[a.Tier]++
		if a.MaxCostUSD != 0.07 {
			t.Errorf("an escalated call lost the per-call cap: %+v", a.MaxCostUSD)
		}
	}
	if tiers[turns.TierFast] != 1 || tiers[turns.TierFrontier] != 1 {
		t.Fatalf("tiers %v, want one fast and one frontier", tiers)
	}
	if !strings.Contains(strings.Join(r.logs, "\n"), "escalated to frontier") {
		t.Errorf("the escalation is not logged: %v", r.logs)
	}
}

func TestEscalationOverTheCallCapFallsBackToTheBaseTier(t *testing.T) {
	r := newRig(t)
	r.cfg.Slack.Extraction.EscalateOverChars = 1
	r.asker.reply = func(_ int, q turns.Ask) ([]byte, error) {
		if q.Tier == turns.TierFrontier {
			return nil, turns.ErrOverBudget
		}
		return answerJSON(t, nil, nil, nil, nil), nil
	}
	postUnits(r, 6)
	summary, skipped := r.run(jT0.Add(2 * time.Minute))
	if !strings.HasPrefix(summary, "extracted 6 of 6") || skipped != "" {
		t.Fatalf("summary %q, skipped %q; want every unit extracted on the fast tier", summary, skipped)
	}
	tiers := map[turns.Tier]int{}
	for _, a := range r.asker.asks {
		tiers[a.Tier]++
	}
	if tiers[turns.TierFrontier] != 6 || tiers[turns.TierFast] != 6 {
		t.Fatalf("tiers %v, want each unit escalated once and then fast", tiers)
	}
	if w := r.job.Warnings(); len(w) != 0 {
		t.Fatalf("an escalated call over the cap fed the breaker: %v", w)
	}
}

func TestEscalationFallbackRespectsTheDailyCap(t *testing.T) {
	r := newRig(t)
	r.cfg.Slack.Extraction.EscalateOverChars = 1
	r.cfg.Slack.Extraction.PerDay = 1
	r.asker.reply = func(_ int, q turns.Ask) ([]byte, error) {
		if q.Tier == turns.TierFrontier {
			return nil, turns.ErrOverBudget
		}
		return answerJSON(t, nil, nil, nil, nil), nil
	}
	postUnits(r, 1)
	r.run(jT0.Add(2 * time.Minute))
	if n := r.asker.calls(); n != 1 {
		t.Fatalf("%d calls, want the fallback held by the daily call cap", n)
	}
	if w := r.job.Warnings(); len(w) != 0 {
		t.Fatalf("a held fallback fed the breaker: %v", w)
	}
	summary, _ := r.run(jT0.Add(25 * time.Hour))
	if !strings.HasPrefix(summary, "extracted 1 of 1") || r.asker.asks[1].Tier != turns.TierFast {
		t.Fatalf("summary %q, asks %+v; want the unit extracted the next day on the fast tier only", summary, r.asker.asks)
	}
}
