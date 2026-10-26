package brief

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/turns"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/usage"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

var t0 = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

type fakeSource struct {
	mu   sync.Mutex
	fail map[string]error // by ref
	// block makes Input wait for its context to end.
	block bool
	// entered, when set, receives once Input blocks.
	entered chan struct{}
	// in, when set, is returned with the ref as its title.
	in    *Input
	takes func()
	// hold, by ref, makes Input send on the channel when it starts and
	// wait to receive on it before it returns.
	hold map[string]chan struct{}
	// diff and diffCut are what Diff returns, with diffErr.
	diff    string
	diffCut bool
	diffErr error
	// diffCalled, when set, receives once Diff is called.
	diffCalled chan struct{}
	// diffBlock makes Diff wait for its context to end.
	diffBlock bool
	// facts and diffs are the priorities each call carried.
	facts, diffs []dgh.Priority
	// edits makes each Facts call return a body no earlier call returned.
	edits bool
}

func (f *fakeSource) Diff(ctx context.Context, _ github.Ref, _ int) (string, bool, error) {
	f.mu.Lock()
	f.diffs = append(f.diffs, dgh.PriorityOf(ctx))
	diffCalled, block, diff, cut, err := f.diffCalled, f.diffBlock, f.diff, f.diffCut, f.diffErr
	f.mu.Unlock()
	if diffCalled != nil {
		diffCalled <- struct{}{}
	}
	if block {
		<-ctx.Done()
		return "", false, ctx.Err()
	}
	return diff, cut, err
}

func (f *fakeSource) Facts(ctx context.Context, ref github.Ref) (Input, error) {
	f.mu.Lock()
	f.facts = append(f.facts, dgh.PriorityOf(ctx))
	err, block, in, takes, entered, hold, edits, facts := f.fail[ref.String()], f.block, f.in, f.takes, f.entered, f.hold[ref.String()], f.edits, f.facts
	f.mu.Unlock()
	if takes != nil {
		takes()
	}
	if hold != nil {
		hold <- struct{}{}
		<-hold
	}
	if block {
		if entered != nil {
			select {
			case entered <- struct{}{}:
			default:
			}
		}
		<-ctx.Done()
		return Input{}, errors.New("gh pr view: signal: killed")
	}
	out := Input{}
	if in != nil {
		out = *in
	}
	out.Title = ref.String()
	if edits {
		out.Body = fmt.Sprintf("edit %d", len(facts))
	}
	return out, err
}

type fakeRunner struct {
	mu      sync.Mutex
	calls   int
	prompts []string
	fail    map[string]error // by the title in the prompt
	// res, when set, is what every call returns, with a failure too.
	res   *Result
	takes func()
	// fields are what a missing-fields call answers, by name; asked are
	// the fields each call asked for.
	fields map[string]any
	asked  [][]string
}

func (f *fakeRunner) Run(_ context.Context, c Call) (Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.prompts = append(f.prompts, c.Prompt)
	f.asked = append(f.asked, c.Fields)
	if len(c.Fields) > 0 {
		out := map[string]any{}
		for _, n := range c.Fields {
			out[n] = f.fields[n]
		}
		return Result{Fields: out, CostUSD: ptr(0.002)}, nil
	}
	if f.takes != nil {
		f.takes()
	}
	res := Result{Brief: Brief{Goal: "g", Kind: "fix", Size: "s", Risk: "low"}, CostUSD: ptr(0.01)}
	if f.res != nil {
		res = *f.res
	}
	for title, err := range f.fail {
		if strings.Contains(c.Prompt, "Title: "+title+"\n") {
			res.Brief = Brief{}
			return res, err
		}
	}
	return res, nil
}

type rig struct {
	q    *Queue
	st   *inventory.Store
	src  *fakeSource
	run  *fakeRunner
	now  time.Time
	mu   sync.Mutex
	logs []string
	// changes is the store's change log; the queue reads it only once a
	// test sets Deps.Changes.
	changes *change.Log
}

