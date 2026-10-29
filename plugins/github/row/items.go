package row

import (
	"slices"
	"strings"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/situation"
)

// ItemFollow is the item of a followed pull request that asks nothing
// else: it waits on whoever the pull request waits on, or on the loop, so
// the pull request keeps its one task.
const ItemFollow = "gh:follow"

// The fields of a derived item.
const (
	kindTodo, kindRequest, kindDecision           = "todo", "request", "decision"
	categoryInbox, categoryReady                  = "inbox", "ready"
	basisNone, basisSelf, basisMention            = "none", "self_assigned", "explicit_mention"
	stateOpen, confidenceHigh, provenanceDerived  = "open", "high", "derived"
	summaryMax                                    = 300
	contextDecide, contextAgent, contextRepoStart = "@decide", "@agent", "@repo-"
)

// Items are the pull request's items from its source row, every one keyed
// on the pull request so they are the asks of its one task: what waits on
// the viewer when the row needs them or is the viewer's own to merge, staff
// or answer, what waits on others in scope, and otherwise, while the pull
// request is open and followed, ItemFollow. A held or blocked round asks
// through its gate, never an item.
func (r Row) Items() []src.Item {
	if r.Payload.Viewer == "" || r.Kind() == situation.KindDone {
		return nil
	}
	out := r.asks()
	if len(out) == 0 && (r.InScope() || r.needsMe()) {
		out = []src.Item{r.followItem()}
	}
	for i := range out {
		out[i].TaskKey = r.taskKey()
	}
	return out
}

func (r Row) asks() []src.Item {
	switch r.Kind() {
	case situation.KindLoopWaiting, situation.KindLoopWorking, situation.KindHeldByCap, situation.KindBlocked:
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

// TaskKey is the key every item of the pull request carries: its unit.
func (r Row) taskKey() string {
	u := r.PR.Ref.Unit()
	return u.Provider + ":" + u.Key
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

// followItem waits on the viewer when the pull request needs them, such as
// an agent waiting with no question the loop holds or a round its gate
// holds; on whoever else it waits on; and on the loop otherwise, such as
// while the loop works on it.
func (r Row) followItem() src.Item {
	it := r.item(kindTodo, "", r.repoContext())
	it.ID, it.Requester, it.Basis = ItemFollow, Person(r.Payload.Viewer), basisNone
	on := []src.Person{r.loop()}
	switch who := People(r.Situation.WaitingOn, r.Payload); {
	case r.needsMe():
		on = []src.Person{Person(r.Payload.Viewer)}
	case r.Kind() != situation.KindLoopWorking && len(who) > 0 && !slices.ContainsFunc(who, r.isViewer):
		on = who
	}
	it.Assignees, it.WaitingOn = on, on
	it.Summary = r.summary("Follow")
	return it
}

func (r Row) isViewer(p src.Person) bool { return p.Key == Person(r.Payload.Viewer).Key }

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

func (r Row) summary(verb string) string {
	s := verb + " " + r.PR.Ref.String()
	if r.PR.Title != "" {
		s += ": " + r.PR.Title
	}
	return pr.OneLine(s, summaryMax)
}
