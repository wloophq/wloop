package row

import (
	"slices"
	"strings"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/workday"
)

// The link signals plugin.toml declares.
const (
	SignalStalled   = "stalled"
	SignalInLoop    = "in_loop"
	SignalReady     = "ready"
	SignalWorkspace = "workspace"
)

// Report is the row's snapshot but its unit, rev, time and data.
func (r Row) Report() src.Snapshot {
	s := src.Snapshot{Situation: r.Situation.Situation(), Card: r.Card(), Items: r.Items(), Facts: r.Facts(), Signals: r.Signals(),
		Views: slices.Clone(r.Views), Facets: map[string]string{}, FacetValues: map[string][]string{}}
	for id, vs := range r.Facets {
		if len(vs) == 1 {
			s.Facets[id] = vs[0]
		} else {
			s.FacetValues[id] = slices.Clone(vs)
		}
	}
	return s
}

// Signals are the row's link signals: stalled, an open pull request in
// scope whose situation lasted past its threshold; in_loop, an open one a
// review workspace holds; ready, one whose work is approved; workspace, one
// a workspace of which remains, open or not.
func (r Row) Signals() []src.Signal {
	open := r.PR.State == "OPEN"
	stalled := src.Signal{Name: SignalStalled, On: open && r.Stalled() && len(r.Payload.Reasons) > 0}
	if stalled.On {
		stalled.Since = r.stallsAt()
	}
	return []src.Signal{stalled, {Name: SignalInLoop, On: open && r.Payload.Workspace.IsReview}, {Name: SignalReady, On: r.Payload.Approved},
		{Name: SignalWorkspace, On: r.Payload.Workspace.ID != ""}}
}

// stallsAt is the first instant the row's situation is stalled; zero when
// it never stalls.
func (r Row) stallsAt() time.Time {
	v := r.Situation.Stalled
	if v.Kind != src.KindAfter || v.Time.IsZero() {
		return time.Time{}
	}
	return workday.Add(v.Time, v.Threshold+time.Nanosecond)
}

// Due is the first instant after the input's now at which the report
// changes with time alone: the situation stalls, a held round's day ends,
// or a day of silence passes; zero for none.
func (r Row) Due() time.Time {
	now := r.in.Now
	var due time.Time
	add := func(t time.Time) {
		if t.After(now) && (due.IsZero() || t.Before(due)) {
			due = t
		}
	}
	add(r.stallsAt())
	if r.PR.State == "OPEN" {
		add(nextLocalDay(now))
	}
	return due
}

func nextLocalDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d+1, 0, 0, 0, 0, t.Location())
}

// Facts are the row's workflow facts that read the pull request alone;
// the host adds those that read its own state.
func (r Row) Facts() []src.Fact {
	p, in := r.PR, r.in
	ref := p.Ref
	viewer := in.Viewer
	var out []src.Fact
	add := func(name string, v src.Value) { out = append(out, src.Fact{Name: name, Value: v}) }
	add("pr.repo", src.String(ref.Owner+"/"+ref.Repo))
	add("pr.repo_name", src.String(ref.Repo))
	add("pr.owner", src.String(ref.Owner))
	add("pr.slug", src.String(ref.String()))
	add("pr.author", src.String(p.Author))
	add("pr.state", src.String(p.State))
	add("pr.ended", src.Bool(p.State == "MERGED" || p.State == "CLOSED"))
	if !p.MergedAt.IsZero() {
		add("pr.merged_at", src.Time(p.MergedAt))
	}
	if !p.ClosedAt.IsZero() {
		add("pr.closed_at", src.Time(p.ClosedAt))
	}
	add("pr.draft", src.Bool(p.IsDraft))
	add("pr.head", src.String(p.HeadOid))
	if p.Mergeable == "CONFLICTING" && !p.MergeableAt.IsZero() {
		add("pr.conflicting_for", src.Since(p.MergeableAt))
	} else {
		add("pr.conflicting_for", src.Duration(0))
	}
	add("pr.mine", src.Bool(viewer != "" && strings.EqualFold(p.Author, viewer)))
	add("pr.base", src.String(p.BaseBranch))
	add("pr.cross_repository", src.Bool(p.IsFork))
	add("pr.requested_teams", src.List(requestedTeams(p.ReviewRequests)...))
	add("pr.review_requested_me", src.Bool(viewer != "" && slices.ContainsFunc(p.ReviewRequests, func(r string) bool { return strings.EqualFold(r, viewer) })))
	add("pr.checks", src.String(strings.ToLower(p.ChecksState)))
	if !p.CreatedAt.IsZero() {
		add("pr.age", src.Since(p.CreatedAt))
	}
	add("pr.adopted", src.Bool(in.Override.Adopted))
	add("pr.stale", r.stale())
	visibility, association, labels := "", "", []string{}
	exists, verdict, decision := false, "", ""
	if d := in.Detail; d != nil {
		visibility, association, labels = d.Visibility, d.AuthorAssociation, nonNil(d.Labels)
		exists, verdict, decision = lastReview(d.Reviews, viewer)
	}
	add("pr.visibility", src.String(visibility))
	add("pr.association", src.String(association))
	add("pr.labels", src.List(labels...))
	add("pr.last_review.exists", src.Bool(exists))
	add("pr.last_review.verdict", src.String(verdict))
	add("pr.last_review.decision", src.String(decision))
	at := lastInteraction(p, in.Events)
	if !at.IsZero() {
		add("pr.last_interaction_at", src.Time(at))
		add("pr.silent_days", src.Int(int64(calendarDays(at, in.Now, in.Location))))
	}
	return out
}

