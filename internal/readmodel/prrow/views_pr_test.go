package prrow_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/readmodel/overlay"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
	"github.com/wloophq/wloop/internal/situation"
	"github.com/wloophq/wloop/internal/turn"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// prNow is the instant the rows are derived and the lens views read as now.
var prNow = time.Date(2026, 3, 4, 15, 0, 0, 0, time.UTC)

const (
	prViewer = "me-login"
	// loopPerson is who a pull request the loop works on waits on, as the host's gh:follow item names it.
	loopPerson = "system:loop"
)

// lensPairs are the GitHub views the lens views replace: the lens view's id and the GitHub view's.
var lensPairs = []struct{ lens, github string }{
	{"needs-me", "needs-me"}, {"stalled", "stalled"}, {"in-loop", "loop"}, {"approved", "approved"}, {"done", "done"},
}

// prCase is one pull request as the GitHub plugin reports it in a snapshot, the host's state over it, and what
// the host's pipeline makes of it for its task: the category of its state, who its items wait on, its open gates
// and its live runs.
type prCase struct {
	name string
	// kind is the situation the host's row shows, which the case is about.
	kind situation.Kind
	// The report: the situation from the source alone, and the source's facts.
	source    situation.Kind
	waitingOn string
	stalled   bool
	author    string
	reasons   []string
	review    bool // a review workspace holds it
	workspace bool // an author workspace holds it
	approved  bool
	state     string
	// needsMe is the plugin's needs-me of the source's situation.
	needsMe bool
	// The host's overlays.
	turn    string // the state of a review or address turn that drives the row
	pending bool
	muted   bool
	// The task.
	category task.Category
	owes     []string
	gates    int
	runs     int
	// wider names the lens views that hold the task where the GitHub view leaves the pull request out, on
	// purpose, and why.
	wider map[string]string
}

