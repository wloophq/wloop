package factsgithub_test

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	factsgithub "github.com/wloophq/wloop/internal/adapter/facts/github"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/sweep"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	dchange "github.com/wloophq/wloop/internal/domain/github/change"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/internal/readmodel/prrow/ghreporttest"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// t0 is a Wednesday afternoon.
var t0 = time.Date(2026, 10, 21, 15, 0, 0, 0, time.UTC)

const me = "me"

var repo = wsdomain.Repo{ID: "r1", Path: "/w/app", RemoteKey: "github.com/acme/app", DefaultBranch: "main"}

var complete = wsdomain.Scope{Hosts: []string{"local"}, Complete: true, RuntimeID: "rt"}

func ref(n int) pr.Ref { return pr.Ref{Owner: "acme", Repo: "app", Number: n} }

// world is every port the provider reads, over maps.
type world struct {
	mu        sync.Mutex
	now       time.Time
	cfg       config.Config
	viewer    string
	rows      map[pr.Ref]dinv.PR
	details   map[pr.Ref]dinv.PlanFacts
	ended     map[unit.Unit]bool
	overrides map[pr.Ref]dinv.Override
	blocks    map[pr.Ref]dinv.Event
	events    map[pr.Ref][]dinv.Event
	recorded  []dinv.Event
	books     map[unit.Unit]turn.Book
	rounds    *round.Store
	pause     round.Pause
	snap      *wsdomain.Snapshot
	git       *fakeGit
	branches  *fakeBranches
	// factReads counts the Facts queries.
	factReads int
}

func newWorld() *world {
	w := &world{now: t0, cfg: config.Defaults(), viewer: me, rows: map[pr.Ref]dinv.PR{}, details: map[pr.Ref]dinv.PlanFacts{},
		ended: map[unit.Unit]bool{}, overrides: map[pr.Ref]dinv.Override{}, blocks: map[pr.Ref]dinv.Event{},
		events: map[pr.Ref][]dinv.Event{}, books: map[unit.Unit]turn.Book{}, rounds: round.New(),
		git: &fakeGit{heads: map[string]string{}, dirty: map[string]bool{}}, branches: &fakeBranches{}}
	w.cfg.Views = map[string]config.SinkConfig{"board": {Columns: map[string]string{config.ColumnCompleted: "Done"}}}
	w.snap = &wsdomain.Snapshot{Gen: 1, Repos: []wsdomain.Repo{repo}, WorkspaceScope: complete, TerminalScope: complete,
		Workspaces: []wsdomain.Workspace{{ID: "main", RepoID: "r1", Path: "/w/app", DisplayName: "app", Branch: "main", HostID: "local", IsMain: true}}}
	return w
}

func (w *world) provider() *factsgithub.Provider { return w.providerWith(nil) }

// providerWith is provider over a change log, which keeps its view between
// calls; nil builds one per call.
func (w *world) providerWith(changes port.Changes) *factsgithub.Provider {
	d := factsgithub.Deps{Inputs: w, Store: w, Snapshot: w, Books: w, Rounds: w, Git: w.git, Branches: w.branches,
		Cfg: func() config.Config { return w.cfg }, Sink: "board", Clock: clock{w},
		Intents: func() *dintent.Registry { return turnstest.Intents() }, Reports: w}
	if changes != nil {
		d.Changes = changes
	}
	return factsgithub.New(d)
}

type clock struct{ w *world }

func (c clock) Now() time.Time { return c.w.now }

// add stores an open pull request of author; edit changes its row and the
// facts its detail adds.
func (w *world) add(n int, author string, edit func(*dinv.PR, *pr.Facts)) pr.Ref {
	r := ref(n)
	row := dinv.PR{Ref: r, Author: author, State: "OPEN", HeadOid: "head", HeadBranch: "feature", BaseBranch: "main",
		CreatedAt: w.now.Add(-72 * time.Hour), UpdatedAt: w.now.Add(-time.Hour), Reasons: []string{"followed"}}
	f := pr.Facts{AuthorAssociation: "MEMBER"}
	if edit != nil {
		edit(&row, &f)
	}
	f.Ref, f.State, f.IsDraft, f.HeadRefOid, f.HeadRefName, f.BaseRefName = r, row.State, row.IsDraft, row.HeadOid, row.HeadBranch, row.BaseBranch
	f.Author, f.ReviewRequests, f.IsCrossRepository, f.ChecksState = row.Author, row.ReviewRequests, row.IsFork, row.ChecksState
	f.Mergeable, f.ReviewDecision = row.Mergeable, row.ReviewDecision
	w.rows[r] = row
	w.details[r] = dinv.PlanFacts{Facts: f, ReadAt: w.now.Add(-time.Minute)}
	return r
}

func (w *world) workspace(ws wsdomain.Workspace) {
	if ws.RepoID == "" {
		ws.RepoID = "r1"
	}
	if ws.HostID == "" {
		ws.HostID = "local"
	}
	w.snap.Workspaces = append(w.snap.Workspaces, ws)
}

func (w *world) Viewer(context.Context) (string, error) { return w.viewer, nil }

func (w *world) Rows(context.Context) ([]dinv.PR, error) {
	rows := slices.Collect(maps.Values(w.rows))
	slices.SortFunc(rows, func(a, b dinv.PR) int { return a.Ref.Number - b.Ref.Number })
	return rows, nil
}

func (w *world) Facts(_ context.Context, rows []dinv.PR, _ string) (map[unit.Unit]dinv.PlanFacts, error) {
	w.factReads++
	out := map[unit.Unit]dinv.PlanFacts{}
	for _, r := range rows {
		out[r.Ref.Unit()] = w.details[r.Ref]
	}
	return out, nil
}

func (w *world) Ended(_ context.Context, _ string, units []unit.Unit) (map[unit.Unit]bool, error) {
	out := map[unit.Unit]bool{}
	for _, u := range units {
		out[u] = w.ended[u]
	}
	return out, nil
}

func (w *world) Overrides(context.Context) (map[pr.Ref]dinv.Override, error) {
	return maps.Clone(w.overrides), nil
}

