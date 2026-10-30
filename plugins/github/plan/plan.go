// Package plan is the GitHub plugin's round planner: over the plugin's
// pull requests and the host's round state, books, events and workspaces,
// it decides which automatic rounds are due, why, and what each covers.
package plan

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/change"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/store"
)

// authorLookback is how long after it ended a pull request of mine still
// matches its workspace by branch.
const authorLookback = 14 * 24 * time.Hour

const (
	actionFollowup = "follow-up"
	actionComplete = "complete"

	whyNotStored = "not in the inventory yet"
	whyLapsed    = "nothing follows it in the inventory"
	// WhyPartial is why no round is due while the workspace listing may
	// miss a workspace.
	WhyPartial = "the workspace listing is incomplete, no rounds this sweep"
)

// roleReview is the role of the review intents, as plugin.toml declares it.
const roleReview = "pr.review"

var errNoViewer = errors.New("the GitHub login is not stored yet; the fast job stores it")

// Result is what the review, first-round and address rules decide on one
// pull request; the host's turn.start guards filter it further.
type Result struct {
	stale   bool
	review  reviewResult
	first   firstResult
	address addressResult
	// dues are the instants a decision changes with no input changing, by
	// kind.
	dues map[string]time.Time
}

type reviewResult struct {
	target       bool
	ws           workspace
	action       string
	reason       string
	observed     bool
	fingerprint  string
	lastReviewAt time.Time
	// upgrade is the fingerprint stored on a decision of nothing.
	upgrade string
	settle  bool
}

type firstResult struct {
	due    bool
	reason string
	// settle is what settling records of the first round; set says it
	// records anything.
	settle firstSettle
}

type firstSettle struct {
	set            bool
	clear          bool
	unblock, block string
}

type addressResult struct {
	target                  bool
	ws                      workspace
	due                     bool
	reason                  string
	reviewers, checks       []string
	fingerprint, checksHead string
	// conflictHead is the head whose lasting conflict with the base the
	// round resolves.
	conflictHead string
	observed     bool
}

// Runs says whether a round of r would run without the user: due and not
// only observed.
func (r Result) Runs() bool {
	return (r.review.action == actionFollowup && !r.review.observed) || r.first.due || (r.address.due && !r.address.observed)
}

// Due is the earliest instant after now a decision of r changes with no
// input changing; zero for none.
func (r Result) Due(now time.Time) time.Time {
	var due time.Time
	for _, t := range r.dues {
		if t.After(now) && (due.IsZero() || t.Before(due)) {
			due = t
		}
	}
	return due
}

func (r *Result) dueAt(kind string, t time.Time) {
	if old, ok := r.dues[kind]; !ok || t.Before(old) {
		r.dues[kind] = t
	}
}

// Planner derives results over one read of its inputs.
type Planner struct {
	ctx    context.Context
	in     Inputs
	cfg    Config
	byRef  map[pr.Ref]pr.PR
	ws     []workspace
	owners []string
	t      *targets
}

// New is the planner over in.
func New(ctx context.Context, in Inputs) *Planner {
	p := &Planner{ctx: ctx, in: in, cfg: in.Cfg, byRef: make(map[pr.Ref]pr.PR, len(in.Rows)), ws: workspaces(in.Listing)}
	for _, r := range in.Rows {
		p.byRef[r.Ref] = r
	}
	p.owners = p.ownersOf()
	p.t = p.buildTargets()
	return p
}

// ownersOf are the owners automated rounds may run for: the configured
// ones, or those of every repository the workspace managers know.
func (p *Planner) ownersOf() []string {
	owners := p.cfg.Owners
	if len(owners) == 0 {
		for _, r := range p.in.Listing.Repos {
			if owner, _, ok := ownerName(r.Remote); ok && !containsFold(owners, owner) {
				owners = append(owners, owner)
			}
		}
	}
	return owners
}

func (p *Planner) partial() bool { return p.in.Unlisted || p.in.Listing.Partial }

