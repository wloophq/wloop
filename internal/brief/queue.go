package brief

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/core/lock"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	dpr "github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/tag"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
)

const (
	// Batch, Concurrency and CallTimeout bound what one run spends.
	Batch       = 5
	Concurrency = 2
	CallTimeout = 3 * time.Minute
	// RefreshFiles is how many files a new head must add before the brief
	// is made again.
	RefreshFiles = 5
	// BodyQuiet is how long an edited description must sit unchanged before
	// the background job briefs it again.
	BodyQuiet = 10 * time.Minute
	// maxBackoff caps the wait of a pull request that keeps failing.
	maxBackoff = 24 * time.Hour
)

// Outcomes of a brief run besides the store's running and interrupted.
const (
	OutcomeOK         = "ok"
	OutcomeFailed     = "failed"
	OutcomeUnfit      = "unfit"
	OutcomeOverBudget = "over-budget"
	OutcomeTooLong    = "too-long"
	OutcomeTimeout    = "timeout"
	// OutcomeReused is a run whose input hash matched the brief in use: no
	// diff read and no agent call.
	OutcomeReused = "reused"
	// OutcomeUnavailable is the agent unavailable, or GitHub refusing the
	// token or the budget.
	OutcomeUnavailable = "unavailable"
)

// Triggers of a brief run.
const (
	// TriggerBackground is a run the brief job started on its own.
	TriggerBackground = "background"
	// TriggerWanted is a run a first turn waits on.
	TriggerWanted = "wanted"
	// TriggerRefresh is a run a later turn started for a stale brief.
	TriggerRefresh = "refresh"
	// TriggerTrial is the breaker's run that tells whether briefs work again.
	TriggerTrial = "trial"
	// TriggerFields is a run that fills a brief's missing extra fields,
	// reusing its core.
	TriggerFields = "fields"
)

type Store interface {
	BriefStates(ctx context.Context) ([]inventory.BriefState, error)
	PR(ctx context.Context, ref github.Ref) (dinv.PR, bool, error)
	BriefRecords(ctx context.Context, refs ...github.Ref) (map[github.Ref]dinv.BriefRecord, error)
	BriefRecord(ctx context.Context, ref github.Ref) (dinv.BriefRecord, bool, error)
	TagVocabulary(ctx context.Context, owner string, keys []string, limit int) (map[string][]tag.Count, error)
	UnitBrief(ctx context.Context, u unit.Unit) (inventory.UnitBrief, bool, error)
	RunStore
}

type RunStore interface {
	StartBriefRun(ctx context.Context, r dinv.BriefRun) (int64, error)
	EndBriefRun(ctx context.Context, r dinv.BriefRun, st dinv.BriefRecord) error
	InterruptBriefRuns(ctx context.Context, at time.Time) (int, error)
	RefreshBrief(ctx context.Context, ref github.Ref) error
	WantBrief(ctx context.Context, ref github.Ref, at time.Time) error
	ClearWanted(ctx context.Context, ref github.Ref) error
	RecordEvent(ctx context.Context, ev dinv.Event) error
}

// VocabularySize is how many values of each open tag key the prompt lists.
const VocabularySize = 30

type Deps struct {
	Store  Store
	Source interface {
		Facts(ctx context.Context, ref github.Ref) (Input, error)
		Diff(ctx context.Context, ref github.Ref, max int) (diff string, cut bool, err error)
	}
	Runner interface {
		Run(ctx context.Context, c Call) (Result, error)
	}
	// Enabled is brief.enabled: false answers every Ready without a brief.
	Enabled bool
	// Unavailable is why the queue cannot brief pull requests, such as no
	// agent to ask: their turns start without a brief, Runner may be nil
	// and the queue is never started. On-demand units are briefed all the
	// same.
	Unavailable string
	// Taxonomy is the bundled one until profiles exist; see the step 2 seam.
	Taxonomy func(ref github.Ref) string
	// Home is the agent home a pull request's brief runs with.
	Home         func(ref github.Ref) string
	Tier         turns.Tier
	MaxDiffBytes int
	// Retry is how long a background failure waits before the next try:
	// sweep.slow, doubled after each failure.
	Retry time.Duration
	// Allow is false while the GitHub budget refuses reads of that
	// priority; nil means fine.
	Allow func(dgh.Priority) bool
	// CallTimeout and WantedTimeout bound reading the input and the call
	// together, of a background and of a wanted run; zero means the
	// package's constant.
	CallTimeout, WantedTimeout time.Duration
	// DemandTimeout bounds a first turn's wait for an OnDemand source's
	// brief; zero is the package's.
	DemandTimeout time.Duration
	Now           func() time.Time
	// After is time.After unless set.
	After func(time.Duration) <-chan time.Time
	// Changes keeps Status's counts until a pull request, a brief or the
	// configuration changes, or the minute turns; nil reads every call.
	Changes port.Changes
	Log     func(format string, args ...any)
	// Intent is the brief intent of a provider's units as the profile
	// resolves it now; nil is the bundled one.
	Intent func(p unit.ProviderID) Intent
	// Records projects a pull request's brief record onto the tasks holding
	// it; nil projects none.
	Records *Records
	// Sources are the providers' BriefSources by provider. Pull requests
	// default to GitHubSource over Store and Source; a GitHub source set
	// here keeps Retry. A source that is also an OnDemand briefs its units
	// on a first turn's demand.
	Sources map[unit.ProviderID]BriefSource
}