func newRig(t *testing.T, n int) *rig {
	t.Helper()
	changes := change.NewLog(change.DefaultWindow, nil)
	stDB, err := sqlite.Open(filepath.Join(t.TempDir(), "inv.db"), changes)
	if err != nil {
		t.Fatal(err)
	}
	st := inventory.New(stDB)
	t.Cleanup(func() { stDB.Close() })
	for i := 1; i <= n; i++ {
		ref := github.Ref{Owner: "acme", Repo: "api", Number: i}
		if err := st.UpsertPR(context.Background(), dinv.PR{Ref: ref, State: "OPEN", HeadOid: "h1", ChangedFiles: 3, Reasons: []string{"repo"},
			UpdatedAt: t0.Add(-time.Duration(i) * time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	r := &rig{st: st, src: &fakeSource{fail: map[string]error{}}, run: &fakeRunner{fail: map[string]error{}}, now: t0, changes: changes}
	r.q = New(Deps{Store: st, Source: r.src, Runner: r.run, Taxonomy: func(github.Ref) string { return DefaultTaxonomy },
		Home: func(github.Ref) string { return "" }, Tier: turns.TierFast, MaxDiffBytes: 1000, Retry: time.Hour, Enabled: true,
		Now: func() time.Time { return r.now }, Log: func(f string, a ...any) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.logs = append(r.logs, fmt.Sprintf(f, a...))
		}})
	return r
}

func TestQueueBriefsAtMostOneBatchPerRun(t *testing.T) {
	r := newRig(t, Batch+2)
	ctx := context.Background()
	if _, err := r.q.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if r.run.calls != Batch {
		t.Fatalf("%d calls in the first run", r.run.calls)
	}
	r.q.Run(ctx)
	r.q.Run(ctx)
	if r.run.calls != Batch+2 {
		t.Fatalf("%d calls after three runs", r.run.calls)
	}
	st, _ := r.q.Status(ctx)
	if st.Ready != Batch+2 || st.Pending != 0 || st.Failed != 0 {
		t.Fatalf("%+v", st)
	}
}

func TestQueueRefreshesOnBodyEditOrManyNewFiles(t *testing.T) {
	r := newRig(t, 1)
	ctx := context.Background()
	r.q.Run(ctx)
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 1}
	pr, _, _ := r.st.PR(ctx, ref)
	rec, ok, _ := r.st.BriefRecord(ctx, ref)
	v := Version(DefaultTaxonomy)
	if Due(pr, rec, ok, v, t0, time.Hour) {
		t.Fatal("due right after its brief")
	}
	pr.ChangedFiles += RefreshFiles
	if Due(pr, rec, ok, v, t0, time.Hour) {
		t.Fatalf("due after %d new files", RefreshFiles)
	}
	pr.ChangedFiles++
	if !Due(pr, rec, ok, v, t0, time.Hour) {
		t.Fatalf("not due after %d new files", RefreshFiles+1)
	}
	pr.ChangedFiles = rec.ChangedFiles
	pr.BodyEditedAt = t0.Add(time.Minute)
	if Due(pr, rec, ok, v, t0, time.Hour) {
		t.Fatal("due before the description was quiet")
	}
	if !Due(pr, rec, ok, v, t0.Add(time.Minute+BodyQuiet), time.Hour) {
		t.Fatal("not due after the description changed")
	}
	if Due(dinv.PR{}, rec, ok, "b1-other", t0, time.Hour) {
		t.Fatal("due under a new version")
	}
}

func TestABriefFromBeforeTheFileCountIsNotRedoneForIt(t *testing.T) {
	r := newRig(t, 1)
	ctx := context.Background()
	r.q.Run(ctx)
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 1}
	pr, _, _ := r.st.PR(ctx, ref)
	rec, ok, _ := r.st.BriefRecord(ctx, ref)
	rec.ChangedFiles = 0
	pr.ChangedFiles = 40
	if Due(pr, rec, ok, Version(DefaultTaxonomy), t0, time.Hour) {
		t.Fatal("a brief that never knew its file count is redone when the count arrives")
	}
}

func TestQueueStopsWhileTheBreakerIsOpen(t *testing.T) {
	r := newRig(t, 3)
	ctx := context.Background()
	r.run.fail["acme/api#1"] = fmt.Errorf("claude: %w", turns.ErrNoAgent)
	r.q.Run(ctx)
	if r.run.calls != 1 {
		t.Fatalf("%d calls after the agent was missing", r.run.calls)
	}
	r.now = t0.Add(14 * time.Minute)
	if summary, _ := r.q.Run(ctx); r.run.calls != 1 || !strings.Contains(summary, "unavailable") {
		t.Fatalf("ran while open: %d calls, %q", r.run.calls, summary)
	}
	st, _ := r.q.Status(ctx)
	if !strings.Contains(st.LastError, "not on the PATH") || !st.PausedUntil.Equal(t0.Add(BreakerCool)) {
		t.Fatalf("%+v", st)
	}
	delete(r.run.fail, "acme/api#1")
	r.now = t0.Add(16 * time.Minute)
	r.tick(ctx)
	r.q.Run(ctx)
	if st, _ := r.q.Status(ctx); st.Ready != 3 || st.LastError != "" || !st.PausedUntil.IsZero() {
		t.Fatalf("after the trial: %+v", st)
	}
}

func TestQueueRetriesAnInvalidAnswerAfterRetry(t *testing.T) {
	r := newRig(t, 2)
	ctx := context.Background()
	r.run.fail["acme/api#1"] = fmt.Errorf("%w: kind %q", ErrInvalid, "epic")
	r.q.Run(ctx)
	st, _ := r.q.Status(ctx)
	if st.Ready != 1 || st.Failed != 1 || !st.PausedUntil.IsZero() {
		t.Fatalf("an invalid answer paused the queue or stopped the other: %+v", st)
	}
	calls := r.run.calls
	r.now = t0.Add(30 * time.Minute)
	r.q.Run(ctx)
	if r.run.calls != calls {
		t.Fatal("retried before Retry")
	}
	r.now = t0.Add(61 * time.Minute)
	r.q.Run(ctx)
	if r.run.calls != calls+1 {
		t.Fatal("did not retry after Retry")
	}
}

func TestQueueLogsAFailureOnce(t *testing.T) {
	r := newRig(t, 1)
	ctx := context.Background()
	r.run.fail["acme/api#1"] = turns.ErrNoAgent
	for i := range 3 {
		r.now = t0.Add(time.Duration(i) * 2 * time.Hour)
		r.q.Run(ctx)
	}
	n := 0
	for _, l := range r.logs {
		if strings.Contains(l, turns.ErrNoAgent.Error()) {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("logged %d times: %v", n, r.logs)
	}
}

func TestQueueKeepsTheLastGoodBriefOnFailure(t *testing.T) {
	r := newRig(t, 1)
	r.src.edits = true
	ctx := context.Background()
	r.q.Run(ctx)
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 1}
	pr, _, _ := r.st.PR(ctx, ref)
	pr.BodyEditedAt = t0.Add(time.Minute)
	r.st.UpsertPR(ctx, pr)
	r.run.fail["acme/api#1"] = fmt.Errorf("%w: no goal", ErrInvalid)
	r.now = t0.Add(time.Minute + BodyQuiet)
	r.q.Run(ctx)
	v, err := viewOf(ctx, r.q, pr)
	if err != nil || v.State != "failed" || v.Brief == nil || v.Brief.Goal != "g" || !v.Stale {
		t.Fatalf("%+v %v", v, err)
	}
}

func TestQueueSkipsOutOfScopeAndWaitsForTheBudget(t *testing.T) {
	r := newRig(t, 1)
	ctx := context.Background()
	out := github.Ref{Owner: "acme", Repo: "api", Number: 50}
	r.st.UpsertPR(ctx, dinv.PR{Ref: out, State: "OPEN"})
	low := true
	r.q.d.Allow = func(p dgh.Priority) bool { return p == dgh.PriorityUser || !low }
	if summary, _ := r.q.Run(ctx); r.run.calls != 0 || !strings.Contains(summary, "budget") {
		t.Fatalf("ran on a low budget: %q", summary)
	}
	low = false
	r.q.Run(ctx)
	if _, ok, _ := r.st.BriefRecord(ctx, out); ok || r.run.calls != 1 {
		t.Fatalf("briefed a pull request out of scope, or not the one in it: %d calls", r.run.calls)
	}
}

func TestQueueLogsAPullRequestsSameFailureOnce(t *testing.T) {
	r := newRig(t, 1)
	ctx := context.Background()
	r.run.fail["acme/api#1"] = fmt.Errorf("%w: kind %q", ErrInvalid, "epic")
	for i := range 3 {
		r.now = t0.Add(time.Duration(i) * 2 * time.Hour)
		r.q.Run(ctx)
	}
	if r.run.calls != 3 || len(r.logs) != 1 {
		t.Fatalf("%d calls, logs %v", r.run.calls, r.logs)
	}
}

func TestQueueDoesNotLetOneUnreadablePullRequestStarveTheOthers(t *testing.T) {
	const n = 7
	r := newRig(t, n+1)
	ctx := context.Background()
	r.src.fail["acme/api#1"] = errors.New("gh pr view: GraphQL: Could not resolve to a PullRequest with the number of 1.")
	_, tooLong := AskRunner{Asker: &fakeAsker{err: fmt.Errorf("%w: claude: Prompt is too long", turns.ErrTooLong)}}.Run(ctx, Call{})
	r.run.fail["acme/api#2"] = tooLong
	runs := (n+Batch-1)/Batch + 1
	for i := range runs {
		r.now = t0.Add(time.Duration(i) * 61 * time.Minute)
		r.q.Run(ctx)
	}
	st, _ := r.q.Status(ctx)
	if st.Ready != n-1 || st.Failed != 2 || !st.PausedUntil.IsZero() || st.LastError != "" {
		t.Fatalf("after %d runs: %+v", runs, st)
	}
}

func TestQueueTriesFreshPullRequestsBeforeFailedOnes(t *testing.T) {
	r := newRig(t, Batch+1)
	ctx := context.Background()
	for i := 1; i <= Batch; i++ {
		r.run.fail[fmt.Sprintf("acme/api#%d", i)] = fmt.Errorf("%w: kind %q", ErrInvalid, "epic")
	}
	r.q.Run(ctx)
	r.now = t0.Add(2 * time.Hour)
	r.q.Run(ctx)
	if rec, _, _ := r.st.BriefRecord(ctx, github.Ref{Owner: "acme", Repo: "api", Number: Batch + 1}); rec.Brief == "" {
		t.Fatalf("the failed pull request went ahead of a fresh one: %+v", rec)
	}
}

func TestQueueBacksOffAPullRequestThatKeepsFailing(t *testing.T) {
	r := newRig(t, 1)
	ctx := context.Background()
	r.run.fail["acme/api#1"] = fmt.Errorf("%w: kind %q", ErrInvalid, "epic")
	for h := range 8 {
		r.now = t0.Add(time.Duration(h)*time.Hour + time.Minute)
		r.q.Run(ctx)
	}
	// Attempts at 0h, 1h, 3h and 7h: the wait doubles after each failure.
	if r.run.calls != 4 {
		t.Fatalf("%d calls in 7 hours", r.run.calls)
	}
	delete(r.run.fail, "acme/api#1")
	r.now = t0.Add(16 * time.Hour)
	r.q.Run(ctx)
	if rec, _, _ := r.st.BriefRecord(ctx, github.Ref{Owner: "acme", Repo: "api", Number: 1}); rec.Failures != 0 || rec.Brief == "" {
		t.Fatalf("a success did not reset the count: %+v", rec)
	}
}

func TestQueueClearsTheGlobalErrorOnASuccess(t *testing.T) {
	r := newRig(t, 1)
	ctx := context.Background()
	r.run.fail["acme/api#1"] = fmt.Errorf("%w: claude: Credit balance is too low", turns.ErrUnavailable)
	r.q.Run(ctx)
	if st, _ := r.q.Status(ctx); !strings.Contains(st.LastError, "Credit balance") || !st.PausedUntil.IsZero() {
		t.Fatalf("one global failure: %+v", st)
	}
	delete(r.run.fail, "acme/api#1")
	r.now = t0.Add(time.Minute)
	r.q.Run(ctx)
	if st, _ := r.q.Status(ctx); st.LastError != "" || st.Ready != 1 {
		t.Fatalf("%+v", st)
	}
}

func TestQueueSavesNothingWhenItsContextEnds(t *testing.T) {
	r := newRig(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	r.src.block, r.src.entered = true, make(chan struct{}, 1)
	go func() {
		<-r.src.entered
		cancel()
	}()
	if _, err := r.q.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := r.st.BriefRecord(context.Background(), github.Ref{Owner: "acme", Repo: "api", Number: 1}); ok || len(r.logs) != 0 {
		t.Fatalf("saved or logged on shutdown: %v", r.logs)
	}
	if st, _ := r.q.Status(context.Background()); !st.PausedUntil.IsZero() {
		t.Fatalf("paused on shutdown: %+v", st)
	}
}

func TestQueueBoundsTheInputByTheCallTimeout(t *testing.T) {
	r := newRig(t, 1)
	r.q.d.CallTimeout = 20 * time.Millisecond
	r.src.block = true
	r.q.Run(context.Background())
	rec, ok, _ := r.st.BriefRecord(context.Background(), github.Ref{Owner: "acme", Repo: "api", Number: 1})
	if !ok || rec.Error == "" {
		t.Fatalf("%+v", rec)
	}
	if st, _ := r.q.Status(context.Background()); !st.PausedUntil.IsZero() {
		t.Fatalf("a slow gh paused every brief: %+v", st)
	}
}

func TestStatusCountsAMovedPullRequestAsPending(t *testing.T) {
	r := newRig(t, 1)
	ctx := context.Background()
	r.q.Run(ctx)
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 1}
	pr, _, _ := r.st.PR(ctx, ref)
	pr.BodyEditedAt = t0.Add(time.Minute)
	r.st.UpsertPR(ctx, pr)
	r.now = t0.Add(time.Minute + BodyQuiet)
	if st, _ := r.q.Status(ctx); st.Ready != 0 || st.Pending != 1 {
		t.Fatalf("%+v", st)
	}
}

func TestQueueRetriesAFailedPullRequestAtOnceWhenItChanges(t *testing.T) {
	r := newRig(t, 1)
	ctx := context.Background()
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 1}
	r.run.fail["acme/api#1"] = fmt.Errorf("%w: kind %q", ErrInvalid, "epic")
	for h := range 4 {
		r.now = t0.Add(time.Duration(h)*time.Hour + time.Minute)
		r.q.Run(ctx)
	}
	calls := r.run.calls
	pr, _, _ := r.st.PR(ctx, ref)
	for _, change := range []func(*dinv.PR){
		func(p *dinv.PR) { p.HeadOid = "h2" },
		func(p *dinv.PR) { p.BodyEditedAt = r.now },
	} {
		change(&pr)
		r.st.UpsertPR(ctx, pr)
		r.now = r.now.Add(time.Minute)
		r.q.Run(ctx)
		if r.run.calls != calls+1 {
			t.Fatalf("a changed pull request waited for its backoff: %d calls", r.run.calls)
		}
		calls = r.run.calls
		if rec, _, _ := r.st.BriefRecord(ctx, ref); rec.Failures != 1 {
			t.Fatalf("the count did not restart on a change: %+v", rec)
		}
	}
	r.now = r.now.Add(time.Minute)
	r.q.Run(ctx)
	if r.run.calls != calls {
		t.Fatal("retried an unchanged pull request before its backoff")
	}
}