func (p *Planner) marks(ref pr.Ref) map[string]string { return p.in.Rounds[ref].Marks }

func (p *Planner) mark(ref pr.Ref, name string) string { return p.marks(ref)[name] }

func (p *Planner) firstSkipped(ref pr.Ref) bool { return p.mark(ref, MarkFirstSkipped) != "" }

// factsOf is ref's stored facts, and whether the store holds ref.
func (p *Planner) factsOf(ref pr.Ref) (facts, bool) {
	row, ok := p.byRef[ref]
	if !ok {
		return facts{}, false
	}
	d, read := p.in.Details[ref]
	f := facts{Facts: store.FactsOf(row, d, p.in.Viewer)}
	if read && d.Version == store.DetailVersion {
		f.ReadAt = d.ReadAt
	}
	return f, true
}

// Derive is what the rules decide on ref.
func (p *Planner) Derive(ref pr.Ref) (Result, error) {
	r := Result{dues: map[string]time.Time{}}
	if row, ok := p.byRef[ref]; ok && row.State == pr.StateOpen {
		f, _ := p.factsOf(ref)
		r.stale = p.in.Now.Sub(f.ReadAt) >= p.cfg.maxAge()
	}
	if p.in.Viewer == "" {
		r.review.reason, r.first.reason, r.address.reason = errNoViewer.Error(), errNoViewer.Error(), errNoViewer.Error()
		return r, nil
	}
	if err := p.review(ref, &r); err != nil {
		return r, err
	}
	p.first(ref, &r)
	p.address(ref, &r)
	if p.partial() {
		if r.review.action == actionFollowup {
			r.review.action, r.review.reason = "", WhyPartial
		}
		if r.first.due {
			r.first.due, r.first.reason = false, WhyPartial
		}
		if r.address.due {
			r.address.due, r.address.reason = false, WhyPartial
		}
	}
	return r, nil
}

// prOf is ref's stored facts, or why the plan cannot decide on it; follows
// says something the plan lists follows it. A stale detail is decided on:
// turn.start refuses it.
func (p *Planner) prOf(ref pr.Ref, follows bool, r *Result) (facts, string) {
	row, ok := p.byRef[ref]
	if !ok {
		return facts{}, whyNotStored
	}
	f, _ := p.factsOf(ref)
	if row.State == pr.StateOpen && !row.Followed() && !follows {
		return facts{}, whyLapsed
	}
	if r.stale {
		r.dueAt("unread", p.in.Now.Add(p.cfg.Fast))
	}
	return f, ""
}

func (p *Planner) review(ref pr.Ref, r *Result) error {
	ws, linked := p.t.review[ref]
	if !linked {
		ok, why := p.reviewWithoutWorkspace(ref, r)
		if !ok {
			r.review.reason = why
			return nil
		}
	}
	r.review.target, r.review.ws = true, ws
	f, why := p.prOf(ref, true, r)
	if why != "" {
		r.review.reason = why
		return nil
	}
	if ws.ID == "" {
		if why := p.notFollowedWithoutWorkspace(f.Facts); why != "" {
			r.review.reason = why
			return nil
		}
	}
	cfg := p.cfg.For(ref.Owner, ref.Repo)
	d := change.Reviewer(change.ReviewerInput{
		PR:              f.Facts,
		Viewer:          p.in.Viewer,
		WorkspaceStatus: ws.Status,
		CompletedStatus: p.cfg.completed(ws.Status),
		TrustedOwners:   p.owners,
		TrustedAuthors:  cfg.TrustedAuthors,
		LastFingerprint: p.mark(ref, MarkReviewed),
		Recorded:        p.recorded(ref),
	})
	r.review.reason, r.review.settle = d.Reason, !r.stale
	switch d.Action {
	case change.ReviewerNothing:
		r.review.upgrade = d.Fingerprint
	case change.ReviewerComplete:
		r.review.action = actionComplete
	case change.ReviewerFollowup:
		r.review.action, r.review.fingerprint, r.review.lastReviewAt = actionFollowup, d.Fingerprint, d.LastReviewAt
		r.review.observed = ws.ID == "" && !cfg.FollowupsWithoutWorkspace
	}
	return nil
}