// Intent is what the brief intent sets of a brief: the composed schema,
// the zero Composed being the core alone, and the prompt and the
// missing-fields prompt, empty for the bundled ones.
type Intent struct {
	Schema               Composed
	Prompt, FieldsPrompt string
}

func (q *Queue) intent() Intent {
	var in Intent
	if q.d.Intent != nil {
		in = q.d.Intent((dpr.Codec{}).Provider())
	}
	in.Prompt, in.FieldsPrompt = cmp.Or(in.Prompt, promptTemplate), cmp.Or(in.FieldsPrompt, fieldsTemplate)
	return in
}

type Queue struct {
	d        Deps
	mu       sync.Mutex
	versions map[string]string // by prompt and taxonomy
	inflight map[github.Ref]bool
	waiters  map[unit.Unit][]chan struct{}
	// srcMu guards Deps.Sources, which Register adds to.
	srcMu sync.RWMutex
	// demand is when a first turn first wanted an on-demand unit's brief,
	// and filling which pull requests' fields failed at which version.
	demand     map[unit.Unit]time.Time
	fillFailed map[github.Ref]string
	breaker    breaker
	// recent are the last two failures, the newest last.
	recent []failure
	// globalTries counts a wanted pull request's global failures on one
	// attempt_of, apart from its own Failures.
	globalTries map[github.Ref]tries
	// refreshSem bounds the refreshes later turns start, apart from
	// wantedSem, so slow refreshes never hold back a first turn's brief.
	refreshSem chan struct{}
	// wantedSem bounds the wanted and trial runs started outside
	// Run.
	wantedSem chan struct{}
	base      context.Context
	// state serializes the reads and writes of a pull request's wanted,
	// retry and give-up state.
	state lock.Mutex
	wg    sync.WaitGroup
	// wake and due hold one pending signal each.
	wake, dueCh chan struct{}
	// faultUntil holds the scheduler off after a run the store could not
	// record.
	faultUntil time.Time
	status     statusMemo
}

// statusInputs are the notes after which Status counts again.
const statusInputs = port.TopicBriefs | port.TopicPRs | port.TopicFollow | port.TopicConfig

type statusMemo struct {
	mu     sync.Mutex
	ok     bool
	gen    port.Generation
	minute time.Time
	counts Status
}

type failure struct {
	ref     github.Ref
	timeout bool
}

type tries struct {
	attemptOf string
	n         int
}

// WantedConcurrency bounds the runs started outside Run.
const WantedConcurrency = 2

func New(d Deps) *Queue {
	return &Queue{d: d, wake: make(chan struct{}, 1), dueCh: make(chan struct{}, 1), wantedSem: make(chan struct{}, WantedConcurrency), refreshSem: make(chan struct{}, WantedConcurrency)}
}

// Forget drops the brief in use and any give-up, so the next run makes it
// again; every run stays.
func (q *Queue) Forget(ctx context.Context, ref github.Ref) error {
	q.state.Lock()
	defer q.state.Unlock()
	return q.d.Store.RefreshBrief(ctx, ref)
}

// unwantPR drops the pull request's want of a brief, once no turn waits on
// it.
func (q *Queue) unwantPR(ctx context.Context, ref github.Ref) error {
	defer q.wakeUp()
	q.state.Lock()
	defer q.state.Unlock()
	return q.d.Store.ClearWanted(ctx, ref)
}

// Interrupted ends the runs a daemon that stopped mid-call left running.
func (q *Queue) Interrupted(ctx context.Context) error {
	n, err := q.d.Store.InterruptBriefRuns(ctx, q.d.Now())
	if n > 0 {
		q.d.Log("brief: %d runs cut short by the last stop", n)
	}
	return err
}