// saveRun stores one background run ended at once: ok with brief, else
// failed with errText.
func saveRun(t *testing.T, st *inventory.Store, ref github.Ref, brief, errText string) {
	t.Helper()
	ctx := context.Background()
	r := dinv.BriefRun{PR: ref, Trigger: TriggerBackground, Version: "b1-x", AttemptOf: "b1-x", StartedAt: t0}
	id, err := st.StartBriefRun(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	r.ID, r.EndedAt, r.Outcome, r.Brief, r.Error = id, t0, OutcomeOK, brief, errText
	rec := dinv.BriefRecord{AttemptOf: r.AttemptOf}
	if brief == "" {
		r.Outcome, rec.Failures = OutcomeFailed, 1
	}
	if err := st.EndBriefRun(ctx, r, rec); err != nil {
		t.Fatal(err)
	}
}

func saveTags(t *testing.T, st *inventory.Store, ref github.Ref, tags ...string) {
	t.Helper()
	ctx := context.Background()
	r := dinv.BriefRun{PR: ref, Trigger: TriggerBackground, Version: "b1-x", AttemptOf: "b1-x", StartedAt: t0}
	id, err := st.StartBriefRun(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	r.ID, r.EndedAt, r.Outcome, r.Brief, r.Tags = id, t0, OutcomeOK, `{}`, tags
	if err := st.EndBriefRun(ctx, r, dinv.BriefRecord{AttemptOf: r.AttemptOf}); err != nil {
		t.Fatal(err)
	}
}

func TestPromptCarriesTheOwnersVocabulary(t *testing.T) {
	r := newRig(t, 1)
	saveTags(t, r.st, github.Ref{Owner: "acme", Repo: "web", Number: 1}, "lang:go", "domain:billing", "change:fix")
	for i := range 3 {
		saveTags(t, r.st, github.Ref{Owner: "other", Repo: "x", Number: i}, "lang:rust")
	}
	r.q.Run(context.Background())
	if len(r.run.prompts) != 1 {
		t.Fatalf("%d calls", r.run.calls)
	}
	p := r.run.prompts[0]
	if !strings.Contains(p, "Reuse one of these spellings when it fits") || !strings.Contains(p, "- lang: go (1), rust (3), python, typescript,") || !strings.Contains(p, "- tool: docker, kubernetes,") || !strings.Contains(p, "- domain: billing (1)\n") ||
		strings.Contains(p, "change: fix") {
		t.Fatalf("prompt:\n%s", p)
	}
}

func TestAnOKRunStoresItsTags(t *testing.T) {
	r := newRig(t, 1)
	r.run.res = &Result{Brief: Brief{Goal: "g", Kind: "fix", Size: "s", Risk: "low", Tags: []string{"lang:go", "change:fix"}}, DroppedTags: []string{"lang:3d"}}
	r.q.Run(context.Background())
	runs := runsOf(t, r.st)
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 1}
	tags, err := r.st.Tags(context.Background(), ref)
	if err != nil || len(runs) != 1 || !slices.Equal(runs[0].Tags, []string{"lang:go", "change:fix"}) || !slices.Equal(runs[0].DroppedTags, []string{"lang:3d"}) ||
		!slices.Equal(tags, []string{"change:fix", "lang:go"}) {
		t.Fatalf("runs %+v, tags %v %v", runs, tags, err)
	}
}

func TestSummariesKeepOnlyBriefsThatFitTheSchema(t *testing.T) {
	r := newRig(t, 0)
	ctx := context.Background()
	ref := func(n int) github.Ref { return github.Ref{Owner: "acme", Repo: "api", Number: n} }
	saveRun(t, r.st, ref(1), `{"goal":"Export in batches","kind":"feature","size":"m","risk":"high","open_questions":["a","b"],"tags":["lang:go","change:feature"]}`, "")
	saveRun(t, r.st, ref(2), "", "claude is not on the PATH")
	saveRun(t, r.st, ref(3), `{"goal":"g","kind":"fix","size":"huge","risk":"low"}`, "")
	saveRun(t, r.st, ref(4), `not json`, "")
	saveRun(t, r.st, ref(1), "", "a later failure keeps the brief")
	got, recs, err := r.q.Briefed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := Summary{Kind: "feature", Size: "m", Risk: "high", Goal: "Export in batches", OpenQuestions: 2, Tags: []string{"change:feature", "lang:go"}}
	if len(got) != 1 || !reflect.DeepEqual(got[ref(1)], want) {
		t.Fatalf("%+v", got)
	}
	if len(recs) != 4 || recs[ref(2)].Error != "claude is not on the PATH" {
		t.Fatalf("records %+v", recs)
	}
	v, err := viewOf(ctx, r.q, dinv.PR{Ref: ref(1)})
	if err != nil || v.Brief == nil || !slices.Equal(v.Brief.Tags, want.Tags) {
		t.Fatalf("the detail's tags are not the row's order: %+v %v", v, err)
	}
}

func runsOf(t *testing.T, st *inventory.Store) []dinv.BriefRun {
	t.Helper()
	runs, err := st.BriefRuns(context.Background(), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	return runs
}

func TestForgetKeepsTheRuns(t *testing.T) {
	r := newRig(t, 1)
	ctx := context.Background()
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 1}
	r.q.Run(ctx)
	r.q.Run(ctx)
	if r.run.calls != 1 {
		t.Fatalf("%d calls before forgetting", r.run.calls)
	}
	if err := r.q.Forget(ctx, ref); err != nil {
		t.Fatal(err)
	}
	pr, _, _ := r.st.PR(ctx, ref)
	if v, err := viewOf(ctx, r.q, pr); err != nil || v.State != "pending" || v.Brief != nil {
		t.Fatalf("the brief is still in use: %+v %v", v, err)
	}
	if st, _ := r.q.Status(ctx); st.Pending != 1 || st.Ready != 0 {
		t.Fatalf("%+v", st)
	}
	r.q.Run(ctx)
	if r.run.calls != 2 {
		t.Fatalf("%d calls after forgetting", r.run.calls)
	}
	if runs := runsOf(t, r.st); len(runs) != 2 || runs[0].Outcome != OutcomeOK || runs[1].Outcome != OutcomeOK {
		t.Fatalf("runs %+v", runs)
	}
}

func TestForgetAFailedBriefRetriesAtOnce(t *testing.T) {
	r := newRig(t, 1)
	ctx := context.Background()
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 1}
	r.run.fail["acme/api#1"] = fmt.Errorf("%w: kind %q", ErrInvalid, "epic")
	r.q.Run(ctx)
	delete(r.run.fail, "acme/api#1")
	if err := r.q.Forget(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if st, _ := r.q.Status(ctx); st.Pending != 1 || st.Failed != 0 {
		t.Fatalf("%+v", st)
	}
	r.q.Run(ctx)
	if rec, _, _ := r.st.BriefRecord(ctx, ref); r.run.calls != 2 || rec.Brief == "" {
		t.Fatalf("%d calls, %+v", r.run.calls, rec)
	}
}

func TestRunRecordsTheTimings(t *testing.T) {
	r := newRig(t, 1)
	ctx := context.Background()
	r.src.in, r.src.diff = &Input{Paths: []string{"a.go", "b.go"}}, strings.Repeat("x", 4096)
	r.src.takes = func() { r.now = r.now.Add(1800 * time.Millisecond) }
	r.run.takes = func() { r.now = r.now.Add(11 * time.Second) }
	tokens := &usage.Tokens{Input: 12, Output: 340, CacheRead: 900, CacheWrite: 50}
	r.run.res = &Result{Brief: Brief{Goal: "g", Kind: "fix", Size: "s", Risk: "low"}, Model: "haiku", CostUSD: ptr(0.012), Duration: 10400 * time.Millisecond, Tokens: tokens}
	r.q.Run(ctx)
	runs := runsOf(t, r.st)
	if len(runs) != 1 {
		t.Fatalf("runs %+v", runs)
	}
	got, _, _ := r.st.BriefRun(ctx, runs[0].ID)
	pr, _, _ := r.st.PR(ctx, github.Ref{Owner: "acme", Repo: "api", Number: 1})
	v := Version(DefaultTaxonomy)
	want := dinv.BriefRun{ID: got.ID, PR: pr.Ref, Trigger: TriggerBackground, Version: v, AttemptOf: attemptOf(pr, v), HeadOid: "h1", ChangedFiles: 3,
		StartedAt: t0, EndedAt: t0.Add(12800 * time.Millisecond), InputMS: ptr[int64](1800), CallMS: ptr[int64](11000), AgentMS: ptr[int64](10400),
		Outcome: OutcomeOK, Model: "haiku", CostUSD: ptr(0.012), Tokens: tokens, DiffBytes: ptr(4096),
		InputSHA: InputSHA("h1", r.q.settings(nil), Input{Title: pr.Ref.String()}),
		Brief:    `{"goal":"g","context":"","kind":"fix","size":"s","risk":"low","risk_why":"","open_questions":null,"tags":null}`, Files: []string{"a.go", "b.go"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
	if rec, ok, _ := r.st.BriefRecord(ctx, pr.Ref); !ok || rec.Run != got.ID || rec.LastRun != got.ID || rec.Failures != 0 {
		t.Fatalf("record %+v", rec)
	}
}

func ptr[T any](v T) *T { return &v }

func TestFailedRunRecordsTheOutcome(t *testing.T) {
	tooLong := func() error {
		_, err := AskRunner{Asker: &fakeAsker{err: fmt.Errorf("%w: claude: Prompt is too long", turns.ErrTooLong)}}.Run(context.Background(), Call{})
		return err
	}()
	for _, c := range []struct {
		name     string
		runErr   error
		inputErr error
		timeout  bool
		outcome  string
		called   bool
	}{
		{name: "invalid", runErr: fmt.Errorf("%w: kind %q", ErrInvalid, "epic"), outcome: OutcomeUnfit, called: true},
		{name: "unfit", runErr: fmt.Errorf("%w: claude: no structured output", turns.ErrUnfit), outcome: OutcomeUnfit, called: true},
		{name: "over budget", runErr: fmt.Errorf("%w: claude: Exceeded USD budget (0.25)", turns.ErrOverBudget), outcome: OutcomeOverBudget, called: true},
		{name: "too long", runErr: tooLong, outcome: OutcomeTooLong, called: true},
		{name: "agent error", runErr: errors.New("claude: Credit balance is too low"), outcome: OutcomeFailed, called: true},
		{name: "unreadable", inputErr: errors.New("gh pr view: HTTP 404"), outcome: OutcomeFailed},
		{name: "slow gh", timeout: true, outcome: OutcomeTimeout},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, 1)
			if c.runErr != nil {
				r.run.fail["acme/api#1"] = c.runErr
				r.run.res = &Result{Model: "haiku", CostUSD: ptr(0.25), Duration: time.Second}
			}
			if c.inputErr != nil {
				r.src.fail["acme/api#1"] = c.inputErr
			}
			if c.timeout {
				r.q.d.CallTimeout, r.src.block = 20*time.Millisecond, true
			}
			r.q.Run(context.Background())
			runs := runsOf(t, r.st)
			if len(runs) != 1 {
				t.Fatalf("runs %+v", runs)
			}
			got, _, _ := r.st.BriefRun(context.Background(), runs[0].ID)
			if got.Outcome != c.outcome || got.Error == "" || got.Brief != "" || got.EndedAt.IsZero() || got.InputMS == nil {
				t.Fatalf("%+v", got)
			}
			if c.called != (got.CallMS != nil) || c.called != (got.CostUSD != nil && *got.CostUSD == 0.25 && got.Model == "haiku") {
				t.Fatalf("called %v: %+v", c.called, got)
			}
			rec, ok, _ := r.st.BriefRecord(context.Background(), got.PR)
			if !ok || rec.Failures != 1 || rec.LastRun != got.ID || rec.Run != 0 || rec.Error != got.Error {
				t.Fatalf("record %+v", rec)
			}
		})
	}
}

