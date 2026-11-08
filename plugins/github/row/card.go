package row

import (
	"strconv"
	"strings"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/situation"
	"github.com/wloophq/wloop/plugins/github/workday"
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

	ChecksFailing          = "failing"
	ChecksFailingNamed     = "failing_named"
	ChecksPending          = "pending"
	ChecksPassing          = "passing"
	ReviewApproved         = "approved"
	ReviewChangesRequested = "changes_requested"
	MergeConflicts         = "conflicts"

	// NoteStalled is the situation's note once it is stalled; its days param
	// is how many working days it has waited.
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
		sit.Notes = []src.SignalNote{StalledNote(r.Situation.Stalled.Time, r.in.Now)}
	}
	out := []src.CardSignal{sit}
	if f := r.Payload.Facts; f != nil {
		switch f.Checks.State {
		case "FAILURE", "ERROR":
			s := src.CardSignal{Key: SignalChecks, Value: ChecksFailing, Tone: ToneBad}
			if len(f.Checks.Failing) > 0 {
				s.Value, s.Params = ChecksFailingNamed, map[string]string{"names": strings.Join(f.Checks.Failing, ", ")}
			}
			out = append(out, s)
		case "PENDING":
			out = append(out, src.CardSignal{Key: SignalChecks, Value: ChecksPending, Tone: ToneWarn})
		case "SUCCESS":
			out = append(out, src.CardSignal{Key: SignalChecks, Value: ChecksPassing, Tone: ToneGood})
		}
	}
	switch {
	case r.Payload.Approved || kind == situation.KindMerge:
		out = append(out, src.CardSignal{Key: SignalReview, Value: ReviewApproved, Tone: ToneGood})
	case kind == situation.KindChanges:
		out = append(out, src.CardSignal{Key: SignalReview, Value: ReviewChangesRequested, Tone: ToneWarn})
	}
	if kind == situation.KindConflicts {
		out = append(out, src.CardSignal{Key: SignalMerge, Value: MergeConflicts, Tone: ToneBad})
	}
	return out
}

// StalledNote is the situation's note of a stall that began waiting at since, as of now.
func StalledNote(since, now time.Time) src.SignalNote {
	return src.SignalNote{Code: NoteStalled, Tone: ToneWarn, Params: map[string]string{"days": stalledDays(since, now)}}
}

// stalledDays is the whole working days from since to now, at least one: a
// stall under a day's threshold still reads as a day.
func stalledDays(since, now time.Time) string {
	return strconv.Itoa(max(1, int(workday.Between(since, now)/(24*time.Hour))))
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
