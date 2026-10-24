package prrow

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/situation"
)

var policyNow = time.Date(2026, 10, 14, 15, 0, 0, 0, time.UTC)

func policyRef(n int) pr.Ref { return pr.Ref{Owner: "acme", Repo: "api", Number: n} }

func pendingRow(n int) api.Row {
	return api.Row{Unit: policyRef(n).Unit(), Title: "PR " + strconv.Itoa(n), Kind: api.Kind(situation.KindRoundPending),
		Pending: &api.PendingRound{Intent: "follow-up", Reason: "ana replied", Since: policyNow, Deadline: policyNow.Add(60 * time.Second)}, Summary: api.Summary{WaitsOn: "me", Reason: "follow-up waiting for your answer: ana replied", Since: policyNow.Add(-20 * time.Minute)}}
}

// offering is r with the actions a gated row of the inventory offers.
func offering(d Deriver, r api.Row) api.Row {
	r.Actions = catalog.States(catalog.Facts{Ref: refOf(r), Row: r, Tracked: true, Open: situation.Kind(r.Kind) != situation.KindDone, HeldByCap: situation.Kind(r.Kind) == situation.KindHeldByCap, GateOn: true, Intents: d.Loaded})
	return r
}

func TestAttentionKeepsTodaysPolicy(t *testing.T) {
	d := Deriver{Now: policyNow, Loaded: bundled(t)}

	n := d.Attention(offering(d, pendingRow(1)), nil)
	if n == nil || n.Category != "ROUND" || n.Title != "Round pending" || n.Subtitle != "acme/api #1" || n.Image != "round" ||
		!strings.Contains(n.Body, "ana replied") || !strings.Contains(n.Body, "waiting 20 min") ||
		!slices.Equal(actionsOf(n), []string{"run_now Run now", "skip Skip", "guide Guide [text]", "pause Pause", "open Open"}) {
		t.Fatalf("pending %+v", n)
	}
	again := pendingRow(1)
	again.Pending.Since = policyNow.Add(time.Minute)
	again.Pending.Deadline = policyNow.Add(90 * time.Second)
	if m := d.Attention(again, nil); m.ID == n.ID || m.Title != "Round pending" {
		t.Fatalf("a proposal again: %+v after %+v", m, n)
	}

	held := api.Row{Unit: policyRef(4).Unit(), Kind: api.Kind(situation.KindHeldByCap), Title: "Cap", Summary: api.Summary{WaitsOn: "me", Reason: "automatic round held: daily cap", Since: policyNow}}
	h := d.Attention(offering(d, held), nil)
	today := policyNow.Local().Format(time.DateOnly)
	if h == nil || h.ID != "HELD|acme|api|4|"+today || h.Category != "HELD" || h.Title != "Round held" || h.Subtitle != "acme/api #4" || h.Image != "held" ||
		h.Body != "automatic round held: daily cap · Cap" || !slices.Equal(actionsOf(h), []string{"guide Guide [text]", "pause Pause", "open Open"}) {
		t.Fatalf("held %+v", h)
	}

	blocked := api.Row{Unit: policyRef(5).Unit(), Kind: api.Kind(situation.KindBlocked), Summary: api.Summary{WaitsOn: "me", Reason: "round blocked: x", Since: policyNow}}
	b := d.Attention(offering(d, blocked), nil)
	if b == nil || b.Category != "BLOCKED" || b.Title != "Round blocked" || b.Subtitle != "acme/api #5" || b.Image != "blocked" ||
		!slices.Equal(actionsOf(b), []string{"guide Guide [text]", "pause Pause", "open Open"}) {
		t.Fatalf("blocked %+v", b)
	}
	blocked.Summary.Since = policyNow.Add(time.Hour)
	if later := d.Attention(blocked, nil); later.ID != b.ID {
		t.Fatalf("a blocked round changed its id with its since: %s then %s", b.ID, later.ID)
	}
	blocked.Summary.Reason = "round blocked: y"
	if other := d.Attention(blocked, nil); other.ID == b.ID {
		t.Fatal("another reason kept the id")
	}

	waits := api.Row{Unit: policyRef(6).Unit(), Title: "Docs", Kind: api.Kind(situation.KindLoopWaiting), Summary: api.Summary{WaitsOn: "me", Reason: "the agent asked you something", Since: policyNow.Add(-10 * time.Minute)}}
	w := d.Attention(offering(d, waits), nil)
	if w == nil || w.Title != "Agent asks" || w.Image != "ask" || !strings.Contains(w.Body, "Docs") || !slices.Equal(actionsOf(w), []string{"guide Guide [text]", "pause Pause", "open Open"}) {
		t.Fatalf("waiting agent %+v", w)
	}
	if d.Attention(waits, nil).ID != w.ID {
		t.Fatal("the same episode changed its id")
	}
	waits.Summary.Since = policyNow.Add(-time.Minute)
	if d.Attention(waits, nil).ID == w.ID {
		t.Fatal("a new since kept the episode")
	}

	for _, k := range []situation.Kind{situation.KindReview, situation.KindLoopWorking, situation.KindChecks, situation.KindMuted} {
		if a := d.Attention(api.Row{Unit: policyRef(7).Unit(), Kind: api.Kind(k), Summary: api.Summary{WaitsOn: "me"}}, nil); a != nil {
			t.Errorf("%s got %+v", k, a)
		}
	}
}