// recorded is the verdict the newest review turn of ref published.
func (p *Planner) recorded(ref pr.Ref) change.RecordedReview {
	head, at, ok := LastPublished(p.in.Books[ref], roleReview)
	if !ok {
		return change.RecordedReview{}
	}
	return change.RecordedReview{Head: head, At: at}
}

// reviewWithoutWorkspace says whether ref is followed up without a review
// workspace: a recent review turn no review workspace links, but those I
// removed or took over, or why not.
func (p *Planner) reviewWithoutWorkspace(ref pr.Ref, r *Result) (bool, string) {
	newest, ok := NewestReview(p.in.Books[ref])
	if !ok || p.t.reviewLinked[strings.ToLower(ref.String())] {
		return false, ""
	}
	stored, found := p.byRef[ref]
	ended := slices.ContainsFunc(p.in.Events[ref], func(e pr.Event) bool { return e.Kind == pr.KindReviewEnded })
	switch {
	case ended && (!found || stored.State != pr.StateOpen):
		return false, "its review ended"
	case p.in.Now.Sub(newest) >= pr.ReviewTurnsFollowed:
		return false, fmt.Sprintf("its newest review turn is older than %s", pr.ReviewTurnsFollowed)
	case p.firstSkipped(ref):
		return false, "you removed it"
	case p.in.Adopted[ref]:
		return false, "you took it over"
	case found && stored.State != "" && stored.State != pr.StateOpen:
		return false, "it is " + strings.ToLower(stored.State)
	}
	r.dueAt("review cutoff", newest.Add(pr.ReviewTurnsFollowed))
	return true, ""
}

// notFollowedWithoutWorkspace says why a pull request whose review
// workspace is gone gets no follow-up, or "".
func (p *Planner) notFollowedWithoutWorkspace(f pr.Facts) string {
	viewer := p.in.Viewer
	switch {
	case f.State != pr.StateOpen:
		return "it is " + strings.ToLower(f.State)
	case strings.EqualFold(f.Author, viewer):
		return "you opened it"
	case containsFold(f.ReviewRequests, viewer):
		return "my review is requested, which starts a first round"
	}
	for _, w := range p.ws {
		if w.of(f.Ref) && p.isReviewWorkspace(w) && (w.DisplayName == p.cfg.ReviewPrefix+f.HeadRefName || w.linked == f.Number) {
			return "its review workspace is " + w.DisplayName
		}
	}
	if w, found := p.authorWorkspace(f, strings.EqualFold(f.Author, viewer)); found {
		return "you develop it in " + w.DisplayName
	}
	return ""
}

func (p *Planner) isReviewWorkspace(w workspace) bool {
	return !w.IsMain && strings.HasPrefix(w.DisplayName, p.cfg.ReviewPrefix)
}

// follows says whether a plan phase that runs before the first rounds lists
// ref, so a lapsed row of it is still read.
func (p *Planner) follows(ref pr.Ref) bool {
	if _, ok := p.t.review[ref]; ok {
		return true
	}
	ok, _ := p.reviewWithoutWorkspace(ref, &Result{dues: map[string]time.Time{}})
	return ok
}

// Requested says whether ref is an open, ready pull request that requests
// the viewer's review by name.
func (p *Planner) Requested(ref pr.Ref) bool {
	row, ok := p.byRef[ref]
	return ok && row.State == pr.StateOpen && !row.IsDraft && containsFold(row.ReviewRequests, p.in.Viewer)
}

