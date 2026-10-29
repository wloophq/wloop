package plan

import (
	"cmp"
	"maps"
	"slices"
	"strings"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/pr"
)

// Plan is the rounds r plans on ref, each with the marks the host writes
// when it sends or skips it.
func (p *Planner) Plan(ref pr.Ref, r Result) src.Plan {
	f, _ := p.factsOf(ref)
	head := f.HeadRefOid
	var out src.Plan
	if r.review.action == actionFollowup {
		fp := r.review.fingerprint
		out.Rounds = append(out.Rounds, src.Round{Name: RoundFollowup, Workspace: wsRef(r.review.ws), Head: head, Reason: r.review.reason,
			Observed: r.review.observed, OnSent: kept(map[string]string{MarkReviewed: fp}), OnSkipped: map[string]string{MarkReviewed: fp}})
	}
	if r.first.due || p.Requested(ref) && !p.firstSkipped(ref) {
		first := src.Round{Name: RoundFirst, Head: head, Reason: r.first.reason, OnSkipped: map[string]string{MarkFirstSkipped: "1"}}
		if !r.first.due {
			first.NotDue = cmp.Or(r.first.reason, "my review was requested")
		}
		out.Rounds = append(out.Rounds, first)
	}
	if r.address.due {
		marks := kept(map[string]string{MarkAddressedReviews: r.address.fingerprint, MarkAddressedChecks: r.address.checksHead,
			MarkAddressedConflict: r.address.conflictHead})
		out.Rounds = append(out.Rounds, src.Round{Name: RoundAddress, Workspace: wsRef(r.address.ws), Head: head, Reason: r.address.reason,
			Observed: r.address.observed, OnSent: marks, OnSkipped: maps.Clone(marks), TurnFacts: addressFacts(r)})
	}
	return out
}

// addressFacts are the turn slot facts of r's address round.
func addressFacts(r Result) []src.Fact {
	return []src.Fact{
		{Name: "pr.address.reviewers", Value: src.List(r.address.reviewers...)},
		{Name: "pr.address.checks", Value: src.List(r.address.checks...)},
		{Name: "pr.address.conflict", Value: src.Bool(r.address.conflictHead != "")},
	}
}

// kept are the marks of m with a value: a round that covered nothing new
// leaves a mark as it was.
func kept(m map[string]string) map[string]string {
	maps.DeleteFunc(m, func(_, v string) bool { return v == "" })
	return m
}

func wsRef(w workspace) src.WorkspaceRef { return src.WorkspaceRef{Manager: w.Manager, ID: w.ID} }

// Facts are the workflow facts r decides on ref.
func (p *Planner) Facts(ref pr.Ref, r Result) []src.Fact {
	_, hasReview := p.t.review[ref]
	_, hasAuthor := p.t.author[ref]
	return []src.Fact{
		{Name: "pr.review.action", Value: src.String(r.review.action)},
		{Name: "pr.review.reason", Value: src.String(r.review.reason)},
		{Name: "pr.review.observed", Value: src.Bool(r.review.observed)},
		{Name: "pr.first.due", Value: src.Bool(r.first.due)},
		{Name: "pr.first.reason", Value: src.String(r.first.reason)},
		{Name: "pr.address.due", Value: src.Bool(r.address.due)},
		{Name: "pr.address.reason", Value: src.String(r.address.reason)},
		{Name: "pr.address.observed", Value: src.Bool(r.address.observed)},
		{Name: "pr.address.reviewers", Value: src.List(r.address.reviewers...)},
		{Name: "pr.address.checks", Value: src.List(r.address.checks...)},
		{Name: "pr.address.conflict", Value: src.Bool(r.address.due && r.address.conflictHead != "")},
		{Name: "pr.fingerprint", Value: src.String(p.fingerprint(ref, r))},
		{Name: "pr.has_workspace", Value: src.Bool(hasReview || hasAuthor)},
	}
}