// stale says the stored detail of an open pull request is older than the
// plugin calls fresh, or missing.
func (r Row) stale() src.Value {
	if r.PR.State != "OPEN" {
		return src.Bool(false)
	}
	d := r.in.Detail
	if d == nil || d.ReadAt.IsZero() {
		return src.Bool(true)
	}
	return src.After(d.ReadAt, r.in.MaxDetailAge-time.Nanosecond, src.Calendar)
}

// requestedTeams are the teams of review requests, as <org>-<slug>.
func requestedTeams(requests []string) []string {
	var out []string
	for _, r := range requests {
		if t, ok := strings.CutPrefix(r, "team:"); ok {
			out = append(out, strings.Replace(t, "/", "-", 1))
		}
	}
	return out
}

// lastReview is whether the viewer reviewed the pull request, the state of
// their newest review, and their newest verdict that is not a comment.
func lastReview(reviews []pr.Review, viewer string) (exists bool, verdict, decision string) {
	var newest, decided time.Time
	for _, r := range reviews {
		if viewer == "" || !strings.EqualFold(bareLogin(r.Author), viewer) || r.SubmittedAt.IsZero() {
			continue
		}
		if r.SubmittedAt.After(newest) {
			verdict, newest = r.State, r.SubmittedAt
		}
		switch r.State {
		case pr.ReviewApproved, pr.ReviewChangesRequested, pr.ReviewCommented, pr.ReviewDismissed:
		default:
			continue
		}
		exists = true
		if r.State != pr.ReviewCommented && r.SubmittedAt.After(decided) {
			decision, decided = r.State, r.SubmittedAt
		}
	}
	return exists, verdict, decision
}

func bareLogin(login string) string { return strings.TrimSuffix(login, "[bot]") }

// interactions are the events by anyone a nudge's silence counts from.
var interactions = []string{pr.KindComment, pr.KindReview, pr.KindPush, pr.KindReviewRequest}

// lastInteraction is the newest comment, review, push or review request on
// the pull request, by anyone; its opening without one.
func lastInteraction(p pr.PR, evs []pr.Event) time.Time {
	var at time.Time
	for _, e := range evs {
		if slices.Contains(interactions, e.Kind) && e.At.After(at) {
			at = e.At
		}
	}
	if at.IsZero() {
		return p.CreatedAt
	}
	return at
}

// calendarDays counts the midnights of loc between from and to; nil counts UTC's.
func calendarDays(from, to time.Time, loc *time.Location) int {
	if loc == nil {
		loc = time.UTC
	}
	y1, m1, d1 := from.In(loc).Date()
	y2, m2, d2 := to.In(loc).Date()
	a := time.Date(y1, m1, d1, 12, 0, 0, 0, time.UTC)
	b := time.Date(y2, m2, d2, 12, 0, 0, 0, time.UTC)
	return int(b.Sub(a).Hours() / 24)
}