type Status struct {
	Ready     int    `json:"ready"`
	Pending   int    `json:"pending"`
	Failed    int    `json:"failed"`
	LastError string `json:"last_error,omitempty"`
	// PausedUntil is the breaker's next trial while it is open.
	PausedUntil time.Time `json:"paused_until,omitzero"`
}

type View struct {
	State string    `json:"state"`
	Brief *Brief    `json:"brief,omitempty"`
	Model string    `json:"model,omitempty"`
	At    time.Time `json:"at,omitzero"`
	Error string    `json:"error,omitempty"`
	// Stale marks a brief that is due again: the pull request moved on.
	Stale bool `json:"stale"`
}

// Due says whether the pull request needs a brief now: none yet, a failed
// one that changed or whose wait is over, or a brief the pull request moved
// past. A new prompt version alone is not due: the brief in use stays until
// a turn asks for it.
func Due(pr dinv.PR, rec dinv.BriefRecord, ok bool, version string, now time.Time, retry time.Duration) bool {
	return !tried(rec, ok) || GitHubSource{Retry: retry}.Stale(writtenOf(rec), AtOf(pr), version, now)
}

// isDue is Due through the pull request's BriefSource; without backoff, a
// failed attempt is due at once.
func (q *Queue) isDue(pr dinv.PR, rec dinv.BriefRecord, ok bool, version string, now time.Time, backoff bool) bool {
	if !tried(rec, ok) {
		return true
	}
	w := writtenOf(rec)
	if !backoff {
		w.AttemptedAt = time.Time{}
	}
	return q.source(pr.Ref.Unit().Provider).Stale(w, AtOf(pr), version, now)
}

// source is the BriefSource of p's units; a pull request without one in
// Deps.Sources has the GitHub rules with the queue's retry.
func (q *Queue) source(p unit.ProviderID) BriefSource {
	q.srcMu.RLock()
	s, ok := q.d.Sources[p]
	q.srcMu.RUnlock()
	if ok {
		return s
	}
	if p == (dpr.Codec{}).Provider() {
		return GitHubSource{Store: q.d.Store, Source: q.d.Source, MaxDiffBytes: q.d.MaxDiffBytes, Retry: q.d.Retry}
	}
	return nil
}

// writtenOf is what rec's brief was made from and how its last attempt went.
func writtenOf(rec dinv.BriefRecord) Written {
	return Written{At: AtOf(dinv.PR{HeadOid: rec.HeadOid, BodyEditedAt: rec.BodyEditedAt, ChangedFiles: rec.ChangedFiles}),
		Version: rec.Version, Error: rec.Error, Failures: rec.Failures, AttemptedAt: rec.AttemptedAt, AttemptOf: rec.AttemptOf}
}

// tried is a record with a brief in use or a failure counted: a refresh
// keeps the record and its last run but clears both.
func tried(rec dinv.BriefRecord, ok bool) bool {
	return ok && (rec.Run != 0 || rec.Failures > 0)
}

// attemptOf names what a brief is made from: a change in any of it is a
// new pull request to try.
func attemptOf(pr dinv.PR, version string) string { return AttemptKey(version, AtOf(pr)) }

// moved is a description edited BodyQuiet ago or more, or a head that added
// more than RefreshFiles files since the brief. A record without a file
// count was made before the inventory read it: the count is unknown, not
// zero.
func moved(pr dinv.PR, rec dinv.BriefRecord, now time.Time) bool {
	return filesMoved(pr, rec) || bodyEdited(pr, rec) && !now.Before(pr.BodyEditedAt.Add(BodyQuiet))
}

func bodyEdited(pr dinv.PR, rec dinv.BriefRecord) bool {
	return pr.BodyEditedAt.After(rec.BodyEditedAt)
}

func filesMoved(pr dinv.PR, rec dinv.BriefRecord) bool {
	return rec.ChangedFiles > 0 && pr.ChangedFiles-rec.ChangedFiles > RefreshFiles
}

// refreshDue is a brief a later turn or a view starts the refresh of: one
// Due says so, or one made at an older prompt version.
func (q *Queue) refreshDue(pr dinv.PR, rec dinv.BriefRecord, version string, now time.Time) bool {
	return q.isDue(pr, rec, true, version, now, true) || rec.Error == "" && rec.Version != version
}