// prCases are every situation of situation.Order, each at least once.
var prCases = []prCase{
	{name: "round pending", kind: situation.KindRoundPending, source: situation.KindReview, waitingOn: "me", needsMe: true, pending: true,
		category: task.CategoryReady, owes: []string{"me"}, gates: 1},
	{name: "agent asks", kind: situation.KindLoopWaiting, source: situation.KindReview, waitingOn: "me", needsMe: true, review: true, turn: "asked",
		category: task.CategoryReady, owes: []string{"me"}, gates: 1, runs: 1},
	{name: "agent stopped", kind: situation.KindLoopStopped, source: situation.KindReview, waitingOn: "me", needsMe: true, review: true, turn: "failed",
		category: task.CategoryReady, owes: []string{"me"}},
	{name: "round blocked", kind: situation.KindBlocked, source: situation.KindBlocked, waitingOn: "me", needsMe: true,
		category: task.CategoryReady, owes: []string{"me"}, gates: 1},
	{name: "to address", kind: situation.KindToAddress, source: situation.KindToAddress, waitingOn: "me", needsMe: true, author: prViewer, workspace: true,
		category: task.CategoryReady, owes: []string{"me"}},
	{name: "held by the cap", kind: situation.KindHeldByCap, source: situation.KindHeldByCap, waitingOn: "me", needsMe: true, review: true,
		category: task.CategoryReady, owes: []string{"me"}, gates: 1},
	{name: "agent working in a review workspace", kind: situation.KindLoopWorking, source: situation.KindReview, waitingOn: "me", needsMe: true, review: true,
		turn: "started", category: task.CategoryReady, owes: []string{loopPerson}, runs: 1},
	{name: "agent working in an author workspace", kind: situation.KindLoopWorking, source: situation.KindChanges, waitingOn: "me", needsMe: true,
		author: prViewer, workspace: true, turn: "started", category: task.CategoryReady, owes: []string{loopPerson}, runs: 1,
		wider: map[string]string{"in-loop": "a live run is the loop working, whichever workspace holds the pull request"}},
	{name: "changes asked", kind: situation.KindChanges, source: situation.KindChanges, waitingOn: "me", needsMe: true, author: prViewer,
		category: task.CategoryReady, owes: []string{"me"}},
	{name: "checks failing", kind: situation.KindChecks, source: situation.KindChecks, waitingOn: "me", needsMe: true, author: prViewer,
		category: task.CategoryReady, owes: []string{"me"}},
	{name: "merge conflicts", kind: situation.KindConflicts, source: situation.KindConflicts, waitingOn: "me", needsMe: true, author: prViewer,
		category: task.CategoryReady, owes: []string{"me"}},
	{name: "reply due", kind: situation.KindReply, source: situation.KindReply, waitingOn: "me", needsMe: true,
		category: task.CategoryReady, owes: []string{"me"}},
	{name: "reply due stalled", kind: situation.KindReply, source: situation.KindReply, waitingOn: "me", needsMe: true, stalled: true,
		category: task.CategoryReady, owes: []string{"me"}},
	{name: "reviewer due", kind: situation.KindReviewerBack, source: situation.KindReviewerBack, waitingOn: "reviewer",
		category: task.CategoryWaiting, owes: []string{"github:bruno"}},
	{name: "reviewer due stalled", kind: situation.KindReviewerBack, source: situation.KindReviewerBack, waitingOn: "reviewer", stalled: true,
		category: task.CategoryWaiting, owes: []string{"github:bruno"}},
	{name: "review asked of me", kind: situation.KindReview, source: situation.KindReview, waitingOn: "me", needsMe: true,
		category: task.CategoryReady, owes: []string{"me"}},
	{name: "review asked of me, stalled", kind: situation.KindReview, source: situation.KindReview, waitingOn: "me", needsMe: true, stalled: true,
		category: task.CategoryReady, owes: []string{"me"}},
	{name: "review asked of another, stalled out of scope", kind: situation.KindReview, source: situation.KindReview, waitingOn: "reviewer",
		stalled: true, reasons: []string{}, category: task.CategoryWaiting, owes: []string{"github:bruno"}},
	{name: "approved in my review workspace", kind: situation.KindReview, source: situation.KindReview, waitingOn: "reviewer", review: true, approved: true,
		category: task.CategoryWaiting, owes: []string{"github:bruno"}},
	{name: "no reviewer on mine", kind: situation.KindNoReviewer, source: situation.KindNoReviewer, waitingOn: "author", author: prViewer,
		category: task.CategoryReady, owes: []string{},
		wider: map[string]string{"needs-me": "your own pull request with nobody to review it asks your next step"}},
	{name: "mine can merge", kind: situation.KindMerge, source: situation.KindMerge, waitingOn: "me", needsMe: true, author: prViewer, workspace: true,
		approved: true, category: task.CategoryReady, owes: []string{"me"}},
	{name: "mine draft", kind: situation.KindDraft, source: situation.KindDraft, waitingOn: "author", author: prViewer, workspace: true,
		category: task.CategoryReady, owes: []string{},
		wider: map[string]string{"needs-me": "your own draft asks your next step"}},
	{name: "muted review", kind: situation.KindMuted, source: situation.KindReview, waitingOn: "me", needsMe: true, stalled: true, muted: true,
		category: task.CategoryDeferred, owes: []string{"me"}},
	{name: "merged in a review workspace", kind: situation.KindDone, source: situation.KindDone, review: true, state: "MERGED",
		category: task.CategoryDone, owes: []string{}},
	{name: "closed without a workspace", kind: situation.KindDone, source: situation.KindDone, state: "CLOSED", category: task.CategoryDone, owes: []string{},
		wider: map[string]string{"done": "the lens lists every done task; the GitHub view only those whose workspace remains"}},
}

func TestLensMatchesTheGitHubViewForEverySituation(t *testing.T) {
	for _, k := range situation.Order {
		if !slices.ContainsFunc(prCases, func(c prCase) bool { return c.kind == k }) {
			t.Errorf("no case for %s", k)
		}
	}
	seen := map[string]bool{}
	for _, c := range prCases {
		for _, p := range lensPairs {
			seen[p.lens] = seen[p.lens] || c.row(t).In(p.github)
		}
	}
	for _, p := range lensPairs {
		if !seen[p.lens] {
			t.Errorf("no case lists a pull request in %s", p.github)
		}
	}
}

