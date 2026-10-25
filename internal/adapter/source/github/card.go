package ghsource

import (
	"context"
	"fmt"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
	"github.com/wloophq/wloop/internal/situation"
)

const (
	toneNeutral card.Tone = "neutral"
	toneGood    card.Tone = "good"
	toneWarn    card.Tone = "warn"
	toneBad     card.Tone = "bad"
)

// Card is a pull request's card, read from its row alone.
func Card(_ context.Context, r api.Row) (card.Card, error) {
	p, ok := prrow.PayloadOf(r)
	if !ok {
		return card.Card{}, fmt.Errorf("github card: %s has no pull request payload", r.Unit.Label())
	}
	d := situationOf(r, p)
	c := card.Card{
		Unit:        r.Unit,
		Label:       r.Label,
		Title:       r.Title,
		State:       "open",
		Signals:     signals(d),
		Why:         r.Summary.Reason,
		WaitsOn:     people(r.Summary.WaitsOn, p),
		Since:       r.Summary.Since,
		Link:        r.Link,
		TurnsLink:   "#unit=" + string(r.Unit.Provider) + "/" + r.Unit.Key,
		StartedByMe: d.mine,
		Cares:       prrow.InScope(r),
	}
	switch {
	case d.kind == situation.KindDone && r.Summary.Reason == "merged":
		c.State, c.Outcome, c.ClosurePosted = "merged", items.Done, true
	case d.kind == situation.KindDone:
		c.State, c.Outcome, c.ClosurePosted = "closed", items.Moot, true
	case d.kind == situation.KindDraft:
		c.State = "draft"
	}
	if h := p.LastHuman; h != nil {
		c.LastHuman = &card.Human{Who: person(h.Who), What: h.What, At: h.At, Link: h.Link}
	}
	if m := p.Mute; m != nil {
		c.Mute = &card.Mute{Until: m.Until, UntilStateChange: m.UntilStateChange}
	}
	return c, nil
}

func signals(d derivation) []card.Signal {
	out := []card.Signal{{Key: "situation", Value: situation.Kind(d.row.Kind).Label(), Tone: toneNeutral}}
	if d.kind == situation.KindDone {
		return out
	}
	if f := d.p.Facts; f != nil {
		switch f.Checks.State {
		case "FAILURE", "ERROR":
			v := "failing"
			if len(f.Checks.Failing) > 0 {
				v += ": " + strings.Join(f.Checks.Failing, ", ")
			}
			out = append(out, card.Signal{Key: "checks", Value: v, Tone: toneBad})
		case "PENDING":
			out = append(out, card.Signal{Key: "checks", Value: "pending", Tone: toneWarn})
		case "SUCCESS":
			out = append(out, card.Signal{Key: "checks", Value: "passing", Tone: toneGood})
		}
	}
	switch {
	case d.p.Approved || d.kind == situation.KindMerge:
		out = append(out, card.Signal{Key: "review", Value: "approved", Tone: toneGood})
	case d.kind == situation.KindChanges:
		out = append(out, card.Signal{Key: "review", Value: "changes requested", Tone: toneWarn})
	}
	if d.kind == situation.KindConflicts {
		out = append(out, card.Signal{Key: "merge", Value: "conflicts", Tone: toneBad})
	}
	if d.row.Summary.Stalled {
		out = append(out, card.Signal{Key: "stalled", Value: "stalled", Tone: toneWarn})
	}
	return out
}
