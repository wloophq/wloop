package factsgithub

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/sweep"
	dchange "github.com/wloophq/wloop/internal/domain/change"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/mark"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workday"
	"github.com/wloophq/wloop/internal/domain/workflow"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
	"github.com/wloophq/wloop/internal/situation"
)

// authorLookback is how long after it ended a pull request of mine still
// matches its workspace by branch.
const authorLookback = 14 * 24 * time.Hour

const (
	actionFollowup = "follow-up"
	actionComplete = "complete"

	whyNotStored = "not in the inventory yet"
	whyLapsed    = "nothing follows it in the inventory"
	whyPartial   = "the workspace listing is incomplete, no rounds this sweep"
)

var errNoViewer = errors.New("the GitHub login is not stored yet; the fast job stores it")

// result is what the review, first-round and address phases decide on one
// pull request; turn.start's guards filter it further.
type result struct {
	stale   bool
	review  reviewResult
	first   firstResult
	address addressResult
	// dues are the instants a decision changes with no input changing, by kind.
	dues map[string]time.Time
}

type reviewResult struct {
	target       bool
	ws           wsdomain.Workspace
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
	// settle is what Settle records of the first round; set says it
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
	ws                      wsdomain.Workspace
	due                     bool
	reason                  string
	reviewers, checks       []string
	fingerprint, checksHead string
	// conflictHead is the head whose lasting conflict with the base the
	// round resolves.
	conflictHead string
	observed     bool
}

// runs says whether a round of r would run without the user: due and not
// only observed.
func (r result) runs() bool {
	return (r.review.action == actionFollowup && !r.review.observed) || r.first.due || (r.address.due && !r.address.observed)
}

func (r *result) dueAt(kind string, t time.Time) {
	r.dues[kind] = earliestOf(r.dues[kind], t)
}

// rank orders units as the base's phases planned their rounds: follow-ups
// by their review's time, first rounds by the pull request's age, address
// rounds by their workspace's place and then among my pull requests, then
// the units no round is due on.
type rank struct {
	path int
	at   time.Time
	idx  int
	unit unit.Unit
}

func (a rank) compare(b rank) int {
	if c := cmp.Compare(a.path, b.path); c != 0 {
		return c
	}
	if c := a.at.Compare(b.at); c != 0 {
		return c
	}
	if c := cmp.Compare(a.idx, b.idx); c != 0 {
		return c
	}
	return strings.Compare(a.unit.Key, b.unit.Key)
}

func (pl *planner) rank(ref pr.Ref, r result) rank {
	k := rank{path: 3, unit: ref.Unit()}
	switch {
	case r.review.action == actionFollowup:
		k.path, k.at = 0, r.review.lastReviewAt
	case r.first.due:
		k.path, k.at = 1, pl.v.byRef[ref].CreatedAt
	case r.address.due:
		k.path, k.idx = 2, slices.Index(pl.targets.authorOrder, ref)
		if k.idx < 0 {
			k.idx = len(pl.targets.authorOrder) + slices.IndexFunc(pl.v.rows, func(p dinv.PR) bool { return p.Ref == ref })
		}
	}
	return k
}

// planner derives results at one instant.
type planner struct {
	ctx       context.Context
	v         *view
	now       time.Time
	cfg       config.Config
	owners    []string
	completed string
	targets   *targets
}

func (v *view) planner(ctx context.Context, now time.Time) *planner {
	return &planner{ctx: ctx, v: v, now: now, cfg: v.cfg, owners: v.owners(),
		completed: v.cfg.Column(v.p.d.Sink, config.ColumnCompleted), targets: v.targetsOf(ctx)}
}

// owners are the GitHub owners automated rounds may run for: the configured
// ones, or those of every repository the workspace manager knows.
func (v *view) owners() []string {
	owners := v.cfg.GitHub.Owners
	if len(owners) == 0 {
		for _, r := range v.repos {
			if owner, _, ok := r.OwnerName(); ok && !containsFold(owners, owner) {
				owners = append(owners, owner)
			}
		}
	}
	return owners
}