func TestLensMatchesTheGitHubViewForRoundPending(t *testing.T) {
	lensMatches(t, situation.KindRoundPending)
}
func TestLensMatchesTheGitHubViewForLoopWaiting(t *testing.T) {
	lensMatches(t, situation.KindLoopWaiting)
}
func TestLensMatchesTheGitHubViewForLoopStopped(t *testing.T) {
	lensMatches(t, situation.KindLoopStopped)
}
func TestLensMatchesTheGitHubViewForBlocked(t *testing.T)   { lensMatches(t, situation.KindBlocked) }
func TestLensMatchesTheGitHubViewForToAddress(t *testing.T) { lensMatches(t, situation.KindToAddress) }
func TestLensMatchesTheGitHubViewForHeldByCap(t *testing.T) { lensMatches(t, situation.KindHeldByCap) }
func TestLensMatchesTheGitHubViewForLoopWorking(t *testing.T) {
	lensMatches(t, situation.KindLoopWorking)
}
func TestLensMatchesTheGitHubViewForChanges(t *testing.T)   { lensMatches(t, situation.KindChanges) }
func TestLensMatchesTheGitHubViewForChecks(t *testing.T)    { lensMatches(t, situation.KindChecks) }
func TestLensMatchesTheGitHubViewForConflicts(t *testing.T) { lensMatches(t, situation.KindConflicts) }
func TestLensMatchesTheGitHubViewForReply(t *testing.T)     { lensMatches(t, situation.KindReply) }
func TestLensMatchesTheGitHubViewForReviewerBack(t *testing.T) {
	lensMatches(t, situation.KindReviewerBack)
}
func TestLensMatchesTheGitHubViewForReview(t *testing.T) { lensMatches(t, situation.KindReview) }
func TestLensMatchesTheGitHubViewForNoReviewer(t *testing.T) {
	lensMatches(t, situation.KindNoReviewer)
}
func TestLensMatchesTheGitHubViewForMerge(t *testing.T) { lensMatches(t, situation.KindMerge) }
func TestLensMatchesTheGitHubViewForDraft(t *testing.T) { lensMatches(t, situation.KindDraft) }
func TestLensMatchesTheGitHubViewForMuted(t *testing.T) { lensMatches(t, situation.KindMuted) }
func TestLensMatchesTheGitHubViewForDone(t *testing.T)  { lensMatches(t, situation.KindDone) }

// lensMatches checks, for every case of situation k and every lens view that replaces a GitHub view, that the
// lens view holds the pull request's task exactly when the GitHub view lists its row, but where the case says
// the lens is wider.
func lensMatches(t *testing.T, k situation.Kind) {
	t.Helper()
	l := prBundled(t)
	for _, c := range prCases {
		if c.kind != k {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			row := c.row(t)
			if situation.Kind(row.Kind) != k {
				t.Fatalf("the fixture's row is %s, not %s", row.Kind, k)
			}
			f := c.facts(t, l, row)
			for _, p := range lensPairs {
				old := row.In(p.github)
				lens, err := catalog.ViewAt(l.Views, p.lens, task.Task{}, f, prNow)
				if err != nil {
					t.Fatal(err)
				}
				why, wider := c.wider[p.lens]
				switch {
				case wider && (old || !lens):
					t.Errorf("%s: GitHub %v, lens %v; the case says the lens alone holds it (%s)", p.lens, old, lens, why)
				case !wider && old != lens:
					t.Errorf("%s: GitHub view %s lists it %v, lens %v (signals %v, owed by %v)", p.lens, p.github, old, lens,
						prrow.Signals(row), f["task.owed_by"])
				}
			}
		})
	}
}