func (w *world) FirstRoundBlocks(context.Context) (map[unit.Unit]port.Event, error) {
	out := map[unit.Unit]port.Event{}
	for ref, ev := range w.blocks {
		out[ref.Unit()] = port.Event{Unit: ref.Unit(), Kind: ev.Kind, Key: ev.Key, Data: ev.Data, At: ev.At}
	}
	return out, nil
}

func (w *world) EventsByPR(_ context.Context, refs []pr.Ref) (map[pr.Ref][]dinv.Event, error) {
	out := map[pr.Ref][]dinv.Event{}
	for _, r := range refs {
		out[r] = w.events[r]
	}
	return out, nil
}

func (w *world) RecordEvent(_ context.Context, ev dinv.Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.recorded = append(w.recorded, ev)
	return nil
}

func (w *world) Read(context.Context, wsdomain.Part) (*wsdomain.Snapshot, error) { return w.snap, nil }

func (w *world) Books(_ context.Context, units ...unit.Unit) (map[unit.Unit]turn.Book, error) {
	if len(units) == 0 {
		return maps.Clone(w.books), nil
	}
	out := map[unit.Unit]turn.Book{}
	for _, u := range units {
		out[u] = w.books[u]
	}
	return out, nil
}

func (w *world) Load(context.Context) (*round.Store, error) { return w.rounds, nil }

func (w *world) Pause(context.Context) (round.Pause, error) { return w.pause, nil }

type fakeGit struct {
	mu        sync.Mutex
	calls     []string
	heads     map[string]string
	dirty     map[string]bool
	upstreams map[string]string
	ahead     bool
}

func (g *fakeGit) call(name string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, name)
}

func (g *fakeGit) called() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.calls)
}

func (g *fakeGit) Status(_ context.Context, dir string) (sweep.GitFacts, error) {
	g.call("status")
	return sweep.GitFacts{Dirty: g.dirty[dir], Head: g.heads[dir]}, nil
}

func (g *fakeGit) Head(_ context.Context, dir string) (string, error) {
	g.call("head")
	return g.heads[dir], nil
}

func (g *fakeGit) Upstream(_ context.Context, dir string) (string, error) {
	g.call("upstream")
	return g.upstreams[dir], nil
}

func (g *fakeGit) IsAncestor(context.Context, string, string, string) (bool, error) {
	g.call("ancestor")
	return g.ahead, nil
}

type fakeBranches struct{ checkedOut map[string]string }

func (b *fakeBranches) LocalBranchTip(context.Context, string, string) (string, error) {
	if len(b.checkedOut) > 0 {
		return "tip", nil
	}
	return "", nil
}

func (b *fakeBranches) CheckedOutAt(_ context.Context, _, branch string) (string, error) {
	return b.checkedOut[branch], nil
}

func (b *fakeBranches) HasCommit(context.Context, string, string) (bool, error) { return false, nil }

func (b *fakeBranches) IsAncestor(context.Context, string, string, string) (bool, error) {
	return true, nil
}