func (pl *planner) derive(ref pr.Ref) (result, error) {
	r := result{dues: map[string]time.Time{}}
	if row, ok := pl.v.byRef[ref]; ok && row.State == dgh.StateOpen {
		f, read := pl.v.factsOf(ref)
		r.stale = !read || pl.now.Sub(f.ReadAt) >= pl.maxAge()
	}
	if pl.v.viewer == "" {
		r.review.reason, r.first.reason, r.address.reason = errNoViewer.Error(), errNoViewer.Error(), errNoViewer.Error()
		return r, nil
	}
	if err := pl.review(ref, &r); err != nil {
		return r, err
	}
	pl.first(ref, &r)
	pl.address(ref, &r)
	if pl.v.partial {
		if r.review.action == actionFollowup {
			r.review.action, r.review.reason = "", whyPartial
		}
		if r.first.due {
			r.first.due, r.first.reason = false, whyPartial
		}
		if r.address.due {
			r.address.due, r.address.reason = false, whyPartial
		}
	}
	return r, nil
}

// prOf is ref's stored facts, or why the plan cannot decide on it; follows
// says something the plan lists follows it. A stale detail is decided on:
// turn.start refuses it.
func (pl *planner) prOf(ref pr.Ref, follows bool, r *result) (sweep.Facts, string) {
	row, ok := pl.v.byRef[ref]
	if !ok {
		return sweep.Facts{}, whyNotStored
	}
	f, read := pl.v.factsOf(ref)
	if row.State == dgh.StateOpen && row.Lapsed() && !follows {
		return sweep.Facts{}, whyLapsed
	}
	if r.stale {
		r.dueAt("unread", pl.now.Add(time.Duration(pl.cfg.Sweep.Fast)))
	}
	if !read {
		return sweep.Facts{}, "not read in full yet"
	}
	return f, ""
}

// maxAge is the oldest detail a plan decides on: the collector re-reads a
// followed pull request's detail once it is older than the slow interval.
func (pl *planner) maxAge() time.Duration {
	return time.Duration(pl.cfg.Sweep.Slow) + 2*time.Duration(pl.cfg.Sweep.Fast)
}

func (pl *planner) review(ref pr.Ref, r *result) error {
	ws, linked := pl.targets.review[ref]
	if !linked {
		ok, why, err := pl.reviewWithoutWorkspace(ref, r)
		if err != nil || !ok {
			r.review.reason = why
			return err
		}
	}
	r.review.target, r.review.ws = true, ws
	f, why := pl.prOf(ref, true, r)
	if why != "" {
		r.review.reason = why
		return nil
	}
	if ws.ID == "" {
		if why := pl.notFollowedWithoutWorkspace(f.Facts); why != "" {
			r.review.reason = why
			return nil
		}
	}
	d := dchange.Reviewer(dchange.ReviewerInput{
		PR:              f.Facts,
		Viewer:          pl.v.viewer,
		WorkspaceStatus: ws.Status,
		CompletedStatus: pl.completed,
		TrustedOwners:   pl.owners,
		TrustedAuthors:  pl.cfg.For(ref.Owner, ref.Repo).GitHub.TrustedAuthors,
		LastFingerprint: pl.v.rounds.Mark(ref.String(), round.Reviewed),
		Recorded:        pl.recorded(ref),
	})
	r.review.reason, r.review.settle = d.Reason, !r.stale
	switch d.Action {
	case dchange.ReviewerNothing:
		r.review.upgrade = d.Fingerprint
	case dchange.ReviewerComplete:
		r.review.action = actionComplete
	case dchange.ReviewerFollowup:
		r.review.action, r.review.fingerprint, r.review.lastReviewAt = actionFollowup, d.Fingerprint, d.LastReviewAt
		r.review.observed = ws.ID == "" && !pl.cfg.For(ref.Owner, ref.Repo).GitHub.FollowupsWithoutWorkspace
	}
	return nil
}

