// Package prrow derives the row of a pull request from what the store, the
// gate and the turns say of it: the github provider of the read model.
package prrow

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/turns"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/readmodel/overlay"
	"github.com/wloophq/wloop/internal/situation"
	"github.com/wloophq/wloop/internal/termfmt"
)

// Deriver derives rows at one instant from what every row shares.
type Deriver struct {
	Viewer  string
	Teams   []string
	Ignore  []string
	Now     time.Time
	Stalled config.Stalled
	Pending map[unit.Unit]round.Pending
	Turns   map[github.Ref]TurnFact
	// What the actions read besides the row.
	Loaded          *intentload.Loaded
	Specs           []api.ActionSpec // nil: catalog.Specs(Loaded)
	Cfg             config.Config
	GateOn, BriefOn bool
	Forceable       func(unit.Unit) string
	Intakes         map[pr.Ref][]intake.Intake
	SpecsVersion    string
	// Asked is the action a request asks for, whose inputs an open intake
	// joins on; zero for a row served to a client.
	Asked catalog.Asked
}

// TurnFact is what a row reads of its pull request's turn book.
type TurnFact struct {
	Facts overlay.Turn
	// Session is the handle of a started turn's session, "" otherwise.
	Session  string
	Waiting  bool
	Unread   bool // the session of a started turn could not be read
	Question *api.QuestionView
	// Unstarted are the ids of the turns that ended before their agent
	// started them.
	Unstarted map[string]bool
}

// Unstarted are the ids of b's turns that ended before their agent started
// them, which posted nothing.
func Unstarted(b turn.Book) map[string]bool { return overlay.Unstarted(b) }

// IgnoreOf is who a row never waits on and a nudge never mentions.
func IgnoreOf(cfg config.Config) []string {
	return slices.Concat(cfg.GitHub.AutomatedReviewers, cfg.GitHub.AddressSkipReviewers)
}

// TurnFacts reads every book's driving turn, with the session of a started
// one; whether that session waits is resolved by Waiting.
func TurnFacts(books map[unit.Unit]turn.Book, reg *dintent.Registry) map[github.Ref]TurnFact {
	out := make(map[github.Ref]TurnFact, len(books))
	for u, b := range books {
		run, ok := overlay.RunOf(b, reg)
		if !ok {
			continue
		}
		ref, _ := pr.RefOf(u)
		out[ref] = TurnFact{Facts: run.Turn, Session: run.Session, Question: run.Question, Unstarted: run.Unstarted}
	}
	return out
}

// Started tells whether any fact is a started turn with a session.
func Started(facts map[github.Ref]TurnFact) bool {
	for _, f := range facts {
		if f.Session != "" {
			return true
		}
	}
	return false
}

// Waiting resolves each started turn against Orca's agents: session reads
// a session by handle. With agentsOK false every started turn is unread.
func Waiting(facts map[github.Ref]TurnFact, agents []turns.AgentObservation, agentsOK bool, session func(handle string) (turn.Session, bool, error), logf func(string, ...any)) {
	for ref, f := range facts {
		if f.Session == "" {
			continue
		}
		if !agentsOK {
			f.Unread = true
			facts[ref] = f
			continue
		}
		sess, ok, err := session(f.Session)
		if err != nil {
			logf("rows: reading session %s: %v", f.Session, err)
			f.Unread = true
			facts[ref] = f
			continue
		}
		if ok && overlay.SessionWaiting(sess, agents) {
			f.Waiting = true
			facts[ref] = f
		}
	}
}

// MuteLapsed tells whether o's "mute until state change" lapsed: the pull
// request is open and its situation, kind, is no longer the muted one. The
// row then shows no mute, and the read model's keeper clears the stored one.
func MuteLapsed(pr dinv.PR, o dinv.Override, kind situation.Kind) bool {
	return o.MuteUntilStateChange && kind != situation.KindMuted && pr.State == dgh.StateOpen
}

// Row derives the row of a pull request the plugin reported.
func (d Deriver) Row(rep Report, evs []dinv.Event, o dinv.Override) api.Row {
	return d.Seal(d.Parts(rep, evs, o))
}