// facts lists, then reads u's facts, as an evaluation does, resolved at
// the world's now.
func (w *world) facts(t *testing.T, p *factsgithub.Provider, u unit.Unit) workflow.Facts {
	t.Helper()
	ctx := context.Background()
	if _, err := p.List(ctx, unit.GitHub); err != nil {
		t.Fatal(err)
	}
	f, err := p.Facts(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	if resolved, _ := workflow.Resolve(f, w.now); resolved != nil {
		return resolved
	}
	return f
}

// review is a pull request of alice under review in a linked workspace, with
// my review on its first commit.
func (w *world) review(n int, edit func(*dinv.PR, *pr.Facts)) pr.Ref {
	r := w.add(n, "alice", func(row *dinv.PR, f *pr.Facts) {
		row.HeadOid = "c2"
		f.Commits = []pr.Commit{{Oid: "c1", Parents: 1}, {Oid: "c2", Parents: 1}}
		f.Reviews = []pr.Review{{ID: "r1", State: pr.ReviewCommented, Author: me, SubmittedAt: w.now.Add(-2 * time.Hour), CommitOid: "c1"}}
		if edit != nil {
			edit(row, f)
		}
	})
	w.workspace(wsdomain.Workspace{ID: "rev", Path: "/w/rev", DisplayName: "REVIEW feature", Branch: "feature", LinkedPR: n, Status: "Reviewing"})
	return r
}

func TestFollowupFactsMatchChangeReviewer(t *testing.T) {
	for _, c := range []struct {
		name   string
		edit   func(*dinv.PR, *pr.Facts)
		setup  func(w *world)
		action string
	}{
		{name: "no review by me", edit: func(_ *dinv.PR, f *pr.Facts) { f.Reviews = nil }},
		{name: "new commits", action: "follow-up"},
		{name: "this head got its follow-up", setup: func(w *world) {
			w.rounds.SetMark(ref(7).String(), round.Reviewed, t0.Add(-2*time.Hour).UTC().Format(time.RFC3339)+"@c2")
		}},
		{name: "this review got its follow-up", setup: func(w *world) {
			w.rounds.SetMark(ref(7).String(), round.Reviewed, t0.Add(-2*time.Hour).UTC().Format(time.RFC3339))
		}},
		{name: "draft", edit: func(row *dinv.PR, _ *pr.Facts) { row.IsDraft = true }},
		{name: "fork", edit: func(row *dinv.PR, _ *pr.Facts) { row.IsFork = true }},
		{name: "merged", edit: func(row *dinv.PR, _ *pr.Facts) { row.State = "MERGED" }, action: "complete"},
		{name: "merged and completed", edit: func(row *dinv.PR, _ *pr.Facts) { row.State = "MERGED" }, setup: func(w *world) {
			w.snap.Workspaces[1].Status = "Done"
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld()
			r := w.review(7, c.edit)
			if c.setup != nil {
				c.setup(w)
			}
			want := dchange.Reviewer(dchange.ReviewerInput{PR: w.details[r].Facts, Viewer: me, WorkspaceStatus: w.snap.Workspaces[1].Status,
				CompletedStatus: "Done", TrustedOwners: []string{"acme"}, LastFingerprint: w.rounds.Mark(r.String(), round.Reviewed)})
			f := w.facts(t, w.provider(), r.Unit())
			if f["pr.review.action"] != c.action || f["pr.review.reason"] != want.Reason {
				t.Fatalf("review = %q, %q; want %q, %q", f["pr.review.action"], f["pr.review.reason"], c.action, want.Reason)
			}
			if f["pr.review.observed"] != false {
				t.Fatal("a follow-up with its workspace is observed")
			}
		})
	}
}

func TestAFollowupWithoutItsWorkspaceIsObserved(t *testing.T) {
	w := newWorld()
	r := w.add(7, "alice", func(row *dinv.PR, f *pr.Facts) {
		row.HeadOid = "c2"
		f.Commits = []pr.Commit{{Oid: "c1", Parents: 1}, {Oid: "c2", Parents: 1}}
		f.Reviews = []pr.Review{{ID: "r1", State: pr.ReviewCommented, Author: me, SubmittedAt: t0.Add(-2 * time.Hour), CommitOid: "c1"}}
	})
	w.books[r.Unit()] = turn.Book{Unit: r.Unit(), Turns: []turn.Turn{{Seq: 1, Intent: turn.Intent(dintent.Review), Role: "pr.review", State: turn.Done, SentAt: t0.Add(-3 * time.Hour)}}}
	f := w.facts(t, w.provider(), r.Unit())
	if f["pr.review.action"] != "follow-up" || f["pr.review.observed"] != true {
		t.Fatalf("facts = %v", f)
	}
	w.cfg.GitHub.FollowupsWithoutWorkspace = true
	if f := w.facts(t, w.provider(), r.Unit()); f["pr.review.observed"] != false {
		t.Fatalf("followups_without_workspace still observes: %v", f)
	}
}

// requested is a pull request of alice that requests my review.
func (w *world) requested(n int, edit func(*dinv.PR, *pr.Facts)) pr.Ref {
	return w.add(n, "alice", func(row *dinv.PR, f *pr.Facts) {
		row.ReviewRequests = []string{me}
		if edit != nil {
			edit(row, f)
		}
	})
}

func TestFirstRoundFactsCarryTheSkipReason(t *testing.T) {
	for _, c := range []struct {
		name   string
		edit   func(*dinv.PR, *pr.Facts)
		setup  func(w *world)
		due    bool
		reason string
	}{
		{name: "due", due: true, reason: "my review was requested"},
		{name: "skipped", setup: func(w *world) { w.rounds.SkipFirstRound(ref(3).String()) }, reason: "you skipped its first round"},
		{name: "review workspace", setup: func(w *world) {
			w.workspace(wsdomain.Workspace{ID: "rev", DisplayName: "REVIEW feature", Branch: "feature-x"})
		}, reason: "its review workspace exists"},
		{name: "untrusted", edit: func(_ *dinv.PR, f *pr.Facts) { f.AuthorAssociation = "NONE" },
			reason: "author association NONE is not trusted for automated rounds"},
		{name: "branch checked out", setup: func(w *world) { w.branches.checkedOut = map[string]string{"feature": "/w/other"} },
			reason: "my review was requested, but feature is checked out in /w/other"},
		{name: "lapsed", edit: func(row *dinv.PR, _ *pr.Facts) { row.Reasons = nil },
			reason: "my review was requested, but nothing follows it in the inventory"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld()
			r := w.requested(3, c.edit)
			if c.setup != nil {
				c.setup(w)
			}
			f := w.facts(t, w.provider(), r.Unit())
			if f["pr.first.due"] != c.due || f["pr.first.reason"] != c.reason {
				t.Fatalf("first = %v, %q; want %v, %q", f["pr.first.due"], f["pr.first.reason"], c.due, c.reason)
			}
		})
	}
}

// mine is a pull request of mine developed in its own worktree, with bob's
// change request on its head and a failing check.
func (w *world) mine(n int, edit func(*dinv.PR, *pr.Facts)) pr.Ref {
	r := w.add(n, me, func(row *dinv.PR, f *pr.Facts) {
		row.ChecksState = "FAILURE"
		f.Reviews = []pr.Review{{ID: "b1", State: pr.ReviewChangesRequested, Author: "bob", SubmittedAt: t0.Add(-time.Hour), CommitOid: "head", HasContent: true}}
		f.Checks = []pr.Check{{Name: "lint", Failed: true}, {Name: "test"}}
		if edit != nil {
			edit(row, f)
		}
	})
	w.workspace(wsdomain.Workspace{ID: "dev", Path: "/w/dev", DisplayName: "feature", Branch: "feature", LinkedPR: n, Status: "In progress"})
	return r
}

func TestAddressFactsMatchReviewsToAddressAndFailingChecks(t *testing.T) {
	w := newWorld()
	r := w.mine(5, nil)
	p := w.provider()
	f := w.facts(t, p, r.Unit())
	who, fp := dchange.ReviewsToAddress(dchange.AddressInput{PR: w.details[r].Facts, Viewer: me})
	failed := dchange.FailingChecks(w.details[r].Facts, "", nil)
	if f["pr.address.due"] != true || !slices.Equal(f["pr.address.reviewers"].([]string), who) || !slices.Equal(f["pr.address.checks"].([]string), failed) {
		t.Fatalf("address = %v, %v, %v; want %v, %v", f["pr.address.due"], f["pr.address.reviewers"], f["pr.address.checks"], who, failed)
	}
	if f["pr.address.reason"] != "reviewed by bob; failing checks: lint" || f["pr.address.observed"] != false {
		t.Fatalf("reason = %q, observed %v", f["pr.address.reason"], f["pr.address.observed"])
	}
	c, ok := w.planned(t, p, r.Unit(), factsgithub.RoundAddress)
	want := map[string]string{round.AddressedReviews: fp, round.AddressedChecks: "head"}
	if !ok || !maps.Equal(c.OnSent, want) || !maps.Equal(c.OnSkipped, want) || c.Workspace.ID != "dev" || c.Head != "head" {
		t.Fatalf("round = %+v, %v", c, ok)
	}

	w.rounds.SetMark(r.String(), round.AddressedReviews, fp)
	w.rounds.SetHeads(r.String(), "head", "")
	if f := w.facts(t, w.provider(), r.Unit()); f["pr.address.due"] != false || f["pr.address.reason"] != "nothing to address" {
		t.Fatalf("a handled pull request is due: %v", f)
	}
	if _, ok := w.planned(t, w.provider(), r.Unit(), factsgithub.RoundAddress); ok {
		t.Fatal("a handled pull request plans its round")
	}
}