// backoff doubles retry after each consecutive failure, up to maxBackoff.
func backoff(retry time.Duration, failures int) time.Duration {
	d := retry
	for i := 1; i < failures && d < maxBackoff; i++ {
		d *= 2
	}
	return min(d, max(retry, maxBackoff))
}

// ghUnavailable is a GitHub error no pull request gets past.
func ghUnavailable(err error) bool {
	return errors.Is(err, github.ErrUnauthorized) || errors.Is(err, dgh.ErrBudget)
}

// priority is what a run's GitHub reads spend: only a run a turn waits on
// keeps the user's reserve.
func priority(waiter bool) dgh.Priority {
	if waiter {
		return dgh.PriorityUser
	}
	return dgh.PriorityBackground
}

func (q *Queue) version(ref github.Ref) (taxonomy, version string) {
	taxonomy = q.d.Taxonomy(ref)
	q.mu.Lock()
	defer q.mu.Unlock()
	prompt := q.intent().Prompt
	key := prompt + "\x00" + taxonomy
	if v, ok := q.versions[key]; ok {
		return taxonomy, v
	}
	if q.versions == nil {
		q.versions = map[string]string{}
	}
	q.versions[key] = versionOf(prompt, taxonomy)
	return taxonomy, q.versions[key]
}

// Run briefs up to Batch due pull requests, Concurrency at a time, leaving
// out the wanted ones and the ones in flight.
func (q *Queue) Run(ctx context.Context) (string, error) {
	now := q.d.Now()
	if open, _, _, next := q.Breaker(); open {
		return "briefs are unavailable; the next trial is at " + next.Local().Format("15:04"), nil
	}
	if q.d.Allow != nil && !q.d.Allow(dgh.PriorityBackground) {
		return "waiting for the GitHub budget", nil
	}
	ctx = dgh.WithPriority(ctx, dgh.PriorityBackground)
	states, err := q.d.Store.BriefStates(ctx)
	if err != nil {
		return "", err
	}
	due := q.due(states, now)
	due = due[:min(len(due), Batch)]
	var (
		wg       sync.WaitGroup
		sem      = make(chan struct{}, Concurrency)
		mu       sync.Mutex
		ok, fail int
	)
	one := func(pr dinv.PR) {
		if !q.claimIf(ctx, pr.Ref, func(rec dinv.BriefRecord, has bool, version string) bool {
			return rec.WantedAt.IsZero() && q.isDue(pr, rec, has, version, q.d.Now(), true)
		}) {
			return
		}
		err := q.brief(ctx, pr, TriggerBackground, q.timeout(false))
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			fail++
			return
		}
		ok++
	}
	// The first call runs alone, so an outage costs one call.
	for i, st := range due {
		pr := st.PR
		if q.isOpen() {
			break
		}
		if i == 0 {
			one(pr)
			continue
		}
		sem <- struct{}{}
		if q.isOpen() {
			<-sem
			break
		}
		wg.Go(func() {
			defer func() { <-sem }()
			one(pr)
		})
	}
	wg.Wait()
	return fmt.Sprintf("%d briefed, %d failed", ok, fail), nil
}

// due lists the followed pull requests the background job briefs now, the
// ones that failed before last, so one that keeps failing is never the call
// that runs alone.
func (q *Queue) due(states []inventory.BriefState, now time.Time) []inventory.BriefState {
	var due, retry []inventory.BriefState
	for _, st := range states {
		if _, v := q.version(st.PR.Ref); !st.PR.Followed() || !st.Rec.WantedAt.IsZero() || q.inFlight(st.PR.Ref) || !q.isDue(st.PR, st.Rec, st.Has, v, now, true) {
			continue
		}
		if tried(st.Rec, true) && st.Rec.Error != "" {
			retry = append(retry, st)
		} else {
			due = append(due, st)
		}
	}
	return append(due, retry...)
}

func (q *Queue) timeout(wanted bool) time.Duration {
	if wanted {
		return cmp.Or(q.d.WantedTimeout, WantedTimeout)
	}
	return cmp.Or(q.d.CallTimeout, CallTimeout)
}