// Parts derives a pull request's row and payload before Seal: a caller adds
// the brief, the intake and the activity in between. evs are the host's
// events of the pull request and o its overrides there.
func (d Deriver) Parts(rep Report, evs []dinv.Event, o dinv.Override) (api.Row, Payload) {
	pr := rep.PR
	var turn *TurnFact
	f, hasTurn := d.Turns[pr.Ref]
	if hasTurn {
		turn = &f
	}
	res, unmuted := SituationOf(rep, turn, o, evs, d.Now)
	if MuteLapsed(pr, o, situation.Kind(res.Kind)) {
		o.MuteUntilStateChange, o.MutedKind, o.MutedWaitingOn = false, "", ""
	}
	o.Adopted = o.Adopted || rep.Payload.Override.Adopted
	o.Tracked = o.Tracked || rep.Payload.Override.Tracked
	r := api.Row{
		Unit: pr.Ref.Unit(), Link: cmp.Or(pr.URL, pr.Ref.URL()), Label: pr.Ref.String(), Title: pr.Title, Kind: api.Kind(res.Kind),
		Summary: api.Summary{WaitsOn: res.WaitingOn, Since: res.Since, Reason: res.Reason, Stalled: res.Stalled},
	}
	p := rep.Payload
	p.Reasons, p.Workspace.AgentStates = nonNil(p.Reasons), nonNil(p.Workspace.AgentStates)
	p.Override, p.Viewer = Override(o), d.Viewer
	switch situation.Kind(res.Kind) {
	case situation.KindLoopWaiting, situation.KindLoopStopped, situation.KindLoopWorking:
		r.Question = f.Question
	}
	if pend, ok := d.Pending[pr.Ref.Unit()]; ok && res.Kind != overlay.KindDone {
		r.Pending = pendingOf(pend)
		over := overlay.Pending{Intent: pend.Intent, Reason: pend.Reason}.Over(overlay.Situation{Kind: string(r.Kind),
			WaitingOn: r.Summary.WaitsOn, Since: r.Summary.Since, Reason: r.Summary.Reason, Stalled: r.Summary.Stalled})
		r.Kind = api.Kind(over.Kind)
		r.Summary.WaitsOn, r.Summary.Since, r.Summary.Reason, r.Summary.Stalled = over.WaitingOn, over.Since, over.Reason, over.Stalled
	}
	if res.Kind == overlay.KindMuted {
		p.UnmutedKind, p.UnmutedWaitingOn, p.UnmutedReason = unmuted.Kind, unmuted.WaitingOn, unmuted.Reason
	}
	if o.MuteUntilStateChange || o.MutedUntil.After(d.Now) {
		p.Mute = &Mute{Until: o.MutedUntil, UntilStateChange: o.MuteUntilStateChange}
	}
	p.Facts = d.facts(rep.Payload.Facts, r, p)
	overlaid := string(r.Kind) != rep.Situation.Kind
	p.Approved = rep.Payload.Approved && !overlaid
	r.Views = d.views(rep.Views, overlaid, r, p)
	unmutedRow := api.Row{Kind: api.Kind(unmuted.Kind), Summary: api.Summary{WaitsOn: unmuted.WaitingOn, Reason: unmuted.Reason, Since: unmuted.Since}}
	r.Actions = catalog.States(d.FactsOf(pr, evs, o, r, p, unmutedRow))
	r.SpecsVersion = d.SpecsVersion
	r.Facets = rep.Facets
	return r, p
}

// Seal fills the row's summary line and facets from p and encodes p as its
// payload.
func (d Deriver) Seal(r api.Row, p Payload) api.Row {
	r.Summary.Closed = situation.Kind(r.Kind) == situation.KindDone
	r.Summary.Line = summaryLine(r, p)
	r.Facets = d.facetValues(r, p, r.Facets)
	raw, err := json.Marshal(p)
	if err != nil {
		panic(fmt.Sprintf("prrow: encoding a payload: %v", err))
	}
	r.Payload = raw
	return r
}

// summaryLine is the short status the menu bar and the terminal list show.
func summaryLine(r api.Row, p Payload) string {
	if i := p.Intake; i != nil && i.Waiting() {
		return overlay.Briefing(*i)
	}
	switch k := situation.Kind(r.Kind); k {
	case situation.KindRoundPending, situation.KindHeldByCap, situation.KindBlocked,
		situation.KindLoopWaiting, situation.KindLoopStopped, situation.KindLoopWorking:
		return k.Label()
	}
	return r.Summary.Reason
}