func TestAConflictPastItsWindowStartsOneAddressRoundPerHead(t *testing.T) {
	w := newWorld()
	r := w.mine(5, func(row *dinv.PR, f *pr.Facts) {
		row.Mergeable, row.MergeableAt, row.ChecksState = "CONFLICTING", t0.Add(-time.Hour), ""
		f.Reviews, f.Checks = nil, nil
	})
	p := w.provider()
	f := w.facts(t, p, r.Unit())
	if f["pr.address.due"] != true || f["pr.address.reason"] != "conflicts with main" || f["pr.address.conflict"] != true {
		t.Fatalf("a lasting conflict: %v", f)
	}
	if f["pr.head"] != "head" || f["pr.conflicting_for"] != time.Hour {
		t.Fatalf("signals: head %v, conflicting for %v", f["pr.head"], f["pr.conflicting_for"])
	}
	c, ok := w.planned(t, p, r.Unit(), factsgithub.RoundAddress)
	if want := map[string]string{round.AddressedConflict: "head"}; !ok || !maps.Equal(c.OnSent, want) || !maps.Equal(c.OnSkipped, want) {
		t.Fatalf("round = %+v, %v", c, ok)
	}
	first := f["pr.fingerprint"]

	for name, v := range c.OnSkipped {
		w.rounds.SetMark(r.String(), name, v)
	}
	if _, ok := w.planned(t, w.provider(), r.Unit(), factsgithub.RoundAddress); ok || w.rounds.Mark(r.String(), round.AddressedConflict) != "head" {
		t.Fatalf("an address round skipped with its conflict head is planned again")
	}
	if f := w.facts(t, w.provider(), r.Unit()); f["pr.address.due"] != false || f["pr.address.reason"] != "nothing to address" || f["pr.address.conflict"] != false {
		t.Fatalf("a handled conflict is due: %v", f)
	}

	row, d := w.rows[r], w.details[r]
	row.HeadOid, d.HeadRefOid, d.Mergeable = "head2", "head2", "UNKNOWN"
	w.rows[r], w.details[r] = row, d
	if f := w.facts(t, w.provider(), r.Unit()); f["pr.address.due"] != false {
		t.Fatalf("a new head GitHub has not judged yet, under the stored conflict: %v", f)
	}
	d.Mergeable = "CONFLICTING"
	w.details[r] = d
	f = w.facts(t, w.provider(), r.Unit())
	if f["pr.address.due"] != true || f["pr.fingerprint"] == first {
		t.Fatalf("a conflict on a new head: %v", f)
	}
}

func TestAddressConflictsOffStartsNoRoundForAConflict(t *testing.T) {
	w := newWorld()
	w.cfg.GitHub.AddressConflicts = false
	r := w.mine(5, func(row *dinv.PR, f *pr.Facts) {
		row.Mergeable, row.MergeableAt, row.ChecksState = "CONFLICTING", t0.Add(-time.Hour), ""
		f.Reviews, f.Checks = nil, nil
	})
	if f := w.facts(t, w.provider(), r.Unit()); f["pr.address.due"] != false || f["pr.address.conflict"] != false {
		t.Fatalf("address_conflicts off: %v", f)
	}
}

func TestAConflictWithinItsWindowIsNoReason(t *testing.T) {
	w := newWorld()
	r := w.mine(5, func(row *dinv.PR, f *pr.Facts) {
		row.Mergeable, row.MergeableAt, row.ChecksState = "CONFLICTING", t0.Add(-time.Minute), ""
		f.Reviews, f.Checks = nil, nil
	})
	if f := w.facts(t, w.provider(), r.Unit()); f["pr.address.due"] != false || f["pr.address.conflict"] != false {
		t.Fatalf("a fresh conflict: %v", f)
	}
}

func TestAConflictJoinsTheReviewsItCameWith(t *testing.T) {
	w := newWorld()
	r := w.mine(5, func(row *dinv.PR, _ *pr.Facts) {
		row.Mergeable, row.MergeableAt = "CONFLICTING", t0.Add(-time.Hour)
	})
	if f := w.facts(t, w.provider(), r.Unit()); f["pr.address.reason"] != "reviewed by bob; failing checks: lint; conflicts with main" {
		t.Fatalf("reason = %q", f["pr.address.reason"])
	}
}

func TestAddressWithoutAWorkspaceNeedsReviews(t *testing.T) {
	w := newWorld()
	r := w.add(5, me, func(row *dinv.PR, f *pr.Facts) {
		row.ChecksState = "FAILURE"
		f.Checks = []pr.Check{{Name: "lint", Failed: true}}
	})
	if f := w.facts(t, w.provider(), r.Unit()); f["pr.address.due"] != false || f["pr.address.reason"] != "failing checks or a conflict alone start no round without a workspace" {
		t.Fatalf("checks alone without a workspace: %v", f)
	}
	r = w.add(7, me, func(row *dinv.PR, _ *pr.Facts) {
		row.Mergeable, row.MergeableAt = "CONFLICTING", t0.Add(-time.Hour)
	})
	if f := w.facts(t, w.provider(), r.Unit()); f["pr.address.due"] != false || f["pr.address.reason"] != "failing checks or a conflict alone start no round without a workspace" {
		t.Fatalf("a conflict alone without a workspace: %v", f)
	}
	r = w.mine(6, nil)
	w.snap.Workspaces = w.snap.Workspaces[:1]
	f := w.facts(t, w.provider(), r.Unit())
	if f["pr.address.due"] != true || f["pr.address.observed"] != true || f["pr.has_workspace"] != false {
		t.Fatalf("reviews without a workspace: %v", f)
	}
}