// fingerprint is what the round rules handled until something new happens:
// each due round's own fingerprint, so a run or a dismissal of one does not
// hold back the next.
func (p *Planner) fingerprint(ref pr.Ref, r Result) string {
	var parts []string
	if r.review.action == actionFollowup {
		parts = append(parts, "review:"+r.review.fingerprint)
	}
	if r.first.due {
		parts = append(parts, "first:"+p.byRef[ref].HeadOid)
	}
	if r.address.due {
		a := "address:" + r.address.fingerprint + "@" + r.address.checksHead
		if r.address.conflictHead != "" {
			a += "!conflict@" + r.address.conflictHead
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, ";")
}

// Rank orders units as the rounds are planned: follow-ups by their
// review's time, first rounds by the pull request's age, address rounds by
// their workspace's place and then among my pull requests, then the units
// no round is due on.
type Rank struct {
	Path int       `json:"path"`
	At   time.Time `json:"at,omitzero"`
	Idx  int       `json:"idx,omitempty"`
}

// Compare orders a before b.
func (a Rank) Compare(b Rank) int {
	return cmp.Or(cmp.Compare(a.Path, b.Path), a.At.Compare(b.At), cmp.Compare(a.Idx, b.Idx))
}

// RankOf is r's rank of ref.
func (p *Planner) RankOf(ref pr.Ref, r Result) Rank {
	k := Rank{Path: 3}
	switch {
	case r.review.action == actionFollowup:
		k.Path, k.At = 0, r.review.lastReviewAt
	case r.first.due:
		k.Path, k.At = 1, p.byRef[ref].CreatedAt
	case r.address.due:
		k.Path, k.Idx = 2, slices.Index(p.t.authorOrder, ref)
		if k.Idx < 0 {
			k.Idx = len(p.t.authorOrder) + slices.IndexFunc(p.in.Rows, func(x pr.PR) bool { return x.Ref == ref })
		}
	}
	return k
}

// Board is what the host's board reads of a pull request's workspaces: the
// review and author workspaces the planner targets it with, and the one an
// author card reads its checkout from.
type Board struct {
	// Carded says a workspace targets the pull request.
	Carded          bool `json:"carded,omitempty"`
	ReviewWorkspace bool `json:"review_workspace,omitempty"`
	// AuthorWorkspace says an author workspace targets it and holds no
	// address round, nor one a partial listing hides.
	AuthorWorkspace bool `json:"author_workspace,omitempty"`
	// Checkout is the author workspace an author card reads; nil for none.
	Checkout *Checkout `json:"checkout,omitempty"`
}

// Checkout is a local workspace an author card reads.
type Checkout struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// BoardOf is ref's board under r.
func (p *Planner) BoardOf(ref pr.Ref, r Result) Board {
	var b Board
	_, b.ReviewWorkspace = p.t.review[ref]
	w, found := p.t.author[ref]
	b.Carded = found || b.ReviewWorkspace
	b.AuthorWorkspace = found && !b.ReviewWorkspace && !r.address.due && r.address.reason != WhyPartial
	if !found {
		if f, ok := p.factsOf(ref); ok {
			w, found = p.authorWorkspace(f.Facts, p.in.Viewer != "" && strings.EqualFold(f.Author, p.in.Viewer))
		}
	}
	if found {
		b.Checkout = &Checkout{ID: w.ID, Path: w.Path}
	}
	return b
}

// Settlement is what planning leaves behind on a pull request: the marks
// to set, "" removing one, and the events to record.
type Settlement struct {
	Marks  map[string]string
	Events []pr.Event
}

// Settle is what r leaves behind on ref: the fingerprint a review stored
// without its head gets, the first round skip a review workspace clears,
// and the first rounds' block and unblock events.
func (p *Planner) Settle(ref pr.Ref, r Result) Settlement {
	var s Settlement
	if p.in.Viewer == "" {
		return s
	}
	st := p.in.Rounds[ref]
	if r.review.upgrade != "" && r.review.settle && !st.Paused && st.Marks[MarkReviewed] != r.review.upgrade {
		s.Marks = map[string]string{MarkReviewed: r.review.upgrade}
	}
	block, blocked := p.block(ref)
	unblock := func(cleared string) {
		if blocked {
			s.Events = append(s.Events, pr.Event{PR: ref, Kind: pr.KindRoundUnblocked, At: p.in.Now, Data: cleared})
			blocked = false
		}
	}
	if f := r.first.settle; f.set {
		if f.clear && p.firstSkipped(ref) {
			if s.Marks == nil {
				s.Marks = map[string]string{}
			}
			s.Marks[MarkFirstSkipped] = ""
		}
		if f.unblock != "" {
			unblock(f.unblock)
		}
		if f.block != "" && (!blocked || block.Data != f.block) {
			s.Events = append(s.Events, pr.Event{PR: ref, Kind: pr.KindRoundBlocked, At: p.in.Now, Data: f.block})
		}
		return s
	}
	if !blocked || p.Requested(ref) || StartBlock(block) && p.stillOpen(ref) {
		return s
	}
	if still, cleared := p.stillRequested(ref); !still {
		unblock(cleared)
	}
	return s
}

// stillOpen says whether the store does not know ref closed.
func (p *Planner) stillOpen(ref pr.Ref) bool {
	row, found := p.byRef[ref]
	return !found || row.State == pr.StateOpen
}

// stillRequested tells, from the store, whether a blocked pull request may
// still request my review: the store may not have read it yet.
func (p *Planner) stillRequested(ref pr.Ref) (bool, string) {
	row, found := p.byRef[ref]
	switch {
	case !found:
		return true, ""
	case row.State != pr.StateOpen:
		return false, "it is " + strings.ToLower(row.State)
	case !containsFold(row.ReviewRequests, p.in.Viewer):
		return false, "my review is no longer requested"
	}
	return true, ""
}