func (p *Planner) first(ref pr.Ref, r *Result) {
	if !p.Requested(ref) {
		return
	}
	f, why := p.prOf(ref, p.follows(ref), r)
	if why != "" {
		r.first.reason = "my review was requested, but " + why
		return
	}
	no := func(reason, unblock string) {
		r.first.reason = reason
		r.first.settle = firstSettle{set: !r.stale, unblock: unblock}
	}
	if _, found := p.reviewWorkspace(f.Facts); found {
		no("its review workspace exists", "its review workspace exists")
		r.first.settle.clear = r.first.settle.set
		return
	}
	if w, found := p.authorWorkspace(f.Facts, strings.EqualFold(f.Author, p.in.Viewer)); found {
		no("you develop it in "+w.DisplayName, "you develop it in "+w.DisplayName)
		return
	}
	if why := change.Untrusted(f.Facts, p.owners, p.cfg.For(ref.Owner, ref.Repo).TrustedAuthors); why != "" {
		no(why, why)
		return
	}
	if p.firstSkipped(ref) {
		no("you skipped its first round", "you skipped its first round")
		return
	}
	waits := p.in.Rounds[ref].Paused
	why, quiet, err := p.firstRoundBlocked(f.Facts)
	switch {
	case err != nil:
		r.first.reason = "checking its head branch for a first round: " + err.Error()
		return
	case quiet:
		no("my review was requested, but "+why, "its head is the default branch")
	case why != "":
		r.first.reason = "my review was requested, but " + why
		r.first.settle = firstSettle{set: !r.stale, block: why}
	default:
		r.first.due, r.first.reason = true, "my review was requested"
		if b, ok := p.block(ref); !ok || !StartBlock(b) {
			r.first.settle = firstSettle{set: !r.stale, unblock: "the branch is free"}
		}
	}
	if waits {
		r.first.settle = firstSettle{}
	}
}

// block is ref's round_blocked event that still holds.
func (p *Planner) block(ref pr.Ref) (pr.Event, bool) {
	return ActiveBlock(p.in.Events[ref], p.in.Viewer)
}

func (p *Planner) address(ref pr.Ref, r *Result) {
	if ws, ok := p.t.author[ref]; ok {
		r.address.target, r.address.ws = true, ws
		if since, ok := conflictingSince(p.byRef[ref]); ok {
			r.dueAt("conflict", since.Add(p.cfg.conflictWindow()))
		}
		f, why := p.prOf(ref, true, r)
		if why != "" {
			r.address.reason = why
			return
		}
		p.addressStep(ws, f, r)
		return
	}
	row, ok := p.byRef[ref]
	if !p.cfg.AddressReviews || !ok || !strings.EqualFold(row.Author, p.in.Viewer) || row.State != pr.StateOpen ||
		!containsFold(p.owners, ref.Owner) || p.developed(row) {
		return
	}
	if p.firstSkipped(ref) {
		r.address.reason = "you removed its workspace"
		return
	}
	r.address.target = true
	f, why := p.prOf(ref, p.follows(ref), r)
	switch {
	case why != "":
		r.address.reason = why
		return
	case f.IsDraft:
		r.address.reason = "a draft without a workspace"
		return
	}
	p.addressStep(workspace{}, f, r)
	if r.address.due && r.address.fingerprint == "" {
		r.address.due, r.address.reason = false, "failing checks or a conflict alone start no round without a workspace"
		return
	}
	r.address.observed = r.address.due && !p.cfg.For(ref.Owner, ref.Repo).AddressWithoutWorkspace
}

// developed says whether a workspace on any host is linked to row or sits
// on its head branch.
func (p *Planner) developed(row pr.PR) bool {
	return slices.ContainsFunc(p.ws, func(w workspace) bool {
		return w.of(row.Ref) && ((!w.IsMain && w.linked == row.Ref.Number) || (row.HeadBranch != "" && w.branch() == row.HeadBranch))
	})
}