// recorded is the verdict the newest review turn of ref published.
func (pl *planner) recorded(ref pr.Ref) dchange.RecordedReview {
	b, ok := pl.v.books[ref.Unit()]
	if !ok {
		return dchange.RecordedReview{}
	}
	if v, ok := b.LastRoleVerdict(builtin.RoleReview); ok {
		return dchange.RecordedReview{Head: v.Head, At: v.At}
	}
	return dchange.RecordedReview{}
}

// reviewWithoutWorkspace says whether ref is followed up without a review
// workspace: a recent review turn no review workspace links, but those I
// removed or took over, or why not.
func (pl *planner) reviewWithoutWorkspace(ref pr.Ref, r *result) (bool, string, error) {
	newest, ok := newestReview(pl.v.books[ref.Unit()])
	if !ok || pl.targets.reviewLinked[strings.ToLower(ref.String())] {
		return false, "", nil
	}
	stored, found := pl.v.byRef[ref]
	ended, err := pl.v.reviewEnded(pl.ctx, ref)
	if err != nil {
		return false, "", err
	}
	switch {
	case ended && (!found || stored.State != dgh.StateOpen):
		return false, "its review ended", nil
	case pl.now.Sub(newest) >= sweep.ReviewTurnsFollowed:
		return false, fmt.Sprintf("its newest review turn is older than %s", sweep.ReviewTurnsFollowed), nil
	case pl.v.rounds.FirstRoundSkipped(ref.String()):
		return false, "you removed it", nil
	case pl.v.overrides[ref].Adopted:
		return false, "you took it over", nil
	case found && stored.State != "" && stored.State != dgh.StateOpen:
		return false, "it is " + strings.ToLower(stored.State), nil
	}
	r.dueAt("review cutoff", newest.Add(sweep.ReviewTurnsFollowed))
	return true, "", nil
}

// newestReview is when b's newest review turn was sent.
func newestReview(b turn.Book) (time.Time, bool) {
	var at time.Time
	found := false
	for _, t := range b.Turns {
		if t.Role == builtin.RoleReview && t.State != turn.Withdrawn {
			found = true
			if t.SentAt.After(at) {
				at = t.SentAt
			}
		}
	}
	return at, found
}

// reviewEnded says whether ref has a review_ended event; read once per view
// for every pull request with a review turn.
func (v *view) reviewEnded(ctx context.Context, ref pr.Ref) (bool, error) {
	v.mu.Lock()
	ended := v.ended
	v.mu.Unlock()
	if ended == nil {
		var units []unit.Unit
		for u, b := range v.books {
			if _, ok := newestReview(b); ok && u.Provider == unit.GitHub {
				units = append(units, u)
			}
		}
		ended = map[unit.Unit]bool{}
		if len(units) > 0 {
			got, err := v.p.d.Inputs.Ended(ctx, dinv.KindReviewEnded, units)
			if err != nil {
				return false, err
			}
			ended = got
		}
		v.mu.Lock()
		v.ended = ended
		v.mu.Unlock()
	}
	return ended[ref.Unit()], nil
}

// notFollowedWithoutWorkspace says why a pull request whose review workspace
// is gone gets no follow-up, or "".
func (pl *planner) notFollowedWithoutWorkspace(f pr.Facts) string {
	viewer := pl.v.viewer
	switch {
	case f.State != dgh.StateOpen:
		return "it is " + strings.ToLower(f.State)
	case strings.EqualFold(f.Author, viewer):
		return "you opened it"
	case containsFold(f.ReviewRequests, viewer):
		return "my review is requested, which starts a first round"
	}
	for _, w := range pl.v.workspaces {
		if _, ofRepo := reposOf(pl.v.repos, f.Ref)[w.RepoID]; ofRepo && pl.isReviewWorkspace(w) &&
			(w.DisplayName == pl.cfg.GitHub.ReviewPrefix+f.HeadRefName || w.LinkedPR == f.Number) {
			return "its review workspace is " + w.DisplayName
		}
	}
	mine := strings.EqualFold(f.Author, viewer)
	if w, found := findAuthorWorkspace(pl.cfg, pl.v.repos, pl.v.workspaces, f, mine); found {
		return "you develop it in " + w.DisplayName
	}
	return ""
}

