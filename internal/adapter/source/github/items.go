package ghsource

import (
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
	"github.com/wloophq/wloop/internal/situation"
	"github.com/wloophq/wloop/internal/termfmt"
)

// Items derives a pull request's items from its row (GTD spec 9.3): what
// waits on the viewer when the row needs them or is the viewer's own to
// merge, staff or answer, and what waits on others in scope. A muted row
// derives as its unmuted situation.
func Items(r api.Row) []items.Item {
	p, ok := prrow.PayloadOf(r)
	if !ok || p.Viewer == "" {
		return nil
	}
	s := situationOf(r, p)
	switch s.kind {
	case situation.KindLoopWaiting:
		if situation.Kind(r.Kind) == situation.KindLoopWaiting && r.Question == nil && prrow.NeedsMe(r) {
			return []items.Item{s.waitItem()}
		}
		return nil
	case situation.KindDone, situation.KindLoopStopped, situation.KindLoopWorking:
		return nil
	}
	if s.needsMe() || ownNext(r) {
		return []items.Item{s.viewerItem()}
	}
	if !prrow.InScope(r) {
		return nil
	}
	if it, ok := s.othersItem(); ok {
		return []items.Item{it}
	}
	return nil
}

// ownNext tells whether r is the viewer's own in-scope pull request that
// spec 9.3 puts in Next although it waits on its author rather than on the
// viewer: merge, no reviewer, a reviewer's comment to answer.
func ownNext(r api.Row) bool {
	p, ok := prrow.PayloadOf(r)
	if !ok || p.Viewer == "" || !prrow.InScope(r) {
		return false
	}
	d := situationOf(r, p)
	return d.mine && (d.kind == situation.KindMerge || d.kind == situation.KindNoReviewer || d.kind == situation.KindReply)
}

// needsMe is prrow.NeedsMe, except that a muted row keeps its pending round,
// which prrow's predicate does not see under the mute.
func (d derivation) needsMe() bool {
	if d.mutedRound {
		return len(d.p.Reasons) > 0 || d.p.Workspace.IsReview
	}
	return prrow.NeedsMe(d.row)
}

// derivation is a row read in the terms of its unmuted situation.
type derivation struct {
	row       api.Row
	p         prrow.Payload
	kind      situation.Kind
	waitingOn string
	reason    string
	mine      bool
	// mutedRound: the row is muted with a pending round, which it derives as.
	mutedRound bool
}

func situationOf(r api.Row, p prrow.Payload) derivation {
	d := derivation{row: r, p: p, kind: situation.Kind(r.Kind), waitingOn: r.Summary.WaitsOn, reason: r.Summary.Reason, mine: strings.EqualFold(p.Author, p.Viewer)}
	if d.kind == situation.KindMuted {
		d.kind, d.waitingOn, d.reason = situation.Kind(p.UnmutedKind), p.UnmutedWaitingOn, p.UnmutedReason
		if r.Pending != nil {
			d.kind, d.waitingOn, d.mutedRound = situation.KindRoundPending, "me", true
		}
	}
	return d
}

func (d derivation) viewerItem() items.Item {
	viewer := person(d.p.Viewer)
	it := d.item(items.Todo, items.InitialInbox, d.repoContext())
	it.Requester, it.Assignees, it.WaitingOn, it.Basis = person(d.p.Author), []items.Person{viewer}, []items.Person{viewer}, items.NoBasis
	switch {
	case d.kind == situation.KindRoundPending:
		it.ID, it.Kind, it.Contexts, it.Requester = "gh:round", items.Decision, decide, loopPerson()
		if pend := d.row.Pending; pend != nil {
			it.Deadline = pend.Deadline.UTC().Format(time.RFC3339)
		}
		it.Summary = summary("Decide the pending round on", d.row)
	case d.kind == situation.KindHeldByCap:
		it.ID, it.Kind, it.Contexts, it.Requester = heldID(d.row.Summary.Since), items.Decision, decide, loopPerson()
		it.Summary = summary("Decide the held round on", d.row)
	case d.kind == situation.KindBlocked:
		it.ID, it.Kind, it.Contexts, it.Requester = "gh:blocked", items.Decision, decide, loopPerson()
		it.Summary = summary("Unblock the round on", d.row)
	case d.mine && d.kind == situation.KindReply:
		it.ID, it.Kind, it.Initial, it.Basis = "gh:review", items.Request, items.InitialNext, items.SelfAssigned
		it.Summary = summary("Answer the review on", d.row)
	case d.kind == situation.KindReview || d.kind == situation.KindReviewerBack || d.kind == situation.KindReply:
		it.ID, it.Kind, it.Initial, it.Basis = "gh:review", items.Request, items.InitialNext, items.ExplicitMention
		it.Summary = summary("Review", d.row)
	case d.kind == situation.KindToAddress ||
		d.mine && (d.kind == situation.KindChanges || d.kind == situation.KindChecks || d.kind == situation.KindConflicts):
		it.ID, it.Initial, it.Basis = "gh:address", items.InitialNext, items.SelfAssigned
		it.Summary = summary("Address", d.row)
	case d.mine && d.kind == situation.KindMerge:
		it.ID, it.Initial, it.Basis = "gh:merge", items.InitialNext, items.SelfAssigned
		it.Summary = summary("Merge", d.row)
	case d.mine && d.kind == situation.KindNoReviewer:
		it.ID, it.Kind, it.Initial, it.Contexts, it.Basis = "gh:reviewer", items.Decision, items.InitialNext, decide, items.SelfAssigned
		it.Summary = summary("Pick a reviewer for", d.row)
	default:
		it.ID = "gh:attention"
		it.Summary = summary("Look at", d.row)
	}
	return it
}

