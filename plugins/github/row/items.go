package row

import (
	"strings"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/situation"
)

// The ids of the items whose tasks carry a notification, which the host
// keys its notices by.
const (
	ItemHeld    = "gh:held"
	ItemBlocked = "gh:blocked"
	ItemWait    = "gh:wait"
)

// The fields of a derived item.
const (
	kindTodo, kindRequest, kindDecision           = "todo", "request", "decision"
	categoryInbox, categoryReady                  = "inbox", "ready"
	basisNone, basisSelf, basisMention            = "none", "self_assigned", "explicit_mention"
	stateOpen, confidenceHigh, provenanceDerived  = "open", "high", "derived"
	summaryMax                                    = 300
	contextDecide, contextAgent, contextRepoStart = "@decide", "@agent", "@repo-"
)

// Items are the pull request's items from its source row: what waits on
// the viewer when the row needs them or is the viewer's own to merge,
// staff or answer, and what waits on others in scope.
func (r Row) Items() []src.Item {
	if r.Payload.Viewer == "" {
		return nil
	}
	switch r.Kind() {
	case situation.KindLoopWaiting:
		if r.needsMe() {
			return []src.Item{r.waitItem()}
		}
		return nil
	case situation.KindDone, situation.KindLoopWorking:
		return nil
	}
	if r.needsMe() || r.ownNext() {
		return []src.Item{r.viewerItem()}
	}
	if !r.InScope() {
		return nil
	}
	if it, ok := r.othersItem(); ok {
		return []src.Item{it}
	}
	return nil
}

func (r Row) needsMe() bool { return NeedsMe(r.Kind(), r.Situation.WaitingOn, r.Payload) }

// ownNext tells whether the row is the viewer's own in-scope pull request
// that waits on its author rather than on the viewer: merge, no reviewer,
// a reviewer's comment to answer.
func (r Row) ownNext() bool {
	k := r.Kind()
	return r.InScope() && r.Mine() && (k == situation.KindMerge || k == situation.KindNoReviewer || k == situation.KindReply)
}

func (r Row) viewerItem() src.Item {
	p, kind, mine := r.Payload, r.Kind(), r.Mine()
	viewer := Person(p.Viewer)
	it := r.item(kindTodo, categoryInbox, r.repoContext())
	it.Requester, it.Assignees, it.WaitingOn, it.Basis = Person(p.Author), []src.Person{viewer}, []src.Person{viewer}, basisNone
	switch {
	case kind == situation.KindHeldByCap:
		it.ID, it.Kind, it.Contexts, it.Requester = HeldID(r.Situation.Since), kindDecision, []string{contextDecide}, r.loop()
		it.Summary = r.summary("Decide the held round on")
	case kind == situation.KindBlocked:
		it.ID, it.Kind, it.Contexts, it.Requester = ItemBlocked, kindDecision, []string{contextDecide}, r.loop()
		it.Summary = r.summary("Unblock the round on")
	case mine && kind == situation.KindReply:
		it.ID, it.Kind, it.Category, it.Basis = "gh:review", kindRequest, categoryReady, basisSelf
		it.Summary = r.summary("Answer the review on")
	case kind == situation.KindReview || kind == situation.KindReviewerBack || kind == situation.KindReply:
		it.ID, it.Kind, it.Category, it.Basis = "gh:review", kindRequest, categoryReady, basisMention
		it.Summary = r.summary("Review")
	case kind == situation.KindToAddress ||
		mine && (kind == situation.KindChanges || kind == situation.KindChecks || kind == situation.KindConflicts):
		it.ID, it.Category, it.Basis = "gh:address", categoryReady, basisSelf
		it.Summary = r.summary("Address")
	case mine && kind == situation.KindMerge:
		it.ID, it.Category, it.Basis = "gh:merge", categoryReady, basisSelf
		it.Summary = r.summary("Merge")
	case mine && kind == situation.KindNoReviewer:
		it.ID, it.Kind, it.Category, it.Contexts, it.Basis = "gh:reviewer", kindDecision, categoryReady, []string{contextDecide}, basisSelf
		it.Summary = r.summary("Pick a reviewer for")
	default:
		it.ID = "gh:attention"
		it.Summary = r.summary("Look at")
	}
	return it
}