// brief records one run of the pull request's brief, whose claim the caller
// holds: started before the input is read, ended with its outcome, and the
// claim released after the end is stored. A run cut short by the daemon
// stopping stays running, for Interrupted to end.
func (q *Queue) brief(ctx context.Context, pr dinv.PR, trigger string, timeout time.Duration) error {
	defer q.release(pr.Ref)
	taxonomy, version := q.version(pr.Ref)
	run := dinv.BriefRun{PR: pr.Ref, Trigger: trigger, Version: version, AttemptOf: attemptOf(pr, version),
		HeadOid: pr.HeadOid, BodyEditedAt: pr.BodyEditedAt, ChangedFiles: pr.ChangedFiles}
	vocabulary, err := q.d.Store.TagVocabulary(ctx, pr.Ref.Owner, tag.OpenKeys(), VocabularySize)
	if err != nil {
		return q.storeFault(ctx, err)
	}
	settings := q.settings(vocabulary)
	run.StartedAt = q.d.Now()
	id, err := q.d.Store.StartBriefRun(ctx, run)
	if err != nil {
		return q.storeFault(ctx, err)
	}
	run.ID = id
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	in, sha, reuse, err := q.input(cctx, pr, version, settings)
	inputAt := q.d.Now()
	run.InputMS = ms(inputAt.Sub(run.StartedAt))
	gh := err != nil && ghUnavailable(err)
	var res Result
	run.InputSHA = sha
	switch {
	case err == nil && reuse != 0:
		run.Files, run.ReusedFrom = in.Paths, reuse
	case err == nil:
		run.Files, run.DiffCut = in.Paths, in.DiffCut
		if in.DiffBytes > 0 || in.DiffNote == "" {
			run.DiffBytes = &in.DiffBytes
		}
		bi := q.intent()
		res, err = q.d.Runner.Run(cctx, Call{Tier: q.d.Tier, Home: q.d.Home(pr.Ref), Schema: bi.Schema,
			Prompt: fill(bi.Prompt, taxonomy, tag.Vocabulary(tag.WithSeeds(vocabulary, VocabularySize)), in, nil)})
		if res.ExtraErr != nil {
			q.d.Log("brief %s: the extra fields are left missing: %v", pr.Ref, res.ExtraErr)
		}
		run.CallMS = ms(q.d.Now().Sub(inputAt))
		if res.Duration > 0 {
			run.AgentMS = ms(res.Duration)
		}
		run.Model, run.Tokens, run.CostUSD, run.DroppedTags = res.Model, res.Tokens, res.CostUSD, res.DroppedTags
		if err == nil {
			raw, _ := json.Marshal(res.Brief)
			run.Brief, run.Tags = string(raw), res.Brief.Tags
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	run.EndedAt, run.Outcome = q.d.Now(), OutcomeOK
	if reuse != 0 {
		run.Outcome = OutcomeReused
	}
	if err != nil {
		run.Outcome = outcome(err, cctx.Err())
		if gh {
			run.Outcome = OutcomeUnavailable
		}
		if run.Outcome == OutcomeTimeout {
			err = timedOut{after: timeout, err: err}
		}
		run.Error = err.Error()
	}
	q.state.Lock()
	defer q.state.Unlock()
	prev, _, serr := q.d.Store.BriefRecord(ctx, pr.Ref)
	if serr != nil {
		return q.storeFault(ctx, errors.Join(err, serr))
	}
	st, change := q.settle(prev, run, err)
	if serr := q.d.Store.EndBriefRun(ctx, run, st); serr != nil {
		return q.storeFault(ctx, errors.Join(err, serr))
	}
	if run.Outcome == OutcomeOK {
		if perr := q.d.Records.ProjectHeld(ctx, pr.Ref.Unit(), []byte(run.Brief), task.WriterAgent); perr != nil {
			q.d.Log("brief %s: the brief's properties: %v", pr.Ref, perr)
		}
	}
	q.recordBreaker(ctx, change, run)
	return err
}

// input reads the facts and the diff, both at once when a turn waits on
// the run, the diff canceled when the facts fail. A diff GitHub does not
// serve leaves a note in its place. A background run whose facts hash like
// the brief in use at version reads no diff and returns that brief's run
// in reuse. sha is the facts' hash, empty when they were not read.
func (q *Queue) input(ctx context.Context, pr dinv.PR, version, settings string) (in Input, sha string, reuse int64, err error) {
	ref := pr.Ref
	var (
		diff    string
		diffCut bool
		diffErr error
	)
	if dgh.PriorityOf(ctx) == dgh.PriorityUser {
		dctx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			diff, diffCut, diffErr = q.d.Source.Diff(dctx, ref, q.d.MaxDiffBytes)
		}()
		if in, err = q.d.Source.Facts(ctx, ref); err != nil {
			cancel()
		}
		<-done
		cancel()
	} else if in, err = q.d.Source.Facts(ctx, ref); err == nil {
		sha = InputSHA(pr.HeadOid, settings, in)
		if run, ok := q.reusable(ctx, ref, version, sha); ok {
			return in, sha, run, nil
		}
		diff, diffCut, diffErr = q.d.Source.Diff(ctx, ref, q.d.MaxDiffBytes)
	}
	if err == nil && sha == "" {
		sha = InputSHA(pr.HeadOid, settings, in)
	}
	switch {
	case err != nil:
		return Input{}, "", 0, err
	case ctx.Err() != nil:
		return Input{}, sha, 0, ctx.Err()
	case diffErr != nil && ghUnavailable(diffErr):
		return Input{}, "", 0, diffErr
	case diffErr != nil:
		in.DiffNote = " (unavailable: " + firstLine(diffErr.Error()) + ")"
		sha = ""
	case diffCut:
		in.Diff, in.DiffCut = diff, true
		in.DiffNote = fmt.Sprintf(" (the first %d bytes; the diff is longer)", len(diff))
	default:
		in.Diff, in.DiffBytes = diff, len(diff)
	}
	return in, sha, 0, nil
}

// settings names what besides the facts shapes a brief and is not in its
// version: the agent tier, the diff cap and the tag values on offer.
func (q *Queue) settings(vocabulary map[string][]tag.Count) string {
	var parts []string
	for _, k := range slices.Sorted(maps.Keys(vocabulary)) {
		var vals []string
		for _, c := range vocabulary[k] {
			vals = append(vals, c.Value)
		}
		slices.Sort(vals)
		parts = append(parts, k+"="+strings.Join(vals, ","))
	}
	return fmt.Sprint(q.d.Tier, "\x00", q.d.MaxDiffBytes, "\x00", strings.Join(parts, ";"))
}

// reusable is the run of ref's brief in use when it is at version and was
// made from input hashing to sha; a store error reads as not reusable.
func (q *Queue) reusable(ctx context.Context, ref github.Ref, version, sha string) (int64, bool) {
	rec, _, err := q.d.Store.BriefRecord(ctx, ref)
	if err != nil || rec.Run == 0 || rec.Brief == "" || rec.Version != version || rec.InputSHA != sha {
		return 0, false
	}
	return rec.Run, true
}

// settle is the pull request's state after run, and what it did to the
// breaker. A failure that belongs to the pull request counts toward it, and
// a wanted brief retries on WantedRetry or gives up. A global failure of a
// wanted brief retries on WantedRetry too, on a count of its own, and
// counts toward the breaker only once that schedule is spent.
func (q *Queue) settle(prev dinv.BriefRecord, run dinv.BriefRun, err error) (dinv.BriefRecord, breakerChange) {
	st := prev
	if prev.AttemptOf != run.AttemptOf {
		st.Failures, st.RetryAt, st.GaveUpAt, st.GaveUpReason = 0, time.Time{}, time.Time{}, ""
	}
	st.AttemptOf = run.AttemptOf
	if err == nil {
		st.Failures, st.RetryAt = 0, time.Time{}
		q.forgetTries(run.PR)
		return st, q.succeeded()
	}
	trial := run.Trigger == TriggerTrial
	wanted := !st.WantedAt.IsZero()
	global, now := q.classify(run.PR, err, run.Outcome)
	switch {
	case global:
		q.noteGlobal(err)
		if wanted && !now {
			if n := q.globalTry(run.PR, run.AttemptOf); n <= len(WantedRetry) {
				st.RetryAt = run.EndedAt.Add(WantedRetry[n-1])
				if !trial {
					return st, breakerKept
				}
			}
		}
		if trial {
			return st, q.trialFailed(run.EndedAt)
		}
		return st, q.globalFailure(run.EndedAt, now)
	case wanted:
		q.perPRFailure(run.PR, err, run.Outcome, prev.Error)
		st.Failures++
		st.RetryAt = time.Time{}
		if st.Failures >= MaxFailures || errors.Is(err, turns.ErrTooLong) || errors.Is(err, turns.ErrOverBudget) {
			st.GaveUpAt, st.GaveUpReason = run.EndedAt, gaveUp(st.Failures, err)
		} else {
			st.RetryAt = run.EndedAt.Add(WantedRetry[min(st.Failures, len(WantedRetry))-1])
		}
	default:
		q.perPRFailure(run.PR, err, run.Outcome, prev.Error)
		st.Failures++
	}
	if trial {
		return st, q.trialFailed(run.EndedAt)
	}
	return st, breakerKept
}

func gaveUp(attempts int, err error) string {
	noun := "attempts"
	if attempts == 1 {
		noun = "attempt"
	}
	return fmt.Sprintf("the brief gave up after %d %s: %s", attempts, noun, err)
}

// globalTry counts one more global failure of ref on attemptOf.
func (q *Queue) globalTry(ref github.Ref, attemptOf string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	t := q.globalTries[ref]
	if t.attemptOf != attemptOf {
		t = tries{attemptOf: attemptOf}
	}
	t.n++
	if q.globalTries == nil {
		q.globalTries = map[github.Ref]tries{}
	}
	q.globalTries[ref] = t
	return t.n
}

// triesOf is how many global failures ref had on attemptOf.
func (q *Queue) triesOf(ref github.Ref, attemptOf string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	if t := q.globalTries[ref]; t.attemptOf == attemptOf {
		return t.n
	}
	return 0
}

func (q *Queue) forgetTries(ref github.Ref) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.globalTries, ref)
}