func TestStoppedDaemonLeavesTheRunRunning(t *testing.T) {
	r := newRig(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	r.src.block, r.src.entered = true, make(chan struct{}, 1)
	go func() {
		<-r.src.entered
		cancel()
	}()
	r.q.Run(ctx)
	runs := runsOf(t, r.st)
	if len(runs) != 1 || runs[0].Outcome != "running" || !runs[0].EndedAt.IsZero() {
		t.Fatalf("runs %+v", runs)
	}
	if _, ok, _ := r.st.BriefRecord(context.Background(), runs[0].PR); ok {
		t.Fatal("a cut run set the pull request's state")
	}
}

func TestInterruptedMarksThem(t *testing.T) {
	r := newRig(t, 2)
	ctx, cancel := context.WithCancel(context.Background())
	r.src.block, r.src.entered = true, make(chan struct{}, 1)
	go func() {
		<-r.src.entered
		cancel()
	}()
	r.q.Run(ctx)
	r.src.block = false
	r.now = t0.Add(time.Hour)
	if err := r.q.Interrupted(context.Background()); err != nil {
		t.Fatal(err)
	}
	runs := runsOf(t, r.st)
	if len(runs) == 0 || slices.ContainsFunc(runs, func(run dinv.BriefRun) bool {
		return run.Outcome != "interrupted" || !run.EndedAt.Equal(r.now)
	}) {
		t.Fatalf("runs %+v", runs)
	}
	if err := r.q.Interrupted(context.Background()); err != nil || len(r.logs) != 1 {
		t.Fatalf("logs %v %v", r.logs, err)
	}
	r.q.Run(context.Background())
	if runs := runsOf(t, r.st); runs[len(runs)-1].Outcome != OutcomeOK {
		t.Fatalf("the next run did not brief: %+v", runs)
	}
}

func TestAnUnreportedCostStaysNull(t *testing.T) {
	r := newRig(t, 1)
	r.run.res = &Result{Brief: Brief{Goal: "g", Kind: "fix", Size: "s", Risk: "low"}}
	r.q.Run(context.Background())
	if runs := runsOf(t, r.st); len(runs) != 1 || runs[0].Outcome != OutcomeOK || runs[0].CostUSD != nil {
		t.Fatalf("runs %+v", runs)
	}
}

func TestUnavailableGitHubRecordsItsOutcome(t *testing.T) {
	r := newRig(t, BreakerAfter)
	for i := 1; i <= BreakerAfter; i++ {
		r.src.fail[fmt.Sprintf("acme/api#%d", i)] = fmt.Errorf("%w: GitHub HTTP 401: Bad credentials", github.ErrUnauthorized)
	}
	r.q.Run(context.Background())
	runs := runsOf(t, r.st)
	if len(runs) != BreakerAfter || slices.ContainsFunc(runs, func(run dinv.BriefRun) bool { return run.Outcome != OutcomeUnavailable || run.CallMS != nil }) || r.run.calls != 0 {
		t.Fatalf("%d calls, runs %+v", r.run.calls, runs)
	}
	if st, _ := r.q.Status(context.Background()); st.PausedUntil.IsZero() {
		t.Fatalf("an unavailable GitHub did not open the breaker: %+v", st)
	}
}

func viewOf(ctx context.Context, q *Queue, pr dinv.PR) (*View, error) {
	vs, err := q.Views(ctx, pr)
	return vs[pr.Ref], err
}

func TestABudgetRefusalMidRunIsUnavailable(t *testing.T) {
	for _, c := range []struct {
		name        string
		facts, diff error
	}{
		{name: "facts", facts: &dgh.BudgetError{Limit: dgh.Limit{Resource: dgh.ResourceGraphQL, Limit: 5000, Remaining: 100}}},
		{name: "diff", diff: &dgh.BudgetError{Limit: dgh.Limit{Resource: dgh.ResourceCore, Limit: 5000, Remaining: 100}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, 1)
			r.src.fail["acme/api#1"], r.src.diffErr = c.facts, c.diff
			r.q.Run(context.Background())
			runs := runsOf(t, r.st)
			if len(runs) != 1 || runs[0].Outcome != OutcomeUnavailable || r.run.calls != 0 {
				t.Fatalf("%d calls, runs %+v", r.run.calls, runs)
			}
		})
	}
}