// waitItem is an agent that waits on the viewer with no question the loop
// holds, such as a permission prompt; a question is the turn's Inbox
// message, which the host drops it for.
func (r Row) waitItem() src.Item {
	viewer := Person(r.Payload.Viewer)
	it := r.item(kindTodo, categoryInbox, []string{contextAgent})
	it.ID, it.Requester, it.Assignees, it.WaitingOn, it.Basis = ItemWait, r.loop(), []src.Person{viewer}, []src.Person{viewer}, basisNone
	it.Summary = r.summary("Answer the agent on")
	return it
}

// othersItem is what the viewer waits for from someone else: the author's
// fix after the viewer's review, or the reviewers on the viewer's own pull
// request.
func (r Row) othersItem() (src.Item, bool) {
	p, kind, waitingOn := r.Payload, r.Kind(), r.Situation.WaitingOn
	switch {
	case !r.Mine() && waitingOn == "author" && r.afterViewerReview():
		author := Person(p.Author)
		it := r.item(kindRequest, categoryReady, r.repoContext())
		it.ID, it.Requester, it.Assignees, it.WaitingOn, it.Basis = "gh:fix", Person(p.Viewer), []src.Person{author}, []src.Person{author}, basisMention
		it.Summary = r.summary("Wait for " + p.Author + "'s fix on")
		return it, true
	case r.Mine() && (kind == situation.KindReview || kind == situation.KindReviewerBack):
		reviewers := People(waitingOn, p)
		if len(reviewers) == 0 || waitingOn == "me" || waitingOn == "author" {
			return src.Item{}, false
		}
		it := r.item(kindRequest, categoryReady, r.repoContext())
		it.ID, it.Requester, it.Assignees, it.WaitingOn, it.Basis = "gh:review", Person(p.Viewer), reviewers, reviewers, basisMention
		it.Summary = r.summary("Wait for the review of")
		return it, true
	}
	return src.Item{}, false
}

// afterViewerReview tells whether the author owes the viewer: the loop
// reviews the pull request as the viewer, or the viewer's changes request or
// comment is what the row waits on.
func (r Row) afterViewerReview() bool {
	kind, reason, viewer := r.Kind(), r.Situation.Reason, r.Payload.Viewer
	if r.Payload.Workspace.IsReview {
		return kind == situation.KindChanges || kind == situation.KindReply || kind == situation.KindChecks || kind == situation.KindConflicts
	}
	switch kind {
	case situation.KindChanges:
		who, ok := strings.CutPrefix(reason, "changes requested by ")
		return ok && strings.EqualFold(who, viewer)
	case situation.KindReply:
		who, ok := strings.CutSuffix(reason, " commented, no reply")
		return ok && strings.EqualFold(who, viewer)
	}
	return false
}

func (r Row) item(kind, category string, contexts []string) src.Item {
	link := r.Link()
	return src.Item{
		Unit:       r.PR.Ref.Unit(),
		Kind:       kind,
		URLs:       []string{link},
		Contexts:   contexts,
		Category:   category,
		Origin:     src.Anchor{Unit: r.PR.Ref.Unit(), Ref: "situation:" + string(r.Kind()), Permalink: link},
		State:      stateOpen,
		Confidence: confidenceHigh,
		Provenance: provenanceDerived,
	}
}

func (r Row) repoContext() []string {
	_, name, _ := strings.Cut(r.Payload.Repo, "/")
	if name == "" {
		name = r.Payload.Repo
	}
	return []string{contextRepoStart + name}
}

func (r Row) loop() src.Person { return src.Person{Key: "system:" + r.in.Loop, Name: r.in.Loop} }

// HeldID names a hold by its local day, the day the situation counts holds
// in, so the next day's hold is a new decision.
func HeldID(since time.Time) string {
	if since.IsZero() {
		return ItemHeld
	}
	return ItemHeld + ":" + since.Local().Format(time.DateOnly)
}

func (r Row) summary(verb string) string {
	s := verb + " " + r.PR.Ref.String()
	if r.PR.Title != "" {
		s += ": " + r.PR.Title
	}
	return pr.OneLine(s, summaryMax)
}