func TestTheFingerprintIsStableAcrossSweeps(t *testing.T) {
	w := newWorld()
	r := w.review(7, nil)
	first := w.facts(t, w.provider(), r.Unit())["pr.fingerprint"]
	if first == "" {
		t.Fatal("a due follow-up has no fingerprint")
	}
	w.now = w.now.Add(5 * time.Minute)
	if again := w.facts(t, w.provider(), r.Unit())["pr.fingerprint"]; again != first {
		t.Fatalf("fingerprint moved from %q to %q", first, again)
	}
	d := w.details[r]
	d.Reviews = append(d.Reviews, pr.Review{ID: "r2", State: pr.ReviewCommented, Author: me, SubmittedAt: w.now.Add(-time.Minute), CommitOid: "c1"})
	w.details[r] = d
	if next := w.facts(t, w.provider(), r.Unit())["pr.fingerprint"]; next == first {
		t.Fatal("a new review keeps the fingerprint")
	}
}

func TestListReachesMergedPRsWithOpenTurnsOrQueuedAnswers(t *testing.T) {
	w := newWorld()
	merged := func(row *dinv.PR, _ *pr.Facts) { row.State, row.MergedAt = "MERGED", t0.Add(-time.Hour) }
	withTurn, withAnswer, neither := w.add(8, "alice", merged), w.add(9, "alice", merged), w.add(10, "alice", merged)
	w.books[withTurn.Unit()] = turn.Book{Unit: withTurn.Unit(), Turns: []turn.Turn{{Seq: 2, Intent: "review", Role: "pr.review", State: turn.Sent}}}
	w.books[withAnswer.Unit()] = turn.Book{Unit: withAnswer.Unit(), Turns: []turn.Turn{{Seq: 1, Intent: "review", Role: "pr.review", State: turn.Done}},
		Answers: []turn.Answer{{ID: 41, TurnSeq: 1, Route: turn.Queued}}}
	open := w.add(11, "alice", nil)
	p := w.provider()
	got, err := p.List(context.Background(), unit.GitHub)
	if err != nil {
		t.Fatal(err)
	}
	want := []unit.Unit{withTurn.Unit(), withAnswer.Unit(), open.Unit()}
	slices.SortFunc(got, func(a, b unit.Unit) int { return strings.Compare(a.Key, b.Key) })
	slices.SortFunc(want, func(a, b unit.Unit) int { return strings.Compare(a.Key, b.Key) })
	if !slices.Equal(got, want) {
		t.Fatalf("list = %v, want %v", got, want)
	}
	if slices.Contains(got, neither.Unit()) {
		t.Fatal("a merged pull request without a turn is listed")
	}
	f := w.facts(t, p, withTurn.Unit())
	if f["pr.open_turn"] != "sent" || f["pr.open_turn_seq"] != 2 || f["pr.state"] != "MERGED" || f["pr.ended"] != true {
		t.Fatalf("open turn facts = %v", f)
	}
	if f := w.facts(t, p, open.Unit()); f["pr.ended"] != false {
		t.Fatalf("an open pull request has ended: %v", f)
	}
	if f := w.facts(t, p, withAnswer.Unit()); f["pr.orphan_answer_id"] != 41 || f["pr.open_turn"] != "" {
		t.Fatalf("orphan answer facts = %v", f)
	}
	if other, _ := p.List(context.Background(), unit.Session); len(other) != 0 {
		t.Fatalf("listed %v for another provider", other)
	}
}

func TestFactsAreReadOnlyAndSendNoGitHubRequest(t *testing.T) {
	w := newWorld()
	units := []unit.Unit{w.review(7, nil).Unit(), w.requested(3, nil).Unit(), w.mine(5, nil).Unit()}
	w.blocks[ref(4)] = dinv.Event{PR: ref(4), Kind: dinv.KindRoundBlocked, Data: "old"}
	p := w.provider()
	ctx := context.Background()
	for _, u := range units {
		w.facts(t, p, u)
		if _, err := p.Plan(ctx, plugin.Unit{Provider: string(u.Provider), Key: u.Key}, w.roundState(u)); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Author(ctx, u, true); err != nil {
			t.Fatal(err)
		}
	}
	p.Due()
	if len(w.recorded) != 0 {
		t.Fatalf("reading facts recorded %v", w.recorded)
	}
	if c := w.rounds.Changes(); len(c.PRs) != 0 || len(c.Counts) != 0 || len(c.Notes) != 0 {
		t.Fatalf("reading facts edited the round state: %+v", c)
	}
}

// state is the sweep's round state as the composition root gives Settle.
type state struct{ st *round.Store }

func (s state) Save(context.Context) error { return nil }
func (s state) Rounds() *round.Store       { return s.st }