// waitItem is an agent that waits on the viewer with no question the loop
// holds, such as a permission prompt; a question is the turn's Inbox message.
func (d derivation) waitItem() items.Item {
	viewer := person(d.p.Viewer)
	it := d.item(items.Todo, items.InitialInbox, []string{"@agent"})
	it.ID, it.Requester, it.Assignees, it.WaitingOn, it.Basis = "gh:wait", loopPerson(), []items.Person{viewer}, []items.Person{viewer}, items.NoBasis
	it.Summary = summary("Answer the agent on", d.row)
	return it
}

// othersItem is what the viewer waits for from someone else: the author's
// fix after the viewer's review, or the reviewers on the viewer's own pull
// request.
func (d derivation) othersItem() (items.Item, bool) {
	switch {
	case !d.mine && d.waitingOn == "author" && d.afterViewerReview():
		author := person(d.p.Author)
		it := d.item(items.Request, items.InitialNext, d.repoContext())
		it.ID, it.Requester, it.Assignees, it.WaitingOn, it.Basis = "gh:fix", person(d.p.Viewer), []items.Person{author}, []items.Person{author}, items.ExplicitMention
		it.Summary = summary("Wait for "+d.p.Author+"'s fix on", d.row)
		return it, true
	case d.mine && (d.kind == situation.KindReview || d.kind == situation.KindReviewerBack):
		reviewers := people(d.waitingOn, d.p)
		if len(reviewers) == 0 || d.waitingOn == "me" || d.waitingOn == "author" {
			return items.Item{}, false
		}
		it := d.item(items.Request, items.InitialNext, d.repoContext())
		it.ID, it.Requester, it.Assignees, it.WaitingOn, it.Basis = "gh:review", person(d.p.Viewer), reviewers, reviewers, items.ExplicitMention
		it.Summary = summary("Wait for the review of", d.row)
		return it, true
	}
	return items.Item{}, false
}

// afterViewerReview tells whether the author owes the viewer: the loop
// reviews the pull request as the viewer, or the viewer's changes request or
// comment is what the row waits on.
func (d derivation) afterViewerReview() bool {
	if d.p.Workspace.IsReview {
		return d.kind == situation.KindChanges || d.kind == situation.KindReply || d.kind == situation.KindChecks || d.kind == situation.KindConflicts
	}
	switch d.kind {
	case situation.KindChanges:
		who, ok := strings.CutPrefix(d.reason, "changes requested by ")
		return ok && strings.EqualFold(who, d.p.Viewer)
	case situation.KindReply:
		who, ok := strings.CutSuffix(d.reason, " commented, no reply")
		return ok && strings.EqualFold(who, d.p.Viewer)
	}
	return false
}

func (d derivation) item(kind items.Kind, initial items.Initial, contexts []string) items.Item {
	r := d.row
	var urls []string
	if r.Link != "" {
		urls = []string{r.Link}
	}
	return items.Item{
		Unit:       r.Unit,
		Kind:       kind,
		URLs:       urls,
		Contexts:   contexts,
		Initial:    initial,
		Origin:     anchorOf(r, d.kind),
		State:      items.Open,
		Confidence: items.High,
		Provenance: items.Derived,
	}
}

func (d derivation) repoContext() []string {
	_, name, _ := strings.Cut(d.p.Repo, "/")
	if name == "" {
		name = d.p.Repo
	}
	return []string{"@repo-" + name}
}

var decide = []string{"@decide"}

// heldID names a hold by its local day, the day situation counts holds in,
// so the next day's hold is a new decision; a muted row whose mute left no
// event dates nothing.
func heldID(since time.Time) string {
	if since.IsZero() {
		return "gh:held"
	}
	return "gh:held:" + since.Local().Format(time.DateOnly)
}

func anchorOf(r api.Row, kind situation.Kind) unit.Anchor {
	return unit.Anchor{Unit: r.Unit, Ref: "situation:" + string(kind), Permalink: r.Link}
}

func summary(verb string, r api.Row) string {
	s := verb + " " + r.Label
	if r.Title != "" {
		s += ": " + r.Title
	}
	return termfmt.OneLine(s, items.MaxSummary)
}

// person is a GitHub login, or the group of a "team:<org>/<slug>" request.
func person(login string) items.Person {
	if team, ok := strings.CutPrefix(login, "team:"); ok {
		return items.Person{Key: "group:" + team, Name: team}
	}
	return items.Person{Key: "github:" + login, Name: login}
}

func loopPerson() items.Person {
	return items.Person{Key: "system:" + brand.Name, Name: brand.Name}
}

// people reads a row's waiting-on: "me", "author", "nobody" or logins.
func people(waitingOn string, p prrow.Payload) []items.Person {
	switch waitingOn {
	case "", "nobody":
		return nil
	case "me":
		return []items.Person{person(p.Viewer)}
	case "author":
		return []items.Person{person(p.Author)}
	}
	var out []items.Person
	for l := range strings.SplitSeq(waitingOn, ", ") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, person(l))
		}
	}
	return out
}