// addressStep decides a triage round when others reviewed a pull request I
// develop since the last one, its checks failed on a new head, or a new
// head has conflicted with the base past the conflict window.
func (p *Planner) addressStep(w workspace, f facts, r *Result) {
	if f.State != pr.StateOpen || (w.Status != "" && slices.Contains(p.cfg.Completed, w.Status)) {
		r.address.reason = "nothing to address"
		return
	}
	var who, failed []string
	var fp string
	if p.cfg.AddressReviews {
		var handled time.Time
		if prev := p.mark(f.Ref, MarkAddressedReviews); prev != "" {
			handled, _ = time.Parse(time.RFC3339Nano, prev)
		}
		who, fp = change.ReviewsToAddress(change.AddressInput{PR: f.Facts, Viewer: p.in.Viewer, Handled: handled, SkipReviewers: p.cfg.AddressSkipReviewers})
	}
	if p.cfg.AddressChecks {
		failed = change.FailingChecks(f.Facts, p.mark(f.Ref, MarkAddressedChecks), p.cfg.IgnoreChecks)
	}
	var reasons []string
	if len(who) > 0 {
		reasons = append(reasons, "reviewed by "+strings.Join(who, ", "))
	}
	if len(failed) > 0 {
		reasons = append(reasons, "failing checks: "+strings.Join(failed, ", "))
		r.address.checksHead = f.HeadRefOid
	}
	if p.cfg.AddressConflicts && p.conflicts(f) && !handled(p.mark(f.Ref, MarkAddressedConflict), f.HeadRefOid) {
		reasons = append(reasons, "conflicts with "+f.BaseRefName)
		r.address.conflictHead = f.HeadRefOid
	}
	if len(reasons) == 0 {
		r.address.reason = "nothing to address"
		return
	}
	r.address.due, r.address.reason, r.address.reviewers, r.address.checks, r.address.fingerprint = true, strings.Join(reasons, "; "), who, failed, fp
}

// conflicts says whether f's head conflicts with its base and the pull
// request has for the conflict window. The row keeps a conflict through
// GitHub's UNKNOWN after a push, so the head's own read must confirm it.
func (p *Planner) conflicts(f facts) bool {
	since, ok := conflictingSince(p.byRef[f.Ref])
	return ok && f.Mergeable == "CONFLICTING" && p.in.Now.Sub(since) >= p.cfg.conflictWindow()
}

// conflictingSince is when the row was first seen CONFLICTING, and whether
// it still is.
func conflictingSince(row pr.PR) (time.Time, bool) {
	return row.MergeableAt, row.Mergeable == "CONFLICTING" && !row.MergeableAt.IsZero()
}

// reviewWorkspace finds ref's review workspace by name or by linked pull
// request, among the local checkouts of its repository.
func (p *Planner) reviewWorkspace(f pr.Facts) (workspace, bool) {
	for _, w := range p.ws {
		if !w.of(f.Ref) || !w.Local || w.IsMain {
			continue
		}
		if w.DisplayName == p.cfg.ReviewPrefix+f.HeadRefName || (w.linked == f.Number && strings.HasPrefix(w.DisplayName, p.cfg.ReviewPrefix)) {
			return w, true
		}
	}
	return workspace{}, false
}

// authorWorkspace finds a workspace outside the review prefix linked to
// the pull request or on its head branch. A main checkout counts only for
// a pull request of mine from the same repository whose head it has checked
// out; a worktree wins over it.
func (p *Planner) authorWorkspace(f pr.Facts, mine bool) (workspace, bool) {
	var main workspace
	for _, w := range p.ws {
		if !w.of(f.Ref) || !w.Local || w.mainOnDefault() || (!w.IsMain && strings.HasPrefix(w.DisplayName, p.cfg.ReviewPrefix)) {
			continue
		}
		onHead := f.HeadRefName != "" && w.branch() == f.HeadRefName
		switch {
		case w.IsMain:
			if onHead && !f.IsCrossRepository {
				main = w
			}
		case w.linked == f.Number || onHead:
			return w, true
		}
	}
	if main.ID == "" || !mine {
		return workspace{}, false
	}
	return main, true
}

// firstRoundBlocked says why a first round for f cannot create its review
// workspace; quiet is set for a release pull request whose head is the
// default branch the main checkout sits on.
func (p *Planner) firstRoundBlocked(f pr.Facts) (why string, quiet bool, err error) {
	if w, ok := p.branchHolder(f); ok {
		return f.HeadRefName + " is checked out in " + w.Path, w.mainOnDefault(), nil
	}
	dir := repoPath(p.in.Listing.Repos, f.Ref)
	if dir == "" || p.in.Git == nil {
		return "", false, nil
	}
	return p.localBranchProblem(dir, f)
}