func TestRunSkipsWhenTheBudgetRefusesBriefs(t *testing.T) {
	r := newRig(t, 2)
	var asked []dgh.Priority
	r.q.d.Allow = func(p dgh.Priority) bool {
		asked = append(asked, p)
		return false
	}
	ctx, sc := metertest.Start(t, context.Background())
	summary, err := r.q.Run(ctx)
	if err != nil || summary != "waiting for the GitHub budget" {
		t.Fatalf("summary %q, %v", summary, err)
	}
	if !slices.Equal(asked, []dgh.Priority{dgh.PriorityBackground}) || len(r.src.facts) != 0 || r.run.calls != 0 {
		t.Fatalf("asked %v, %d reads, %d calls", asked, len(r.src.facts), r.run.calls)
	}
	metertest.Within(t, sc, metertest.Budget{{Res: meter.SQL}: 0, {Res: meter.Tx}: 0})
}

func TestBackgroundRunsCarryPriorityBackground(t *testing.T) {
	r := newRig(t, 2)
	r.q.Run(context.Background())
	want := []dgh.Priority{dgh.PriorityBackground, dgh.PriorityBackground}
	if !slices.Equal(r.src.facts, want) || !slices.Equal(r.src.diffs, want) {
		t.Fatalf("facts %v, diffs %v", r.src.facts, r.src.diffs)
	}
}

