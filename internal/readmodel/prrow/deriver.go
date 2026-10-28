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
	"github.com/wloophq/wloop/internal/core/port"
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
		ref, _ := port.PROf(port.Event{Unit: u})
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

// Row derives the row of a pull request of the inventory.
func (d Deriver) Row(pr dinv.PR, evs []dinv.Event, o dinv.Override) api.Row {
	return d.Seal(d.Parts(pr, evs, o))
}

// Parts derives a pull request's row and payload before Seal: a caller adds
// the brief, the intake and the activity in between.
func (d Deriver) Parts(pr dinv.PR, evs []dinv.Event, o dinv.Override) (api.Row, Payload) {
	in := situation.Input{PR: pr, Events: evs, Override: o, Viewer: d.Viewer, MyTeams: d.Teams, Ignore: d.Ignore, Now: d.Now, Stalled: d.Stalled}
	f, hasTurn := d.Turns[pr.Ref]
	if hasTurn {
		in.Turn, in.SessionWaiting = &f.Facts, f.Waiting
		if f.Unread && o.MuteUntilStateChange {
			// An unread session cannot show that the muted state changed.
			in.SessionWaiting = o.MutedKind == string(situation.KindLoopWaiting)
		}
	}
	res := situation.Derive(in)
	if MuteLapsed(pr, o, res.Kind) {
		o.MuteUntilStateChange, o.MutedKind, o.MutedWaitingOn = false, "", ""
	}
	unmuted := res
	if res.Kind == situation.KindMuted {
		in.Override = dinv.Override{PR: pr.Ref}
		unmuted = situation.Derive(in)
	}
	ws := Workspace(pr.Workspace)
	ws.AgentStates = nonNil(ws.AgentStates)
	r := api.Row{
		Unit: pr.Ref.Unit(), Link: cmp.Or(pr.URL, pr.Ref.URL()), Label: pr.Ref.String(), Title: pr.Title, Kind: api.Kind(res.Kind),
		Summary: api.Summary{WaitsOn: res.WaitingOn, Since: res.Since, Reason: res.Reason, Stalled: res.Stalled},
	}
	p := Payload{Author: pr.Author, Repo: pr.Ref.Owner + "/" + pr.Ref.Repo, Head: termfmt.OneLine(pr.HeadBranch, 255),
		Reasons: nonNil(pr.Reasons), Workspace: ws, Override: Override(o), Viewer: d.Viewer}
	switch res.Kind {
	case situation.KindLoopWaiting, situation.KindLoopWorking, situation.KindLoopStopped:
		r.Question = f.Question
	}
	if pend, ok := d.Pending[pr.Ref.Unit()]; ok && res.Kind != situation.KindDone {
		r.Pending = pendingOf(pend)
		over := overlay.Pending{Intent: pend.Intent, Reason: pend.Reason}.Over(overlay.Situation{Kind: string(r.Kind),
			WaitingOn: r.Summary.WaitsOn, Since: r.Summary.Since, Reason: r.Summary.Reason, Stalled: r.Summary.Stalled})
		r.Kind = api.Kind(over.Kind)
		r.Summary.WaitsOn, r.Summary.Since, r.Summary.Reason, r.Summary.Stalled = over.WaitingOn, over.Since, over.Reason, over.Stalled
	}
	if res.Kind == situation.KindMuted {
		p.UnmutedKind, p.UnmutedWaitingOn, p.UnmutedReason = string(unmuted.Kind), unmuted.WaitingOn, unmuted.Reason
	}
	if o.MuteUntilStateChange || o.MutedUntil.After(d.Now) {
		p.Mute = &Mute{Until: o.MutedUntil, UntilStateChange: o.MuteUntilStateChange}
	}
	p.LastHuman = d.lastHuman(pr, evs, r.Link)
	p.Facts = d.facts(pr, r, p)
	p.Approved = d.approved(pr, evs, r, p)
	r.Views = d.views(r, p)
	unmutedRow := api.Row{Kind: api.Kind(unmuted.Kind), Summary: api.Summary{WaitsOn: unmuted.WaitingOn, Reason: unmuted.Reason, Since: unmuted.Since}}
	r.Actions = catalog.States(d.FactsOf(pr, evs, o, r, p, unmutedRow))
	r.SpecsVersion = d.SpecsVersion
	return r, p
}