// branchHolder is the local checkout that has the head branch of a pull
// request from the same repository checked out.
func (p *Planner) branchHolder(f pr.Facts) (workspace, bool) {
	if f.IsCrossRepository || f.HeadRefName == "" {
		return workspace{}, false
	}
	for _, w := range p.ws {
		if w.of(f.Ref) && w.Local && w.Branch == f.HeadRefName {
			return w, true
		}
	}
	return workspace{}, false
}

// localBranchProblem says why the workspace manager would not reuse the
// pull request's local head branch in dir, before the head is fetched.
func (p *Planner) localBranchProblem(dir string, f pr.Facts) (string, bool, error) {
	git := p.in.Git
	branch, head := f.HeadRefName, f.HeadRefOid
	if f.IsCrossRepository || branch == "" || head == "" {
		return "", false, nil
	}
	tip, err := git.LocalBranchTip(p.ctx, dir, branch)
	if err != nil || tip == "" {
		return "", false, err
	}
	if path, err := git.CheckedOutAt(p.ctx, dir, branch); err != nil || path != "" {
		if path != "" {
			return branch + " is checked out in " + path, false, err
		}
		return "", false, err
	}
	if tip == head {
		return "", false, nil
	}
	if known, err := git.HasCommit(p.ctx, dir, head); err != nil || !known {
		return "", false, err
	}
	behind, err := git.IsAncestor(p.ctx, dir, tip, head)
	switch {
	case err != nil:
		return "", false, err
	case !behind:
		return fmt.Sprintf("local branch %s has commits not on the PR: push them or `git branch -D %s`", branch, branch), false, nil
	}
	return "", false, nil
}

// LastPublished is the head and time of the newest verdict a turn of role
// published in b: the head its last record covered, else the turn's.
func LastPublished(b src.Book, role string) (head string, at time.Time, ok bool) {
	for i := len(b.Turns) - 1; i >= 0; i-- {
		t := b.Turns[i]
		if t.Role != role || t.State == turnWithdrawn {
			continue
		}
		rec, found := lastRecord(b, t.Seq)
		if !found || !published(rec) {
			continue
		}
		return cmp.Or(rec.Head, t.Head), rec.At, true
	}
	return "", time.Time{}, false
}

// The turn states the planner reads, as the host names them.
const (
	turnWithdrawn = "withdrawn"
	recordDone    = "done"
)

func lastRecord(b src.Book, seq int64) (src.TurnRecord, bool) {
	for i := len(b.Records) - 1; i >= 0; i-- {
		if b.Records[i].TurnSeq == seq {
			return b.Records[i], true
		}
	}
	return src.TurnRecord{}, false
}

// published says the record is a done one that published a verdict.
func published(rec src.TurnRecord) bool {
	var r struct {
		State   string `json:"state"`
		Outcome *struct {
			Verdict string `json:"verdict"`
		} `json:"outcome"`
	}
	if err := json.Unmarshal(rec.Record, &r); err != nil {
		return false
	}
	return r.State == recordDone && r.Outcome != nil && r.Outcome.Verdict != ""
}

// NewestReview is when b's newest review turn was sent.
func NewestReview(b src.Book) (time.Time, bool) {
	var at time.Time
	found := false
	for _, t := range b.Turns {
		if t.Role == roleReview && t.State != turnWithdrawn {
			found = true
			if t.SentAt.After(at) {
				at = t.SentAt
			}
		}
	}
	return at, found
}

// handled says a mark that names a head covers head; an empty head is
// handled.
func handled(stored, head string) bool { return head == "" || head == stored }

func containsFold(list []string, v string) bool {
	return slices.ContainsFunc(list, func(s string) bool { return strings.EqualFold(s, v) })
}
