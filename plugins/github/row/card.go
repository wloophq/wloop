package row

import (
	"strings"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/situation"
)

// The tones of a card's signals.
const (
	ToneNeutral = "neutral"
	ToneGood    = "good"
	ToneWarn    = "warn"
	ToneBad     = "bad"
)

// SignalSituation is the key of a card's first signal, whose value is the
// situation's kind; the host words it.
const SignalSituation = "situation"

// Card is the pull request's card from its source row.
func (r Row) Card() *src.Card {
	p, kind := r.Payload, r.Kind()
	c := &src.Card{
		Label:       r.PR.Ref.String(),
		Title:       r.PR.Title,
		State:       "open",
		Signals:     r.signals(),
		Why:         r.Situation.Reason,
		WaitsOn:     People(r.Situation.WaitingOn, p),
		Since:       r.Situation.Since,
		Link:        r.Link(),
		TurnsLink:   "#unit=" + pr.Provider + "/" + r.PR.Ref.String(),
		StartedByMe: r.Mine(),
		Cares:       r.InScope(),
	}
	switch {
	case kind == situation.KindDone && r.Situation.Reason == "merged":
		c.State, c.Outcome, c.ClosurePosted = "merged", outcomeDone, true
	case kind == situation.KindDone:
		c.State, c.Outcome, c.ClosurePosted = "closed", outcomeMoot, true
	case kind == situation.KindDraft:
		c.State = "draft"
	}
	if h := p.LastHuman; h != nil {
		c.LastHuman = &src.Human{Who: Person(h.Who), What: h.What, At: h.At, Link: h.Link}
	}
	return c
}

// The outcomes of a finished pull request's items.
const (
	outcomeDone = "done"
	outcomeMoot = "moot"
)

func (r Row) signals() []src.CardSignal {
	kind := r.Kind()
	out := []src.CardSignal{{Key: SignalSituation, Value: string(kind), Tone: ToneNeutral}}
	if kind == situation.KindDone {
		return out
	}
	if f := r.Payload.Facts; f != nil {
		switch f.Checks.State {
		case "FAILURE", "ERROR":
			v := "failing"
			if len(f.Checks.Failing) > 0 {
				v += ": " + strings.Join(f.Checks.Failing, ", ")
			}
			out = append(out, src.CardSignal{Key: "checks", Value: v, Tone: ToneBad})
		case "PENDING":
			out = append(out, src.CardSignal{Key: "checks", Value: "pending", Tone: ToneWarn})
		case "SUCCESS":
			out = append(out, src.CardSignal{Key: "checks", Value: "passing", Tone: ToneGood})
		}
	}
	switch {
	case r.Payload.Approved || kind == situation.KindMerge:
		out = append(out, src.CardSignal{Key: "review", Value: "approved", Tone: ToneGood})
	case kind == situation.KindChanges:
		out = append(out, src.CardSignal{Key: "review", Value: "changes requested", Tone: ToneWarn})
	}
	if kind == situation.KindConflicts {
		out = append(out, src.CardSignal{Key: "merge", Value: "conflicts", Tone: ToneBad})
	}
	if r.Stalled() {
		out = append(out, src.CardSignal{Key: "stalled", Value: "stalled", Tone: ToneWarn})
	}
	return out
}

// Person is a GitHub login, or the group of a "team:<org>/<slug>" request.
func Person(login string) src.Person {
	if team, ok := strings.CutPrefix(login, "team:"); ok {
		return src.Person{Key: "group:" + team, Name: team}
	}
	return src.Person{Key: pr.Provider + ":" + login, Name: login}
}

// People reads a situation's waiting-on: "me", "author", "nobody" or
// logins.
func People(waitingOn string, p Payload) []src.Person {
	switch waitingOn {
	case "", "nobody":
		return nil
	case "me":
		return []src.Person{Person(p.Viewer)}
	case "author":
		return []src.Person{Person(p.Author)}
	}
	var out []src.Person
	for l := range strings.SplitSeq(waitingOn, ", ") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, Person(l))
		}
	}
	return out
}