// Seal fills the row's summary line and facets from p and encodes p as its
// payload.
func (d Deriver) Seal(r api.Row, p Payload) api.Row {
	r.Summary.Closed = situation.Kind(r.Kind) == situation.KindDone
	r.Summary.Line = summaryLine(r, p)
	r.Facets = d.facetValues(r, p)
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

// humanEvents are the event kinds a person's own action on GitHub leaves.
var humanEvents = []string{dinv.KindOpened, dinv.KindPush, dinv.KindReview, dinv.KindComment}

// lastHuman is the newest action of a person other than the viewer, bots
// and the ignored reviewers; nil for none.
func (d Deriver) lastHuman(pr dinv.PR, evs []dinv.Event, link string) *LastHuman {
	var last *dinv.Event
	for i, e := range evs {
		if !slices.Contains(humanEvents, e.Kind) || e.Actor == "" || strings.HasSuffix(e.Actor, "[bot]") ||
			strings.EqualFold(e.Actor, d.Viewer) || slices.ContainsFunc(d.Ignore, func(s string) bool { return strings.EqualFold(s, e.Actor) }) {
			continue
		}
		if last == nil || !e.At.Before(last.At) {
			last = &evs[i]
		}
	}
	if last == nil {
		return nil
	}
	return &LastHuman{Who: last.Actor, What: last.Kind, At: last.At, Link: link}
}

func (d Deriver) facts(pr dinv.PR, r api.Row, p Payload) *api.RowFacts {
	f := &api.RowFacts{CreatedAt: pr.CreatedAt, ChangedFiles: pr.ChangedFiles, Checks: api.CheckFacts{State: pr.ChecksState}}
	for _, c := range pr.FailingChecks {
		f.Checks.Failing = append(f.Checks.Failing, c.Name)
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

func (d Deriver) approved(pr dinv.PR, evs []dinv.Event, r api.Row, p Payload) bool {
	if pr.State != "OPEN" || p.Workspace.ID == "" || p.Workspace.IsMain {
		return false
	}
	switch situation.Kind(r.Kind) {
	case situation.KindRoundPending, situation.KindLoopWorking, situation.KindLoopWaiting, situation.KindLoopStopped, situation.KindHeldByCap, situation.KindBlocked, situation.KindMuted:
		return false
	}
	if p.Workspace.IsReview {
		return d.approvedHead(pr, evs)
	}
	return d.Viewer != "" && strings.EqualFold(pr.Author, d.Viewer) && situation.Kind(r.Kind) == situation.KindMerge
}

// approvedHead tells whether my newest review approved the current head.
func (d Deriver) approvedHead(pr dinv.PR, evs []dinv.Event) bool {
	var last dinv.Event
	for _, e := range evs {
		if e.Kind == dinv.KindReview && strings.EqualFold(e.Actor, d.Viewer) && !e.At.Before(last.At) {
			last = e
		}
	}
	return d.Viewer != "" && pr.HeadOid != "" && last.Data == "APPROVED:"+pr.HeadOid
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// views puts a pull request outside the scope (no reasons) only where the
// loop or an adoption put it: loop, mine, and needs-me for a review
// workspace's question or an author workspace whose agent waits for me.
func (d Deriver) views(r api.Row, p Payload) []string {
	out := []string{}
	kind := situation.Kind(r.Kind)
	if needsMe(kind, r.Summary.WaitsOn, p) && kind != situation.KindMuted {
		out = append(out, api.ViewNeedsMe)
	}
	if r.Summary.Stalled && len(p.Reasons) > 0 {
		out = append(out, api.ViewStalled)
	}
	if (mineAuthor(p) || p.Override.Adopted) && kind != situation.KindDone {
		out = append(out, api.ViewMine)
	}
	if p.Workspace.IsReview {
		out = append(out, api.ViewLoop)
	}
	if p.Approved {
		out = append(out, api.ViewApproved)
	}
	if kind == situation.KindDone && p.Workspace.ID != "" {
		out = append(out, api.ViewDone)
	}
	return out
}

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