func TestSettleWritesTheBasesBookkeeping(t *testing.T) {
	w := newWorld()
	reviewed := w.review(7, nil)
	reviewedAt := t0.Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	w.rounds.SetMark(reviewed.String(), round.Reviewed, reviewedAt)
	skipped := w.requested(3, func(row *dinv.PR, _ *pr.Facts) { row.HeadBranch = "skipped" })
	w.rounds.SkipFirstRound(skipped.String())
	w.workspace(wsdomain.Workspace{ID: "rev3", DisplayName: "REVIEW skipped", Branch: "skipped"})
	w.blocks[skipped] = dinv.Event{PR: skipped, Kind: dinv.KindRoundBlocked, Data: "old"}
	busy := w.requested(4, func(row *dinv.PR, _ *pr.Facts) { row.HeadBranch = "busy" })
	w.branches.checkedOut = map[string]string{"busy": "/w/other"}
	already := w.requested(6, func(row *dinv.PR, _ *pr.Facts) { row.HeadBranch = "busy" })
	w.blocks[already] = dinv.Event{PR: already, Kind: dinv.KindRoundBlocked, Data: "busy is checked out in /w/other"}
	gone := w.add(9, "alice", nil)
	w.blocks[gone] = dinv.Event{PR: gone, Kind: dinv.KindRoundBlocked, Data: "old"}
	held := w.requested(12, func(row *dinv.PR, _ *pr.Facts) { row.HeadBranch = "busy" })
	w.overrides[held] = dinv.Override{PR: held, Hold: true}
	w.rounds.Saved()

	if err := w.provider().Settle(context.Background(), state{w.rounds}); err != nil {
		t.Fatal(err)
	}
	if fp := w.rounds.Mark(reviewed.String(), round.Reviewed); fp != reviewedAt+"@c2" {
		t.Fatalf("fingerprint = %q, want the head added", fp)
	}
	if w.rounds.FirstRoundSkipped(skipped.String()) {
		t.Fatal("a review workspace left the first round skipped")
	}
	got := map[pr.Ref]string{}
	for _, ev := range w.recorded {
		got[ev.PR] = ev.Kind + ": " + ev.Data
	}
	want := map[pr.Ref]string{
		skipped: "round_unblocked: its review workspace exists",
		busy:    "round_blocked: busy is checked out in /w/other",
		gone:    "round_unblocked: my review is no longer requested",
	}
	if !maps.Equal(got, want) {
		t.Fatalf("recorded %v, want %v", got, want)
	}
	if err := w.provider().Settle(context.Background(), struct{ state2 }{}); err == nil {
		t.Fatal("settling without the round store succeeded")
	}
}

type state2 struct{}

func (state2) Save(context.Context) error { return nil }

func TestStaleDetailSetsStale(t *testing.T) {
	w := newWorld()
	fresh, stale := w.review(7, nil), w.add(8, "alice", nil)
	closed := w.add(9, "alice", func(row *dinv.PR, _ *pr.Facts) { row.State = "CLOSED" })
	for _, r := range []pr.Ref{stale, closed} {
		d := w.details[r]
		d.ReadAt = t0.Add(-2 * time.Hour)
		w.details[r] = d
	}
	p := w.provider()
	for r, want := range map[pr.Ref]bool{fresh: false, stale: true, closed: false} {
		if got := w.facts(t, p, r.Unit())["pr.stale"]; got != want {
			t.Errorf("%s: stale = %v, want %v", r, got, want)
		}
	}
}

func TestVisibilityComesFromTheDetail(t *testing.T) {
	w := newWorld()
	private := w.add(7, "alice", func(_ *dinv.PR, f *pr.Facts) { f.Visibility = "private" })
	public := w.add(8, "alice", func(_ *dinv.PR, f *pr.Facts) { f.Visibility = "public" })
	unread := w.add(9, "alice", nil)
	delete(w.details, unread)
	p := w.provider()
	for r, want := range map[pr.Ref]string{private: "private", public: "public", unread: ""} {
		if got := w.facts(t, p, r.Unit())["pr.visibility"]; got != want {
			t.Errorf("%s: visibility = %v, want %q", r, got, want)
		}
	}
	if f := w.facts(t, p, private.Unit()); f["pr.owner"] != private.Owner || f["pr.author"] != "alice" {
		t.Errorf("owner %v, author %v", f["pr.owner"], f["pr.author"])
	}
}

func TestLastInteractionCountsAnyonesCommentReviewPushOrRequest(t *testing.T) {
	w := newWorld()
	r := w.add(5, me, func(row *dinv.PR, _ *pr.Facts) { row.CreatedAt = t0.Add(-60 * 24 * time.Hour) })
	day := 24 * time.Hour
	w.events[r] = []dinv.Event{
		{PR: r, Kind: dinv.KindComment, Actor: "bob", At: t0.Add(-40 * day)},
		{PR: r, Kind: dinv.KindPush, At: t0.Add(-35 * day)},
		{PR: r, Kind: dinv.KindReviewRequest, Actor: me, At: t0.Add(-32 * day)},
		{PR: r, Kind: dinv.KindCheck, At: t0.Add(-day)},
		{PR: r, Kind: dinv.KindSituation, At: t0.Add(-day)},
	}
	f := w.facts(t, w.provider(), r.Unit())
	if at := f["pr.last_interaction_at"]; at != t0.Add(-32*day) || f["pr.silent_days"] != 32 {
		t.Fatalf("last interaction = %v, %v days", at, f["pr.silent_days"])
	}
	quiet := w.add(6, me, func(row *dinv.PR, _ *pr.Facts) { row.CreatedAt = t0.Add(-3 * day) })
	if f := w.facts(t, w.provider(), quiet.Unit()); f["pr.last_interaction_at"] != t0.Add(-3*day) {
		t.Fatalf("without an interaction = %v, want its opening", f["pr.last_interaction_at"])
	}
}

func TestWaitsOnUserFollowsTheRow(t *testing.T) {
	w := newWorld()
	asksMe := w.requested(3, nil)
	waitsOnBob := w.add(5, me, func(row *dinv.PR, _ *pr.Facts) { row.ReviewRequests = []string{"bob"} })
	myDraft := w.add(6, me, func(row *dinv.PR, _ *pr.Facts) { row.IsDraft = true })
	p := w.provider()
	for r, want := range map[pr.Ref]bool{asksMe: true, waitsOnBob: false, myDraft: true} {
		if got := w.facts(t, p, r.Unit())["pr.waits_on_user"]; got != want {
			t.Errorf("%s: waits_on_user = %v, want %v", r, got, want)
		}
	}
	if f := w.facts(t, p, waitsOnBob.Unit()); f["pr.nudge.refusal"] != "" {
		t.Errorf("a nudge to bob is refused: %q", f["pr.nudge.refusal"])
	}
	if f := w.facts(t, p, asksMe.Unit()); f["pr.nudge.refusal"] == "" {
		t.Error("a nudge on a pull request waiting on me goes")
	}
}

