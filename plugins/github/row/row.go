// Package row derives what the plugin reports of a pull request from the
// pull request and its events alone: its situation, the source part of its
// row's payload, its views and facets, its card, its items, its link
// signals and its workflow facts. The host lays its own state over them.
package row

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/situation"
	"github.com/wloophq/wloop/plugins/github/store"
)

// Input is what one pull request's report derives from.
type Input struct {
	PR       pr.PR
	Events   []pr.Event
	Override pr.Override
	// Detail is the pull request's full read; nil before one.
	Detail *store.Detail
	Viewer string
	Teams  []string
	// Ignore are who a row never waits on: automated reviewers and the
	// reviewers an address round skips.
	Ignore  []string
	Stalled situation.Thresholds
	// AddressedAt is when the pull request's driving turn, an address turn,
	// reached its state; zero without one.
	AddressedAt time.Time
	// MaxDetailAge is the oldest detail the plugin calls fresh.
	MaxDetailAge time.Duration
	// Loop is the loop's name, as items it asks of the user name it.
	Loop string
	Now  time.Time
}

// Row is a pull request's source row: its situation and the source part of
// its payload, views and facets.
type Row struct {
	PR        pr.PR
	Situation situation.Result
	Payload   Payload
	Views     []string
	Facets    map[string][]string
	in        Input
}

// The views of plugin rows the host serves.
const (
	ViewNeedsMe  = "needs-me"
	ViewStalled  = "stalled"
	ViewMine     = "mine"
	ViewLoop     = "loop"
	ViewApproved = "approved"
	ViewDone     = "done"
)

// Derive is in's source row at in.Now.
func Derive(in Input) Row {
	p := in.PR
	res := situation.Derive(situation.Input{PR: p, Events: in.Events, Viewer: in.Viewer, MyTeams: in.Teams, Ignore: in.Ignore,
		Now: in.Now, Stalled: in.Stalled, AddressedAt: in.AddressedAt})
	ws := WorkspaceOf(p.Workspace)
	pl := Payload{Author: p.Author, Repo: p.Ref.Owner + "/" + p.Ref.Repo, Head: pr.OneLine(p.HeadBranch, 255), Reasons: nonNil(p.Reasons),
		Workspace: ws, Override: Override{Adopted: in.Override.Adopted, Tracked: in.Override.Tracked}, Viewer: in.Viewer}
	pl.LastHuman = lastHuman(in, cmp.Or(p.URL, p.Ref.URL()))
	pl.Facts = &Facts{CreatedAt: p.CreatedAt, ChangedFiles: p.ChangedFiles, Checks: Checks{State: p.ChecksState}}
	for _, c := range p.FailingChecks {
		pl.Facts.Checks.Failing = append(pl.Facts.Checks.Failing, c.Name)
	}
	r := Row{PR: p, Situation: res, Payload: pl, in: in}
	r.Payload.Approved = r.approved()
	r.Views = r.views()
	r.Facets = r.facets()
	return r
}

// Events are the host's events of the pull request the row derives from.
func (r Row) Events() []pr.Event { return r.in.Events }

// Kind is the row's situation kind.
func (r Row) Kind() situation.Kind { return r.Situation.Kind }

// Stalled says whether the row's situation is stalled at the input's now.
func (r Row) Stalled() bool { return r.Situation.StalledAt(r.in.Now) }

// Link is the pull request's page.
func (r Row) Link() string { return cmp.Or(r.PR.URL, r.PR.Ref.URL()) }

// Mine says the viewer authored the pull request.
func (r Row) Mine() bool { return MineAuthor(r.Payload) }

// MineAuthor says the viewer authored the pull request of p.
func MineAuthor(p Payload) bool { return p.Viewer != "" && strings.EqualFold(p.Author, p.Viewer) }

// InScope says the row has reasons, a review workspace, or is adopted or
// tracked.
func (r Row) InScope() bool {
	p := r.Payload
	return len(p.Reasons) > 0 || p.Workspace.IsReview || p.Override.Adopted || p.Override.Tracked
}

// NeedsMe is the needs-me predicate of a situation: it waits on the user,
// asks, holds or is the user's own to fix, in scope or in the loop.
func NeedsMe(kind situation.Kind, waitingOn string, p Payload) bool {
	asks := kind == situation.KindLoopWaiting || kind == situation.KindLoopStopped
	needs := (waitingOn == "me" && kind != situation.KindLoopWorking) || asks ||
		kind == situation.KindHeldByCap || kind == situation.KindRoundPending ||
		(MineAuthor(p) && (kind == situation.KindChecks || kind == situation.KindChanges || kind == situation.KindConflicts))
	return needs && (len(p.Reasons) > 0 || p.Workspace.IsReview || asks)
}

// views puts a pull request outside the scope (no reasons) only where the
// loop or an adoption put it: loop, mine, and needs-me for a review
// workspace's question or an author workspace whose agent waits for me.
func (r Row) views() []string {
	out := []string{}
	p, kind := r.Payload, r.Kind()
	if NeedsMe(kind, r.Situation.WaitingOn, p) {
		out = append(out, ViewNeedsMe)
	}
	if r.Stalled() && len(p.Reasons) > 0 {
		out = append(out, ViewStalled)
	}
	if (MineAuthor(p) || p.Override.Adopted) && kind != situation.KindDone {
		out = append(out, ViewMine)
	}
	if p.Workspace.IsReview {
		out = append(out, ViewLoop)
	}
	if p.Approved {
		out = append(out, ViewApproved)
	}
	if kind == situation.KindDone && p.Workspace.ID != "" {
		out = append(out, ViewDone)
	}
	return out
}

// approved says the work on the pull request is approved: in a review
// workspace, my newest review approved its head; in mine, it can merge.
func (r Row) approved() bool {
	p, pull := r.Payload, r.PR
	if pull.State != "OPEN" || p.Workspace.ID == "" || p.Workspace.IsMain {
		return false
	}
	switch r.Kind() {
	case situation.KindHeldByCap, situation.KindBlocked, situation.KindLoopWorking, situation.KindLoopWaiting:
		return false
	}
	if p.Workspace.IsReview {
		var last pr.Event
		for _, e := range r.in.Events {
			if e.Kind == pr.KindReview && strings.EqualFold(e.Actor, r.in.Viewer) && !e.At.Before(last.At) {
				last = e
			}
		}
		return r.in.Viewer != "" && pull.HeadOid != "" && last.Data == "APPROVED:"+pull.HeadOid
	}
	return MineAuthor(p) && r.Kind() == situation.KindMerge
}

// humanEvents are the event kinds a person's own action on GitHub leaves.
var humanEvents = []string{pr.KindOpened, pr.KindPush, pr.KindReview, pr.KindComment}

// lastHuman is the newest action of a person other than the viewer, bots
// and the ignored reviewers; nil for none.
func lastHuman(in Input, link string) *LastHuman {
	var last *pr.Event
	for i, e := range in.Events {
		if !slices.Contains(humanEvents, e.Kind) || e.Actor == "" || strings.HasSuffix(e.Actor, "[bot]") ||
			strings.EqualFold(e.Actor, in.Viewer) || slices.ContainsFunc(in.Ignore, func(s string) bool { return strings.EqualFold(s, e.Actor) }) {
			continue
		}
		if last == nil || !e.At.Before(last.At) {
			last = &in.Events[i]
		}
	}
	if last == nil {
		return nil
	}
	return &LastHuman{Who: last.Actor, What: last.Kind, At: last.At, Link: link}
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