func TestWantedRunsCarryPriorityUser(t *testing.T) {
	r := newRig(t, 1)
	r.src.diffCalled = make(chan struct{}, 1)
	r.src.takes = func() {
		select {
		case <-r.src.diffCalled:
		case <-time.After(5 * time.Second):
			t.Error("a wanted run read the diff after the facts")
		}
	}
	p, _, _ := r.st.PR(context.Background(), refN(1))
	if !r.q.claim(p.Ref) {
		t.Fatal("not claimed")
	}
	if err := r.q.brief(context.Background(), p, TriggerWanted, time.Minute); err != nil {
		t.Fatal(err)
	}
	want := []dgh.Priority{dgh.PriorityUser}
	if !slices.Equal(r.src.facts, want) || !slices.Equal(r.src.diffs, want) || r.run.calls != 1 {
		t.Fatalf("facts %v, diffs %v, %d calls", r.src.facts, r.src.diffs, r.run.calls)
	}
}

func TestAWantedRunCancelsTheDiffWhenTheFactsFail(t *testing.T) {
	r := newRig(t, 1)
	r.src.fail["acme/api#1"] = errors.New("GitHub HTTP 404: Not Found")
	r.src.diffCalled, r.src.diffBlock = make(chan struct{}, 1), true
	r.src.takes = func() { <-r.src.diffCalled }
	p, _, _ := r.st.PR(context.Background(), refN(1))
	if !r.q.claim(p.Ref) {
		t.Fatal("not claimed")
	}
	done := make(chan error, 1)
	go func() { done <- r.q.brief(context.Background(), p, TriggerWanted, time.Hour) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "404") || r.run.calls != 0 {
			t.Fatalf("err %v, %d calls", err, r.run.calls)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the diff outlived the failed facts")
	}
}