// classify says whether err is global, no pull request getting past it,
// and whether it opens the breaker at once. A timeout is global when the
// two failures before it were timeouts of two other pull requests.
func (q *Queue) classify(ref github.Ref, err error, outcome string) (global, now bool) {
	switch {
	case errors.Is(err, turns.ErrNoAgent):
		return true, true
	case errors.Is(err, turns.ErrUnavailable), outcome == OutcomeUnavailable:
		return true, false
	case outcome == OutcomeTimeout:
		q.mu.Lock()
		defer q.mu.Unlock()
		if len(q.recent) < 2 {
			return false, false
		}
		a, b := q.recent[len(q.recent)-2], q.recent[len(q.recent)-1]
		if a.timeout && b.timeout && a.ref != ref && b.ref != ref && a.ref != b.ref {
			return true, true
		}
	}
	return false, false
}

// perPRFailure ends a run of global failures: the breaker counts
// consecutive ones.
func (q *Queue) perPRFailure(ref github.Ref, err error, outcome, previous string) {
	q.mu.Lock()
	q.recent = append(q.recent, failure{ref: ref, timeout: outcome == OutcomeTimeout})
	q.recent = q.recent[max(len(q.recent)-2, 0):]
	q.breaker.count = 0
	q.mu.Unlock()
	if err.Error() != previous {
		q.d.Log("brief %s: %v; retried later", ref, err)
	}
}