func prBundled(t *testing.T) *intentload.Loaded {
	t.Helper()
	l, err := turn.Bundled()
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func (c prCase) ref() pr.Ref {
	return pr.Ref{Owner: "acme", Repo: "api", Number: 1 + slices.IndexFunc(prCases, func(o prCase) bool { return o.name == c.name })}
}

// report is the case's snapshot, as the GitHub plugin reports it.
func (c prCase) report() prrow.Report {
	since := prNow.Add(-time.Hour)
	threshold := 72 * time.Hour
	if c.stalled {
		since = prNow.Add(-10 * 24 * time.Hour)
	}
	author := c.author
	if author == "" {
		author = "ana"
	}
	reasons := c.reasons
	if reasons == nil {
		reasons = []string{"review_requested"}
	}
	ws := api.Workspace{}
	switch {
	case c.review:
		ws = api.Workspace{ID: "ws-review", IsReview: true}
	case c.workspace:
		ws = api.Workspace{ID: "ws-author"}
	}
	state := c.state
	if state == "" {
		state = "OPEN"
	}
	views := []string{}
	add := func(v string, on bool) {
		if on {
			views = append(views, v)
		}
	}
	add("needs-me", c.needsMe)
	add("stalled", c.stalled && len(reasons) > 0)
	add("mine", author == prViewer && c.source != situation.KindDone)
	add("loop", ws.IsReview)
	add("approved", c.approved)
	add("done", c.source == situation.KindDone && ws.ID != "")
	return prrow.Report{
		PR:        dinv.PR{Ref: c.ref(), Title: c.name, Author: author, State: state, CreatedAt: since, Reasons: reasons},
		Situation: src.Situation{Kind: string(c.source), WaitingOn: c.waitingOn, Since: since, Reason: c.name, Stalled: src.After(since, threshold, src.Calendar)},
		Payload:   prrow.Payload{Author: author, Repo: "acme/api", Reasons: reasons, Workspace: ws, Approved: c.approved},
		Views:     views,
	}
}

// row is the case's row as the host derives it from the snapshot under its overlays.
func (c prCase) row(t *testing.T) api.Row {
	t.Helper()
	l := prBundled(t)
	ref := c.ref()
	d := prrow.Deriver{Viewer: prViewer, Now: prNow, Loaded: l, Mutes: map[unit.Unit]overlay.Mute{}, Turns: map[pr.Ref]prrow.TurnFact{},
		Pending: map[unit.Unit]round.Pending{}}
	if c.turn != "" {
		role := "pr.review"
		if !c.review {
			role = "pr.address"
		}
		d.Turns[ref] = prrow.TurnFact{Facts: overlay.Turn{Intent: "review", Role: role, State: c.turn, Summary: "a question", At: prNow.Add(-time.Minute)}}
	}
	if c.pending {
		d.Pending[ref.Unit()] = round.Pending{Intent: "review", Reason: "a new push", Since: prNow.Add(-time.Minute), Deadline: prNow.Add(time.Minute)}
	}
	if c.muted {
		d.Mutes[ref.Unit()] = overlay.Mute{UntilStateChange: true, At: prNow.Add(-time.Minute)}
	}
	r, p := d.Parts(c.report(), nil, dinv.Override{})
	return d.Seal(r, p)
}

// facts are the task's facts as the task facts provider reads them of the case's task, its link signals from
// the row the host derived.
func (c prCase) facts(t *testing.T, l *intentload.Loaded, row api.Row) workflow.Facts {
	t.Helper()
	f := workflow.Facts{"task.category": string(c.category), "task.state": "", "task.gates_open": c.gates, "task.link_owes": c.owes,
		"task.wake_until": time.Time{}, "task.wake_when": "", "task.check_on": time.Time{}, "task.since_movement": prNow.Sub(row.Summary.Since),
		"task.runs_live": c.runs}
	for _, name := range builtin.LinkSignals {
		f[builtin.LinkSignalPrefix+name] = prrow.Signals(row)[name]
	}
	who, err := catalog.OwedByOf(func() *intentload.Loaded { return l })(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	f["task.owed_by"] = who
	return f
}
