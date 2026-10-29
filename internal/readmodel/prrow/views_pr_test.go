package prrow_test

import (
	"context"
	"slices"
	"testing"
	"time"

	facttask "github.com/wloophq/wloop/internal/adapter/facts/task"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
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

// lensViews are the lens views that replaced the GitHub plugin's Needs me, Stalled, In loop, Approved and Done.
var lensViews = []string{"needs-me", "stalled", "in-loop", "approved", "done"}

// prCase is one pull request as the GitHub plugin reports it in a snapshot, the host's state over it, what the
// host's pipeline makes of it for its task (the category of its state, who its items wait on, its open items and
// gates, its live runs), and the lens views that hold the task.
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
	// The host's overlays.
	turn    string // the state of a review or address turn that drives the row
	pending bool
	muted   bool
	// The task.
	category task.Category
	owes     []string
	items    int
	gates    int
	runs     int
	// lens are the lens views that hold the task.
	lens []string
}

// prCases are every situation of situation.Order, each at least once.
var prCases = []prCase{
	{name: "round pending", kind: situation.KindRoundPending, source: situation.KindReview, waitingOn: "me", pending: true,
		category: task.CategoryReady, owes: []string{"me"}, items: 1, gates: 1, lens: []string{"needs-me"}},
	{name: "agent asks", kind: situation.KindLoopWaiting, source: situation.KindReview, waitingOn: "me", review: true, turn: "asked",
		category: task.CategoryReady, owes: []string{"me"}, items: 1, gates: 1, runs: 1, lens: []string{"needs-me", "in-loop"}},
	{name: "agent stopped", kind: situation.KindLoopStopped, source: situation.KindReview, waitingOn: "me", review: true, turn: "failed",
		category: task.CategoryReady, owes: []string{"me"}, items: 1, lens: []string{"needs-me", "in-loop"}},
	{name: "round blocked", kind: situation.KindBlocked, source: situation.KindBlocked, waitingOn: "me",
		category: task.CategoryReady, owes: []string{"me"}, items: 1, gates: 1, lens: []string{"needs-me"}},
	{name: "to address", kind: situation.KindToAddress, source: situation.KindToAddress, waitingOn: "me", author: prViewer, workspace: true,
		category: task.CategoryReady, owes: []string{"me"}, items: 1, lens: []string{"needs-me"}},
	{name: "held by the cap", kind: situation.KindHeldByCap, source: situation.KindHeldByCap, waitingOn: "me", review: true,
		category: task.CategoryReady, owes: []string{"me"}, items: 1, gates: 1, lens: []string{"needs-me", "in-loop"}},
	{name: "agent working in a review workspace", kind: situation.KindLoopWorking, source: situation.KindReview, waitingOn: "me", review: true,
		turn: "started", category: task.CategoryReady, owes: []string{loopPerson}, items: 1, runs: 1, lens: []string{"in-loop"}},
	{name: "agent working in an author workspace", kind: situation.KindLoopWorking, source: situation.KindChanges, waitingOn: "me",
		author: prViewer, workspace: true, turn: "started", category: task.CategoryReady, owes: []string{loopPerson}, items: 1, runs: 1, lens: []string{"in-loop"}},
	{name: "changes asked", kind: situation.KindChanges, source: situation.KindChanges, waitingOn: "me", author: prViewer,
		category: task.CategoryReady, owes: []string{"me"}, items: 1, lens: []string{"needs-me"}},
	{name: "checks failing", kind: situation.KindChecks, source: situation.KindChecks, waitingOn: "me", author: prViewer,
		category: task.CategoryReady, owes: []string{"me"}, items: 1, lens: []string{"needs-me"}},
	{name: "merge conflicts", kind: situation.KindConflicts, source: situation.KindConflicts, waitingOn: "me", author: prViewer,
		category: task.CategoryReady, owes: []string{"me"}, items: 1, lens: []string{"needs-me"}},
	{name: "reply due", kind: situation.KindReply, source: situation.KindReply, waitingOn: "me",
		category: task.CategoryReady, owes: []string{"me"}, items: 1, lens: []string{"needs-me"}},
	{name: "reply due stalled", kind: situation.KindReply, source: situation.KindReply, waitingOn: "me", stalled: true,
		category: task.CategoryReady, owes: []string{"me"}, items: 1, lens: []string{"needs-me", "stalled"}},
	{name: "reviewer due", kind: situation.KindReviewerBack, source: situation.KindReviewerBack, waitingOn: "reviewer",
		category: task.CategoryWaiting, owes: []string{"github:bruno"}, items: 1},
	{name: "reviewer due stalled", kind: situation.KindReviewerBack, source: situation.KindReviewerBack, waitingOn: "reviewer", stalled: true,
		category: task.CategoryWaiting, owes: []string{"github:bruno"}, items: 1, lens: []string{"stalled"}},
	{name: "review asked of me", kind: situation.KindReview, source: situation.KindReview, waitingOn: "me",
		category: task.CategoryReady, owes: []string{"me"}, items: 1, lens: []string{"needs-me"}},
	{name: "review asked of me, stalled", kind: situation.KindReview, source: situation.KindReview, waitingOn: "me", stalled: true,
		category: task.CategoryReady, owes: []string{"me"}, items: 1, lens: []string{"needs-me", "stalled"}},
	{name: "review asked of another, stalled out of scope", kind: situation.KindReview, source: situation.KindReview, waitingOn: "reviewer",
		stalled: true, reasons: []string{}, category: task.CategoryWaiting, owes: []string{"github:bruno"}, items: 1},
	{name: "approved in my review workspace", kind: situation.KindReview, source: situation.KindReview, waitingOn: "reviewer", review: true, approved: true,
		category: task.CategoryWaiting, owes: []string{"github:bruno"}, items: 1, lens: []string{"in-loop", "approved"}},
	{name: "no reviewer on mine", kind: situation.KindNoReviewer, source: situation.KindNoReviewer, waitingOn: "author", author: prViewer,
		category: task.CategoryReady, owes: []string{"me"}, items: 1, lens: []string{"needs-me"}},
	{name: "mine can merge", kind: situation.KindMerge, source: situation.KindMerge, waitingOn: "me", author: prViewer, workspace: true,
		approved: true, category: task.CategoryReady, owes: []string{"me"}, items: 1, lens: []string{"needs-me", "approved"}},
	{name: "mine draft", kind: situation.KindDraft, source: situation.KindDraft, waitingOn: "author", author: prViewer, workspace: true,
		category: task.CategoryWaiting, owes: []string{}, items: 1},
	{name: "mine can merge, waiting on its author", kind: situation.KindMerge, source: situation.KindMerge, waitingOn: "author", author: prViewer,
		workspace: true, approved: true, category: task.CategoryReady, owes: []string{"me"}, items: 1, lens: []string{"needs-me", "approved"}},
	{name: "reply due on mine", kind: situation.KindReply, source: situation.KindReply, waitingOn: "author", author: prViewer,
		category: task.CategoryReady, owes: []string{"me"}, items: 1, lens: []string{"needs-me"}},
	{name: "a draft to approve on a review asked of another", kind: situation.KindReview, source: situation.KindReview, waitingOn: "reviewer",
		category: task.CategoryWaiting, owes: []string{"github:bruno"}, items: 1, gates: 1, lens: []string{"needs-me"}},
	{name: "muted review", kind: situation.KindMuted, source: situation.KindReview, waitingOn: "me", stalled: true, muted: true,
		category: task.CategoryDeferred, owes: []string{"me"}, items: 1},
	{name: "merged in a review workspace", kind: situation.KindDone, source: situation.KindDone, review: true, state: "MERGED",
		category: task.CategoryDone, owes: []string{}, lens: []string{"done"}},
	{name: "closed without a workspace", kind: situation.KindDone, source: situation.KindDone, state: "CLOSED", category: task.CategoryDone, owes: []string{}},
}