// outcome names a failed run; callErr is the call's own context error, so a
// run past its timeout reads as one even when gh only says it was killed.
func outcome(err, callErr error) string {
	switch {
	case errors.Is(err, turns.ErrOverBudget):
		return OutcomeOverBudget
	case errors.Is(err, turns.ErrTooLong):
		return OutcomeTooLong
	case errors.Is(err, ErrInvalid), errors.Is(err, turns.ErrUnfit):
		return OutcomeUnfit
	case errors.Is(err, turns.ErrUnavailable):
		return OutcomeUnavailable
	case errors.Is(err, context.DeadlineExceeded), errors.Is(callErr, context.DeadlineExceeded):
		return OutcomeTimeout
	}
	return OutcomeFailed
}

// timedOut is a run cut off at its timeout, whose own error only says the
// command was killed.
type timedOut struct {
	after time.Duration
	err   error
}

func (e timedOut) Error() string { return fmt.Sprintf("timed out after %s", e.after) }

func (e timedOut) Unwrap() error { return e.err }

func ms(d time.Duration) *int64 {
	v := d.Milliseconds()
	return &v
}

// Status counts the followed pull requests by brief state, read again only
// after a note of statusInputs or when the minute turns.
func (q *Queue) Status(ctx context.Context) (Status, error) {
	st, err := q.counts(ctx)
	if err != nil {
		return Status{}, err
	}
	open, _, lastErr, next := q.Breaker()
	st.LastError = lastErr
	if open {
		st.PausedUntil = next
	}
	return st, nil
}

func (q *Queue) counts(ctx context.Context) (Status, error) {
	m := &q.status
	m.mu.Lock()
	defer m.mu.Unlock()
	now := q.d.Now()
	minute := now.Truncate(time.Minute)
	var gen port.Generation
	if c := q.d.Changes; c != nil {
		if m.ok && m.minute.Equal(minute) && c.Since(m.gen, statusInputs).Empty() {
			return m.counts, nil
		}
		gen = c.Gen()
	}
	states, err := q.d.Store.BriefStates(ctx)
	if err != nil {
		return Status{}, err
	}
	var st Status
	for _, s := range states {
		pr, rec, ok := s.PR, s.Rec, s.Has
		if !pr.Followed() {
			continue
		}
		_, version := q.version(pr.Ref)
		switch {
		case tried(rec, ok) && rec.Error != "":
			st.Failed++
		case ok && !q.isDue(pr, rec, ok, version, now, true):
			st.Ready++
		default:
			st.Pending++
		}
	}
	if q.d.Changes != nil {
		m.ok, m.gen, m.minute, m.counts = true, gen, minute, st
	}
	return st, nil
}