func (pl *planner) isReviewWorkspace(w wsdomain.Workspace) bool {
	return !w.IsMain && strings.HasPrefix(w.DisplayName, pl.cfg.GitHub.ReviewPrefix)
}

// follows says whether a plan phase that runs before the first rounds lists
// ref, so a lapsed row of it is still read.
func (pl *planner) follows(ref pr.Ref) bool {
	if _, ok := pl.targets.review[ref]; ok {
		return true
	}
	ok, _, _ := pl.reviewWithoutWorkspace(ref, &result{dues: map[string]time.Time{}})
	return ok
}

func (pl *planner) first(ref pr.Ref, r *result) {
	row, ok := pl.v.byRef[ref]
	if !ok || row.State != dgh.StateOpen || row.IsDraft || !containsFold(row.ReviewRequests, pl.v.viewer) {
		return
	}
	f, why := pl.prOf(ref, pl.follows(ref), r)
	if why != "" {
		r.first.reason = "my review was requested, but " + why
		return
	}
	no := func(reason, unblock string) {
		r.first.reason = reason
		r.first.settle = firstSettle{set: !r.stale, unblock: unblock}
	}
	if _, found := findReviewWorkspace(pl.cfg, pl.v.repos, pl.v.workspaces, f.Ref.RemoteKey(), f.Number, f.HeadRefName); found {
		no("its review workspace exists", "its review workspace exists")
		r.first.settle.clear = r.first.settle.set
		return
	}
	if w, found := findAuthorWorkspace(pl.cfg, pl.v.repos, pl.v.workspaces, f.Facts, strings.EqualFold(f.Author, pl.v.viewer)); found {
		no("you develop it in "+w.DisplayName, "you develop it in "+w.DisplayName)
		return
	}
	if why := dchange.Untrusted(f.Facts, pl.owners, pl.cfg.For(ref.Owner, ref.Repo).GitHub.TrustedAuthors); why != "" {
		no(why, why)
		return
	}
	if pl.v.rounds.FirstRoundSkipped(ref.String()) {
		no("you skipped its first round", "you skipped its first round")
		return
	}
	waits := pl.v.overrides[ref].Hold || pl.v.pause.Holds(ref.Owner, ref.Repo)
	why, quiet, err := pl.firstRoundBlocked(f.Facts)
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
		if _, ok := startBlock(pl.v.blocks[ref]); !ok {
			r.first.settle = firstSettle{set: !r.stale, unblock: "the branch is free"}
		}
	}
	if waits {
		r.first.settle = firstSettle{}
	}
}

func (pl *planner) address(ref pr.Ref, r *result) {
	if ws, ok := pl.targets.author[ref]; ok {
		r.address.target, r.address.ws = true, ws
		if since, ok := pl.v.byRef[ref].ConflictingSince(); ok {
			r.dueAt("conflict", since.Add(pl.v.conflictWindow()))
		}
		f, why := pl.prOf(ref, true, r)
		if why != "" {
			r.address.reason = why
			return
		}
		pl.addressStep(ws, f, r)
		return
	}
	row, ok := pl.v.byRef[ref]
	if !pl.cfg.GitHub.AddressReviews || !ok || !strings.EqualFold(row.Author, pl.v.viewer) || row.State != dgh.StateOpen ||
		!containsFold(pl.owners, ref.Owner) || pl.developed(row) {
		return
	}
	if pl.v.rounds.FirstRoundSkipped(ref.String()) {
		r.address.reason = "you removed its workspace"
		return
	}
	r.address.target = true
	f, why := pl.prOf(ref, pl.follows(ref), r)
	switch {
	case why != "":
		r.address.reason = why
		return
	case f.IsDraft:
		r.address.reason = "a draft without a workspace"
		return
	}
	pl.addressStep(wsdomain.Workspace{}, f, r)
	if r.address.due && r.address.fingerprint == "" {
		r.address.due, r.address.reason = false, "failing checks or a conflict alone start no round without a workspace"
		return
	}
	r.address.observed = r.address.due && !pl.cfg.For(ref.Owner, ref.Repo).GitHub.AddressWithoutWorkspace
}