func TestDueMatchesTheBasesNextDue(t *testing.T) {
	day := 24 * time.Hour
	for _, c := range []struct {
		name  string
		setup func(w *world)
		want  time.Time
	}{
		{"review cutoff", func(w *world) {
			r := w.add(7, "alice", nil)
			w.books[r.Unit()] = turn.Book{Unit: r.Unit(), Turns: []turn.Turn{{Seq: 1, Intent: "review", Role: "pr.review", State: turn.Done, SentAt: t0.Add(-10 * day)}}}
		}, t0.Add(20 * day)},
		{"cap rollover", func(w *world) {
			r := w.review(7, nil)
			for range w.cfg.Turns.PerUnitPerDay {
				w.rounds.Count(r.String(), t0)
			}
		}, nextDay(t0)},
		{"conflict window", func(w *world) {
			w.mine(5, func(row *dinv.PR, f *pr.Facts) {
				row.Mergeable, row.MergeableAt, row.ChecksState = "CONFLICTING", t0.Add(-time.Minute), ""
				f.Reviews, f.Checks = nil, nil
			})
		}, t0.Add(4 * time.Minute)},
		{"stale detail", func(w *world) {
			r := w.review(7, nil)
			d := w.details[r]
			d.ReadAt = time.Time{}
			w.details[r] = d
		}, t0.Add(2 * time.Minute)},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld()
			c.setup(w)
			p := w.provider()
			if _, err := p.List(context.Background(), unit.GitHub); err != nil {
				t.Fatal(err)
			}
			if got := p.Due(); !got.Equal(c.want) {
				t.Fatalf("due = %v, want %v", got, c.want)
			}
		})
	}
}

func nextDay(t time.Time) time.Time {
	y, m, d := t.Local().Date()
	return time.Date(y, m, d+1, 0, 0, 0, 0, time.Local)
}

func TestAuthorFactsReadOnlyWhatLocalNeedAsks(t *testing.T) {
	for _, c := range []struct {
		name string
		edit func(*dinv.PR, *pr.Facts)
		want []string
	}{
		{"approved and green reads the tree", func(row *dinv.PR, _ *pr.Facts) { row.ReviewDecision = "APPROVED" }, []string{"status", "ancestor"}},
		{"not approved reads the head", nil, []string{"head", "ancestor"}},
		{"merged reads nothing", func(row *dinv.PR, _ *pr.Facts) { row.State = "MERGED" }, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld()
			r := w.add(5, me, c.edit)
			w.workspace(wsdomain.Workspace{ID: "dev", Path: "/w/dev", DisplayName: "feature", Branch: "feature", LinkedPR: 5})
			w.git.heads["/w/dev"] = "local"
			p := w.provider()
			if _, err := p.Author(context.Background(), r.Unit(), false); err != nil || len(w.git.called()) != 0 {
				t.Fatalf("without needLocal git saw %v, %v", w.git.called(), err)
			}
			f, err := p.Author(context.Background(), r.Unit(), true)
			if err != nil {
				t.Fatal(err)
			}
			if got := w.git.called(); !slices.Equal(got, c.want) {
				t.Fatalf("git calls = %v, want %v", got, c.want)
			}
			if (f.Local != nil) != (c.want != nil) {
				t.Fatalf("local = %+v", f.Local)
			}
		})
	}
	if f, err := newWorld().provider().Author(context.Background(), ref(99).Unit(), true); f != nil || err != nil {
		t.Fatalf("an unknown pull request has author facts %+v, %v", f, err)
	}
}

func TestEveryFactIsDeclaredWithItsType(t *testing.T) {
	w := newWorld()
	r := w.mine(5, func(row *dinv.PR, _ *pr.Facts) {
		row.State, row.MergedAt, row.ClosedAt = "MERGED", t0.Add(-time.Hour), t0.Add(-time.Hour)
	})
	f := w.facts(t, w.provider(), r.Unit())
	types := map[expr.Type]func(any) bool{
		expr.Bool:     func(v any) bool { _, ok := v.(bool); return ok },
		expr.Int:      func(v any) bool { _, ok := v.(int); return ok },
		expr.String:   func(v any) bool { _, ok := v.(string); return ok },
		expr.List:     func(v any) bool { _, ok := v.([]string); return ok },
		expr.Duration: func(v any) bool { _, ok := v.(time.Duration); return ok },
		expr.Time:     func(v any) bool { _, ok := v.(time.Time); return ok },
	}
	for name, v := range f {
		typ, ok := builtin.FactType(name)
		if !ok || !types[typ](v) {
			t.Errorf("%s = %#v: declared %v, %v", name, v, typ, ok)
		}
	}
	for _, name := range builtin.WorkflowEnv("pr").Names() {
		if _, ok := f[name]; !ok && name[:3] == "pr." {
			t.Errorf("%s is declared and never set", name)
		}
	}
}

func TestListReachesAFollowupWithoutItsWorkspace(t *testing.T) {
	w := newWorld()
	r := w.add(7, "alice", func(row *dinv.PR, f *pr.Facts) {
		row.HeadOid, row.Reasons = "c2", nil
		f.Commits = []pr.Commit{{Oid: "c1", Parents: 1}, {Oid: "c2", Parents: 1}}
		f.Reviews = []pr.Review{{ID: "r1", State: pr.ReviewCommented, Author: me, SubmittedAt: t0.Add(-2 * time.Hour), CommitOid: "c1"}}
	})
	w.books[r.Unit()] = turn.Book{Unit: r.Unit(), Turns: []turn.Turn{{Seq: 1, Intent: turn.Intent(dintent.Review), Role: "pr.review", State: turn.Done, SentAt: t0.Add(-3 * time.Hour)}}}
	p := w.provider()
	got, err := p.List(context.Background(), unit.GitHub)
	if err != nil || !slices.Contains(got, r.Unit()) {
		t.Fatalf("list = %v, %v; want the lapsed follow-up", got, err)
	}
	if f := w.facts(t, p, r.Unit()); f["pr.review.action"] != "follow-up" || f["pr.review.observed"] != true {
		t.Fatalf("facts = %v", f)
	}
}