// Summary is what a row of the list shows of a brief.
type Summary struct {
	Kind          string `json:"kind"`
	Size          string `json:"size"`
	Risk          string `json:"risk"`
	Goal          string `json:"goal"`
	OpenQuestions int    `json:"open_questions"`
	// Tags are the brief's key:value tags, sorted.
	Tags []string `json:"tags"`
	// Extra are the brief intent's extra fields the brief holds, by name.
	Extra map[string]any `json:"extra,omitempty"`
}

// Briefed reads every brief record in one query, with the summary of each
// stored brief; a pull request whose brief is missing or does not fit the
// schema has no summary.
func (q *Queue) Briefed(ctx context.Context) (map[github.Ref]Summary, map[github.Ref]dinv.BriefRecord, error) {
	recs, err := q.d.Store.BriefRecords(ctx)
	if err != nil {
		return nil, nil, err
	}
	out := map[github.Ref]Summary{}
	for ref, rec := range recs {
		if sum, ok := summaryOf(rec); ok {
			out[ref] = sum
		}
	}
	return out, recs, nil
}

// summaryOf is the summary of a stored brief; false when the record holds
// none or it does not fit the schema.
func summaryOf(rec dinv.BriefRecord) (Summary, bool) {
	var b Brief
	if rec.Brief == "" || json.Unmarshal([]byte(rec.Brief), &b) != nil || b.validate() != nil {
		return Summary{}, false
	}
	return Summary{Kind: b.Kind, Size: b.Size, Risk: b.Risk, Goal: b.Goal, OpenQuestions: len(b.OpenQuestions), Tags: slices.Sorted(slices.Values(b.Tags))}, true
}

// AttemptOf is Attempt with the pull request and its brief record already
// read.
func (q *Queue) AttemptOf(pr dinv.PR, rec dinv.BriefRecord) int {
	_, version := q.version(pr.Ref)
	return q.attempt(pr.Ref, rec, attemptOf(pr, version))
}

// Views are what the detail panel shows for each of prs, by pull request.
func (q *Queue) Views(ctx context.Context, prs ...dinv.PR) (map[github.Ref]*View, error) {
	if len(prs) == 0 {
		return map[github.Ref]*View{}, nil
	}
	refs := make([]github.Ref, len(prs))
	for i, pr := range prs {
		refs[i] = pr.Ref
	}
	recs, err := q.d.Store.BriefRecords(ctx, refs...)
	if err != nil {
		return nil, err
	}
	out := make(map[github.Ref]*View, len(prs))
	for _, pr := range prs {
		rec, ok := recs[pr.Ref]
		out[pr.Ref] = q.view(pr, rec, ok)
	}
	return out, nil
}

func (q *Queue) view(pr dinv.PR, rec dinv.BriefRecord, ok bool) *View {
	if !tried(rec, ok) {
		return &View{State: "pending"}
	}
	v := &View{State: "pending", Model: rec.Model, At: rec.CreatedAt, Error: rec.Error}
	if rec.Brief != "" {
		var b Brief
		if err := json.Unmarshal([]byte(rec.Brief), &b); err == nil {
			b.Tags = slices.Sorted(slices.Values(b.Tags))
			v.Brief, v.State = &b, "ready"
		}
	}
	if rec.Error != "" {
		v.State = "failed"
	}
	_, version := q.version(pr.Ref)
	v.Stale = v.Brief != nil && (rec.Version != version || bodyEdited(pr, rec) || filesMoved(pr, rec))
	return v
}

// RefreshIfStale starts one refresh of pr's brief when it is stale and due;
// View alone never starts a run.
func (q *Queue) RefreshIfStale(ctx context.Context, pr dinv.PR) error {
	q.state.Lock()
	defer q.state.Unlock()
	rec, ok, err := q.d.Store.BriefRecord(ctx, pr.Ref)
	if err != nil || !ok || rec.Brief == "" {
		return err
	}
	_, version := q.version(pr.Ref)
	if q.refreshDue(pr, rec, version, q.d.Now()) && !q.isOpen() && q.claim(pr.Ref) {
		q.spawn(ctx, pr, TriggerRefresh, q.timeout(true), false)
	}
	return nil
}