// developed says whether a workspace on any host is linked to p or sits on
// its head branch.
func (pl *planner) developed(p dinv.PR) bool {
	ofRepo := reposOf(pl.v.repos, p.Ref)
	return slices.ContainsFunc(pl.v.workspaces, func(w wsdomain.Workspace) bool {
		_, ok := ofRepo[w.RepoID]
		return ok && ((!w.IsMain && w.LinkedPR == p.Ref.Number) || (p.HeadBranch != "" && w.PRBranch() == p.HeadBranch))
	})
}

// addressStep decides a triage round when others reviewed a pull request I
// develop since the last one, its checks failed on a new head, or a new head
// has conflicted with the base past the conflict window.
func (pl *planner) addressStep(w wsdomain.Workspace, f sweep.Facts, r *result) {
	if f.State != dgh.StateOpen || (pl.completed != "" && w.Status == pl.completed) {
		r.address.reason = "nothing to address"
		return
	}
	key := f.Ref.String()
	var who, failed []string
	var fp string
	if pl.cfg.GitHub.AddressReviews {
		var handled time.Time
		if prev := pl.v.rounds.Mark(key, round.AddressedReviews); prev != "" {
			handled, _ = time.Parse(time.RFC3339Nano, prev)
		}
		who, fp = dchange.ReviewsToAddress(dchange.AddressInput{PR: f.Facts, Viewer: pl.v.viewer, Handled: handled, SkipReviewers: pl.cfg.GitHub.AddressSkipReviewers})
	}
	if pl.cfg.GitHub.AddressChecks {
		failed = dchange.FailingChecks(f.Facts, pl.v.rounds.Mark(key, round.AddressedChecks), pl.cfg.GitHub.IgnoreChecks)
	}
	var reasons []string
	if len(who) > 0 {
		reasons = append(reasons, "reviewed by "+strings.Join(who, ", "))
	}
	if len(failed) > 0 {
		reasons = append(reasons, "failing checks: "+strings.Join(failed, ", "))
		r.address.checksHead = f.HeadRefOid
	}
	if pl.cfg.GitHub.AddressConflicts && pl.conflicts(f) && !mark.Handled(pl.v.rounds.Mark(key, round.AddressedConflict), f.HeadRefOid) {
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
func (pl *planner) conflicts(f sweep.Facts) bool {
	since, ok := pl.v.byRef[f.Ref].ConflictingSince()
	return ok && f.Mergeable == "CONFLICTING" && pl.now.Sub(since) >= pl.v.conflictWindow()
}

// facts are ref's pr facts from r at now.
func (pl *planner) facts(ref pr.Ref, r result) workflow.Facts {
	v, now := pl.v, pl.now
	f := workflow.Facts{"pr.repo": ref.Owner + "/" + ref.Repo, "pr.repo_name": ref.Repo, "pr.owner": ref.Owner, "pr.author": "", "pr.visibility": "", "pr.ended": false}
	if pf, read := v.factsOf(ref); read {
		f["pr.visibility"] = pf.Visibility
	}
	row, ok := v.byRef[ref]
	if ok {
		o := v.overrides[ref]
		f["pr.state"] = row.State
		f["pr.ended"] = row.State == dgh.StateMerged || row.State == dgh.StateClosed
		if !row.MergedAt.IsZero() {
			f["pr.merged_at"] = row.MergedAt
		}
		if !row.ClosedAt.IsZero() {
			f["pr.closed_at"] = row.ClosedAt
		}
		f["pr.finished_for"] = finishedFor(row, now)
		f["pr.draft"] = row.IsDraft
		f["pr.head"] = row.HeadOid
		f["pr.conflicting_for"] = time.Duration(0)
		if since, ok := row.ConflictingSince(); ok {
			f["pr.conflicting_for"] = now.Sub(since)
		}
		f["pr.author"] = row.Author
		f["pr.mine"] = v.viewer != "" && strings.EqualFold(row.Author, v.viewer)
		f["pr.checks"] = strings.ToLower(row.ChecksState)
		if !row.CreatedAt.IsZero() {
			f["pr.age"] = now.Sub(row.CreatedAt)
		}
		f["pr.held"], f["pr.adopted"] = o.Hold, o.Adopted
		f["pr.muted"] = o.MuteUntilStateChange || o.MutedUntil.After(now)
		f["pr.mute.refusal"] = o.MuteRefusal(now)
		f["pr.stale"] = r.stale
		evs := v.eventsOf(ref)
		if at := lastInteraction(row, evs); !at.IsZero() {
			f["pr.last_interaction_at"] = at
			f["pr.silent_days"] = calendarDays(at, now)
		}
		sit, refusal := v.situation(row, evs, o, now)
		f["pr.stalled"] = sit.Stalled
		f["pr.waits_on_user"] = sit.WaitingOn == "me" || (sit.WaitingOn == "author" && strings.EqualFold(row.Author, v.viewer) && v.viewer != "")
		f["pr.nudge.refusal"] = refusal
	}
	_, hasReview := pl.targets.review[ref]
	_, hasAuthor := pl.targets.author[ref]
	f["pr.has_workspace"] = hasReview || hasAuthor
	b := v.books[ref.Unit()]
	f["pr.open_turn"], f["pr.open_turn_seq"] = "", 0
	if t, ok := openTurn(b); ok {
		f["pr.open_turn"], f["pr.open_turn_seq"] = string(t.State), int(t.Seq)
	}
	f["pr.orphan_answer_id"] = orphanAnswer(b)
	f["pr.review.action"], f["pr.review.reason"], f["pr.review.observed"] = r.review.action, r.review.reason, r.review.observed
	f["pr.first.due"], f["pr.first.reason"] = r.first.due, r.first.reason
	f["pr.address.due"], f["pr.address.reason"], f["pr.address.observed"] = r.address.due, r.address.reason, r.address.observed
	f["pr.address.reviewers"], f["pr.address.checks"] = nonNil(r.address.reviewers), nonNil(r.address.checks)
	f["pr.address.conflict"] = r.address.due && r.address.conflictHead != ""
	f["pr.fingerprint"] = fingerprint(r, row)
	return f
}

// fingerprint is what the round rules handled until something new happens:
// each due round's own fingerprint, so a run or a dismissal of one does not
// hold back the next.
func fingerprint(r result, row dinv.PR) string {
	var parts []string
	if r.review.action == actionFollowup {
		parts = append(parts, "review:"+r.review.fingerprint)
	}
	if r.first.due {
		parts = append(parts, "first:"+row.HeadOid)
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

// finishedFor is the working time since the pull request ended, its newer
// of merged and closed; zero while open.
func finishedFor(row dinv.PR, now time.Time) time.Duration {
	if row.State == dgh.StateOpen {
		return 0
	}
	ended := row.MergedAt
	if row.ClosedAt.After(ended) {
		ended = row.ClosedAt
	}
	if ended.IsZero() {
		return 0
	}
	return workday.Between(ended, now)
}

// interactions are the events by anyone a nudge's silence counts from.
var interactions = []string{dinv.KindComment, dinv.KindReview, dinv.KindPush, dinv.KindReviewRequest}

// lastInteraction is the newest comment, review, push or review request on
// the pull request, by anyone; its opening without one.
func lastInteraction(row dinv.PR, evs []dinv.Event) time.Time {
	var at time.Time
	for _, e := range evs {
		if slices.Contains(interactions, e.Kind) && e.At.After(at) {
			at = e.At
		}
	}
	if at.IsZero() {
		return row.CreatedAt
	}
	return at
}

// calendarDays counts the local midnights between from and to.
func calendarDays(from, to time.Time) int {
	y1, m1, d1 := from.Local().Date()
	y2, m2, d2 := to.Local().Date()
	a := time.Date(y1, m1, d1, 12, 0, 0, 0, time.UTC)
	b := time.Date(y2, m2, d2, 12, 0, 0, 0, time.UTC)
	return int(b.Sub(a).Hours() / 24)
}

// situation is the row's situation without a pending round, and the text a
// nudge would be refused with now.
func (v *view) situation(row dinv.PR, evs []dinv.Event, o dinv.Override, now time.Time) (situation.Result, string) {
	in := situation.Input{PR: row, Events: evs, Override: o, Viewer: v.viewer, MyTeams: v.teams, Ignore: prrow.IgnoreOf(v.cfg), Now: now, Stalled: v.cfg.GitHub.Stalled}
	if b, ok := v.books[row.Ref.Unit()]; ok {
		if tf, ok := prrow.TurnFacts(map[unit.Unit]turn.Book{row.Ref.Unit(): b}, v.registry)[row.Ref]; ok {
			in.Turn = &tf.Facts
		}
	}
	res := situation.Derive(in)
	unmuted := res
	if res.Kind == situation.KindMuted {
		in.Override = dinv.Override{PR: row.Ref}
		unmuted = situation.Derive(in)
	}
	who, why := prrow.NudgeTargets(apiRow(unmuted), row.Author, v.viewer, prrow.IgnoreOf(v.cfg))
	return res, prrow.NudgeRefusal(row.Ref, who, why, unmuted.Since, evs, now)
}

// apiRow is the row situation.Derive's result gives, as nudge.targets reads it.
func apiRow(r situation.Result) api.Row {
	return api.Row{Kind: api.Kind(r.Kind), Summary: api.Summary{WaitsOn: r.WaitingOn, Reason: r.Reason, Since: r.Since}}
}

// orphanAnswer is the id of an answer still waiting whose turn ended; 0 when
// none.
func orphanAnswer(b turn.Book) int {
	for _, a := range b.Answers {
		if a.Route != turn.Queued && a.Route != turn.Pending {
			continue
		}
		if i := slices.IndexFunc(b.Turns, func(t turn.Turn) bool { return t.Seq == a.TurnSeq }); i >= 0 && !b.Turns[i].State.Open() {
			return int(a.ID)
		}
	}
	return 0
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func containsFold(list []string, v string) bool {
	return slices.ContainsFunc(list, func(s string) bool { return strings.EqualFold(s, v) })
}

func reposOf(repos []wsdomain.Repo, ref pr.Ref) map[string]wsdomain.Repo {
	remoteKey := ref.RemoteKey()
	byID := map[string]wsdomain.Repo{}
	for _, r := range repos {
		if strings.EqualFold(r.RemoteKey, remoteKey) {
			byID[r.ID] = r
		}
	}
	return byID
}

// findReviewWorkspace matches by name or by linked pull request, among the
// local checkouts of the pull request's repository.
func findReviewWorkspace(cfg config.Config, repos []wsdomain.Repo, all []wsdomain.Workspace, remoteKey string, number int, headRef string) (wsdomain.Workspace, bool) {
	ofRepo := map[string]bool{}
	for _, r := range repos {
		if strings.EqualFold(r.RemoteKey, remoteKey) {
			ofRepo[r.ID] = true
		}
	}
	for _, w := range all {
		if !ofRepo[w.RepoID] || w.HostID != wsdomain.LocalHost || w.IsMain {
			continue
		}
		if w.DisplayName == cfg.GitHub.ReviewPrefix+headRef || (w.LinkedPR == number && strings.HasPrefix(w.DisplayName, cfg.GitHub.ReviewPrefix)) {
			return w, true
		}
	}
	return wsdomain.Workspace{}, false
}

// findAuthorWorkspace finds a workspace outside the review prefix linked to
// the pull request or on its head branch. A main checkout counts only for a
// pull request of mine from the same repository whose head it has checked
// out; a worktree wins over it.
func findAuthorWorkspace(cfg config.Config, repos []wsdomain.Repo, all []wsdomain.Workspace, f pr.Facts, mine bool) (wsdomain.Workspace, bool) {
	ofRepo := reposOf(repos, f.Ref)
	var main wsdomain.Workspace
	for _, w := range all {
		r, ok := ofRepo[w.RepoID]
		if !ok || w.HostID != wsdomain.LocalHost || wsdomain.MainOnDefault(w, r) || (!w.IsMain && strings.HasPrefix(w.DisplayName, cfg.GitHub.ReviewPrefix)) {
			continue
		}
		onHead := f.HeadRefName != "" && w.PRBranch() == f.HeadRefName
		switch {
		case w.IsMain:
			if onHead && !f.IsCrossRepository {
				main = w
			}
		case w.LinkedPR == f.Number || onHead:
			return w, true
		}
	}
	if main.ID == "" || !mine {
		return wsdomain.Workspace{}, false
	}
	return main, true
}

// firstRoundBlocked says why a first round for f cannot create its review
// workspace; quiet is set for a release pull request whose head is the
// default branch the main checkout sits on.
func (pl *planner) firstRoundBlocked(f pr.Facts) (why string, quiet bool, err error) {
	if w, r, ok := branchHolder(pl.v.repos, pl.v.workspaces, f); ok {
		return f.HeadRefName + " is checked out in " + w.Path, wsdomain.MainOnDefault(w, r), nil
	}
	found := wsdomain.FindRepos(pl.v.repos, f.Ref.RemoteKey(), "")
	if len(found) == 0 || pl.v.p.d.Branches == nil {
		return "", false, nil
	}
	return pl.localBranchProblem(found[0].Path, f)
}

// branchHolder is the local checkout that has the head branch of a pull
// request from the same repository checked out.
func branchHolder(repos []wsdomain.Repo, all []wsdomain.Workspace, f pr.Facts) (wsdomain.Workspace, wsdomain.Repo, bool) {
	if f.IsCrossRepository || f.HeadRefName == "" {
		return wsdomain.Workspace{}, wsdomain.Repo{}, false
	}
	ofRepo := reposOf(repos, f.Ref)
	for _, w := range all {
		if r, ok := ofRepo[w.RepoID]; ok && w.HostID == wsdomain.LocalHost && w.Branch == f.HeadRefName {
			return w, r, true
		}
	}
	return wsdomain.Workspace{}, wsdomain.Repo{}, false
}

// localBranchProblem says why the workspace manager would not reuse the
// pull request's local head branch in dir, before the head is fetched.
func (pl *planner) localBranchProblem(dir string, f pr.Facts) (string, bool, error) {
	git := pl.v.p.d.Branches
	branch, head := f.HeadRefName, f.HeadRefOid
	if f.IsCrossRepository || branch == "" || head == "" {
		return "", false, nil
	}
	tip, err := git.LocalBranchTip(pl.ctx, dir, branch)
	if err != nil || tip == "" {
		return "", false, err
	}
	if path, err := git.CheckedOutAt(pl.ctx, dir, branch); err != nil || path != "" {
		if path != "" {
			return branch + " is checked out in " + path, false, err
		}
		return "", false, err
	}
	if tip == head {
		return "", false, nil
	}
	if known, err := git.HasCommit(pl.ctx, dir, head); err != nil || !known {
		return "", false, err
	}
	behind, err := git.IsAncestor(pl.ctx, dir, tip, head)
	switch {
	case err != nil:
		return "", false, err
	case !behind:
		return fmt.Sprintf("local branch %s has commits not on the PR: push them or `git branch -D %s`", branch, branch), false, nil
	}
	return "", false, nil
}