func TestLensMatchesTheGitHubViewForEverySituation(t *testing.T) {
	for _, k := range situation.Order {
		if !slices.ContainsFunc(prCases, func(c prCase) bool { return c.kind == k }) {
			t.Errorf("no case for %s", k)
		}
	}
	for _, v := range lensViews {
		if !slices.ContainsFunc(prCases, func(c prCase) bool { return slices.Contains(c.lens, v) }) {
			t.Errorf("no case lists a pull request in %s", v)
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

// lensMatches checks, for every case of situation k, that the lens views hold the pull request's task exactly when
// the case says.
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
			var got []string
			for _, v := range lensViews {
				in, err := catalog.ViewAt(l.Views, v, task.Task{}, f, prNow)
				if err != nil {
					t.Fatal(err)
				}
				if in {
					got = append(got, v)
				}
			}
			if !slices.Equal(got, c.lens) {
				t.Errorf("lens views %v, want %v (signals %v, owed by %v)", got, c.lens, prrow.Signals(row), f["task.owed_by"])
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
	if author == prViewer && c.source != situation.KindDone {
		views = append(views, "mine")
	}
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
		Plans: map[unit.Unit]overlay.Pending{}}
	if c.turn != "" {
		role := "pr.review"
		if !c.review {
			role = "pr.address"
		}
		d.Turns[ref] = prrow.TurnFact{Facts: overlay.Turn{Intent: "review", Role: role, State: c.turn, Summary: "a question", At: prNow.Add(-time.Minute)}}
	}
	if c.pending {
		d.Plans[ref.Unit()] = overlay.Pending{Intent: "review", Reason: "a new push", Since: prNow.Add(-time.Minute), Deadline: prNow.Add(time.Minute)}
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
	f := workflow.Facts{"task.category": string(c.category), "task.state": "", "task.gates_open": c.gates, "task.items_open": c.items, "task.link_owes": c.owes,
		"task.wake_until": time.Time{}, "task.wake_when": "", "task.check_on": time.Time{}, "task.since_movement": prNow.Sub(row.Summary.Since),
		"task.runs_live": c.runs}
	reported := []string{}
	for _, name := range builtin.LinkSignals {
		on, ok := prrow.Signals(row)[name]
		f[builtin.LinkSignalPrefix+name] = on
		if ok {
			reported = append(reported, name)
		}
	}
	f["task.link_signals"] = reported
	who, err := catalog.OwedByOf(func() *intentload.Loaded { return l })(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	f["task.owed_by"] = who
	if facttask.FallsBack(c.owes, prStaleDays) && who != "" {
		f["task.check_on"] = task.AddWorkdays(row.Summary.Since, prStaleDays, time.UTC)
	}
	return f
}

// prStaleDays is tasks.stale_waiting_days as the bundled configuration sets it.
const prStaleDays = 2
