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

// The keys of a card's signals and the codes of their values and notes,
// which the plugin's ui.toml [signals] words. The situation signal's value
// is the situation's kind.
const (
	SignalSituation = "situation"
	SignalChecks    = "checks"
	SignalReview    = "review"
	SignalMerge     = "merge"

	ChecksFailing      = "failing"
	ChecksFailingNamed = "failing_named"
	ChecksPending      = "pending"
	ChecksPassing      = "passing"
	ReviewApproved     = "approved"
	MergeConflicts     = "conflicts"

	// NoteStalled is the situation's note once it is stalled, counting from
	// when the situation began.
	NoteStalled = "stalled"
)

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
	sit := src.CardSignal{Key: SignalSituation, Value: string(kind), Tone: ToneNeutral}
	if kind == situation.KindDone {
		return []src.CardSignal{sit}
	}
	if r.Stalled() {
		sit.Detail = &src.SignalDetail{Notes: []src.SignalNote{{Code: NoteStalled, Tone: ToneWarn, Since: r.Situation.Since}}}
	}
	out := []src.CardSignal{sit}
	if f := r.Payload.Facts; f != nil {
		switch f.Checks.State {
		case "FAILURE", "ERROR":
			s := src.CardSignal{Key: SignalChecks, Value: ChecksFailing, Tone: ToneBad}
			if len(f.Checks.Failing) > 0 {
				s.Value, s.Detail = ChecksFailingNamed, &src.SignalDetail{Params: map[string]string{"names": strings.Join(f.Checks.Failing, ", ")}}
			}
			out = append(out, s)
		case "PENDING":
			out = append(out, src.CardSignal{Key: SignalChecks, Value: ChecksPending, Tone: ToneWarn})
		case "SUCCESS":
			out = append(out, src.CardSignal{Key: SignalChecks, Value: ChecksPassing, Tone: ToneGood})
		}
	}
	if r.Payload.Approved || kind == situation.KindMerge {
		out = append(out, src.CardSignal{Key: SignalReview, Value: ReviewApproved, Tone: ToneGood})
	}
	if kind == situation.KindConflicts {
		out = append(out, src.CardSignal{Key: SignalMerge, Value: MergeConflicts, Tone: ToneBad})
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