// facts are the reported facts of the row with the instants ahead it waits
// for.
func (d Deriver) facts(reported *api.RowFacts, r api.Row, p Payload) *api.RowFacts {
	f := &api.RowFacts{}
	if reported != nil {
		*f = *reported
		f.Checks.Failing = slices.Clone(reported.Checks.Failing)
	}
	if r.Pending != nil {
		f.Next = d.upcoming(f.Next, r.Pending.Deadline, api.UpcomingGate)
	}
	f.Next = d.upcoming(f.Next, p.Override.MutedUntil, api.UpcomingUnmute)
	return f
}

// upcoming adds at to next when it is still ahead, keeping next sorted.
func (d Deriver) upcoming(next []api.Upcoming, at time.Time, what string) []api.Upcoming {
	if !at.After(d.Now) {
		return next
	}
	next = append(next, api.Upcoming{At: at, What: what})
	slices.SortStableFunc(next, func(a, b api.Upcoming) int { return a.At.Compare(b.At) })
	return next
}

// AddActivity puts the pull request's turns, usage and brief retry in the
// payload's facts.
func (d Deriver) AddActivity(p *Payload, a turn.Activity) {
	f := p.Facts
	if f == nil {
		return
	}
	f.Turns = api.TurnFacts{Count: a.Turns, Open: a.OpenTurns, LastIntent: a.LastIntent, Tokens: a.Tokens, CostUSD: a.CostUSD}
	f.Next = d.upcoming(f.Next, a.BriefRetryAt, api.UpcomingBriefRetry)
	p.briefRetryAt = a.BriefRetryAt
}

// BriefSummary is the row's view of a brief.
func BriefSummary(b brief.Summary) *api.BriefSummary {
	return &api.BriefSummary{Kind: b.Kind, Size: b.Size, Risk: b.Risk, Goal: termfmt.OneLine(b.Goal, 300), OpenQuestions: b.OpenQuestions, Tags: b.Tags, Extra: b.Extra}
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// views are the views the plugin put the row in, laid over with the
// host's state: an overlay that set the row's situation decides needs-me
// and leaves it out of approved, the stalled view follows the host's clock,
// and a pull request the user took over is the user's.
func (d Deriver) views(reported []string, overlaid bool, r api.Row, p Payload) []string {
	drop := []string{api.ViewStalled}
	if overlaid {
		drop = append(drop, api.ViewNeedsMe, api.ViewApproved)
	}
	out := slices.DeleteFunc(slices.Clone(reported), func(v string) bool { return slices.Contains(drop, v) })
	if out == nil {
		out = []string{}
	}
	kind := situation.Kind(r.Kind)
	add := func(v string, on bool) {
		if on && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	add(api.ViewNeedsMe, overlaid && needsMe(kind, r.Summary.WaitsOn, p) && kind != situation.KindMuted)
	add(api.ViewStalled, r.Summary.Stalled && len(p.Reasons) > 0)
	add(api.ViewMine, p.Override.Adopted && kind != situation.KindDone)
	slices.SortStableFunc(out, func(a, b string) int { return slices.Index(viewOrder, a) - slices.Index(viewOrder, b) })
	return out
}

// viewOrder is the order of a row's views.
var viewOrder = []string{api.ViewNeedsMe, api.ViewStalled, api.ViewMine, api.ViewLoop, api.ViewApproved, api.ViewDone}

func mineAuthor(p Payload) bool { return p.Viewer != "" && strings.EqualFold(p.Author, p.Viewer) }

// needsMe is the needs-me predicate of a situation: it waits on the user,
// asks, holds or is the user's own to fix, in scope or in the loop.
func needsMe(kind situation.Kind, waitingOn string, p Payload) bool {
	asks := kind == situation.KindLoopWaiting || kind == situation.KindLoopStopped
	needs := (waitingOn == "me" && kind != situation.KindLoopWorking) || asks ||
		kind == situation.KindHeldByCap || kind == situation.KindRoundPending ||
		(mineAuthor(p) && (kind == situation.KindChecks || kind == situation.KindChanges || kind == situation.KindConflicts))
	return needs && (len(p.Reasons) > 0 || p.Workspace.IsReview || asks)
}