// TestListOrdersEachRoundAsTheBasePlannedIt holds every path to one slot:
// the unit each workflow meets first is the one the base sent.
func TestListOrdersEachRoundAsTheBasePlannedIt(t *testing.T) {
	w := newWorld()
	followup := func(n int, reviewed time.Duration, branch string) pr.Ref {
		r := w.add(n, "alice", func(row *dinv.PR, f *pr.Facts) {
			row.HeadOid, row.HeadBranch = "c2", branch
			f.Commits = []pr.Commit{{Oid: "c1", Parents: 1}, {Oid: "c2", Parents: 1}}
			f.Reviews = []pr.Review{{ID: "r", State: pr.ReviewCommented, Author: me, SubmittedAt: t0.Add(-reviewed), CommitOid: "c1"}}
		})
		w.workspace(wsdomain.Workspace{ID: "rev" + branch, Path: "/w/" + branch, DisplayName: "REVIEW " + branch, Branch: branch, LinkedPR: n})
		return r
	}
	recent, older := followup(3, time.Hour, "f3"), followup(9, 40*time.Hour, "f9")
	first := func(n int, created time.Duration) pr.Ref {
		return w.requested(n, func(row *dinv.PR, _ *pr.Facts) {
			row.HeadBranch, row.CreatedAt = "q"+string(rune('a'+n)), t0.Add(-created)
		})
	}
	newer, oldest := first(4, time.Hour), first(12, 100*time.Hour)
	address := func(n int, ws string) pr.Ref {
		r := w.add(n, me, func(row *dinv.PR, f *pr.Facts) {
			row.HeadBranch = ws
			f.Reviews = []pr.Review{{ID: "b", State: pr.ReviewChangesRequested, Author: "bob", SubmittedAt: t0.Add(-time.Hour), CommitOid: "head", HasContent: true}}
		})
		return r
	}
	second, firstWS := address(5, "a5"), address(13, "a13")
	w.workspace(wsdomain.Workspace{ID: "a13", Path: "/w/a13", DisplayName: "a13", Branch: "a13", LinkedPR: 13})
	w.workspace(wsdomain.Workspace{ID: "a5", Path: "/w/a5", DisplayName: "a5", Branch: "a5", LinkedPR: 5})

	p := w.provider()
	got, err := p.List(context.Background(), unit.GitHub)
	if err != nil {
		t.Fatal(err)
	}
	want := []unit.Unit{older.Unit(), recent.Unit(), oldest.Unit(), newer.Unit(), firstWS.Unit(), second.Unit()}
	if !slices.Equal(got[:len(want)], want) {
		t.Fatalf("list = %v, want %v first", got, want)
	}
	for path, u := range map[string]unit.Unit{"pr.review.action": older.Unit(), "pr.first.due": oldest.Unit(), "pr.address.due": firstWS.Unit()} {
		for _, v := range got {
			f := w.facts(t, p, v)
			if f[path] == true || f[path] == "follow-up" {
				if v != u {
					t.Errorf("%s: the one slot goes to %v, the base sent %v", path, v, u)
				}
				break
			}
		}
	}
}

// roundState is w's round state of u, as the host gives it to a plan.
func (w *world) roundState(u unit.Unit) src.RoundState {
	p := w.rounds.PRs[u.Key]
	day := w.rounds.Days[round.DayKey(w.now)]
	return src.RoundState{Marks: maps.Clone(p.Marks), SentAt: p.SentAt, AgentHome: p.AgentHome, Today: day.PerPR[u.Key], DayTotal: day.Total}
}

// planned is the round name p plans on u over w's round state.
func (w *world) planned(t *testing.T, p *factsgithub.Provider, u unit.Unit, name string) (src.Round, bool) {
	t.Helper()
	plan, err := p.Plan(context.Background(), plugin.Unit{Provider: string(u.Provider), Key: u.Key}, w.roundState(u))
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(plan.Rounds, func(r src.Round) bool { return r.Name == name })
	if i < 0 {
		return src.Round{}, false
	}
	return plan.Rounds[i], true
}

func TestTheFirstRoundPlanReadsTheHostsRoundState(t *testing.T) {
	w := newWorld()
	r := w.requested(3, nil)
	p := w.provider()
	first, ok := w.planned(t, p, r.Unit(), factsgithub.RoundFirst)
	if !ok || !maps.Equal(first.OnSkipped, map[string]string{round.FirstSkipped: "1"}) || len(first.OnSent) != 0 {
		t.Fatalf("first round %+v, %v", first, ok)
	}
	st := w.roundState(r.Unit())
	st.Marks = map[string]string{round.FirstSkipped: "1"}
	plan, err := p.Plan(context.Background(), plugin.Unit{Provider: string(r.Unit().Provider), Key: r.Unit().Key}, st)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Rounds) != 0 {
		t.Fatalf("a round state whose first round was skipped plans %+v", plan.Rounds)
	}
}

// Units are the pull requests the world holds, as the plugin reports them.
func (w *world) Units(_ context.Context, p unit.ProviderID) ([]unit.Unit, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []unit.Unit
	for r := range w.rows {
		out = append(out, r.Unit())
	}
	return out, nil
}

// Reported are the plugin's reports of units, derived from the world now.
func (w *world) Reported(_ context.Context, units []unit.Unit) (map[unit.Unit]src.Snapshot, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := map[unit.Unit]src.Snapshot{}
	for _, u := range units {
		r, ok := pr.RefOf(u)
		row, held := w.rows[r]
		if !ok || !held {
			continue
		}
		in := ghreporttest.Input{PR: row, Events: w.events[r], Adopted: w.overrides[r].Adopted, Tracked: w.overrides[r].Tracked}
		if f, ok := w.details[r]; ok {
			in.Facts = &f
		}
		cfg := w.cfg
		out[u] = ghreporttest.Snapshot(ghreporttest.Env{Viewer: w.viewer, Now: w.now, Stalled: &cfg.GitHub.Stalled, Teams: []string{}}, in)
	}
	return out, nil
}
