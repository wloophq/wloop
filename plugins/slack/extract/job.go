package extract

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/plugins/slack/config"
	slack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/host"
	"github.com/wloophq/wloop/plugins/slack/items"
	"github.com/wloophq/wloop/plugins/slack/store"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

const (
	JobName  = "extract:slack"
	JobEvery = 30 * time.Second
	// PassBudget is how long a pass starts new calls; a started call runs on, up to CallTimeout.
	PassBudget = 10 * time.Minute
	// CallTimeout bounds one extraction call.
	CallTimeout = 10 * time.Minute
	// JobTimeout bounds a pass: its budget, the last call it started, and the work after it.
	JobTimeout = PassBudget + CallTimeout + 2*time.Minute
)

// Skip texts of the job.
const (
	SkipOff      = "slack extraction: off"
	SkipNoAgent  = "slack extraction: no agent answers headless calls"
	SkipNoLedger = "slack extraction: no items ledger"
	SkipRunning  = "slack extraction: a pass is running"
)

const (
	changePage = 1000
	// sweepEvery is how often watched units are checked for an unwatch when nothing changed in them.
	sweepEvery = 5 * time.Minute
	// dmSpans is how many of a DM's latest open segments a run shows and may relabel.
	dmSpans = 2
	// maxBackoff bounds the wait before a failed target runs again.
	maxBackoff = time.Hour
	// gaveUpFor is how long health names the targets the job gave up on.
	gaveUpFor = 24 * time.Hour
)

// Ledger is the host's items ledger as the job applies its ops.
type Ledger interface {
	Apply(ctx context.Context, prov items.Provenance, ops []items.Op) (host.Result, error)
	Items(ctx context.Context, u unit.Unit) ([]items.Item, error)
}

// Clock is the job's clock.
type Clock interface{ Now() time.Time }

// Job is the plugin's job JobName: each pass reads the Slack changes since its cursor and
// extracts every unit whose changes are due, within the daily caps and the breaker.
type Job struct {
	Facts    slack.FactsReader
	Segments slack.Segments
	Writer   slack.SegmentWriter
	// DM reads a DM conversation's messages with ts at or after from ("" all), ascending.
	DM func(ctx context.Context, conv, from string) ([]slack.Message, error)
	// DMBefore reads the newest n messages of a DM whose anchor is before ts, ascending; nil shows no context.
	DMBefore func(ctx context.Context, conv, before string, n int) ([]slack.Message, error)
	Ledger   Ledger
	Watcher  slack.Watcher
	Asker    host.Asker
	Parse    ParseTarget
	Store    Store
	Clock    Clock
	Cfg      func() config.Config
	Log      func(format string, args ...any)
	// Briefed hears of each unit whose brief a pass wrote; nil hears none.
	Briefed func(u unit.Unit)
	// PutBrief keeps a unit's brief in the host, before the pass saves its
	// cursor, so a crash between them reads the messages again; nil keeps
	// none.
	PutBrief func(ctx context.Context, u unit.Unit, brief string, at time.Time) error
	// Extra is the brief intent's extra fields' schema, which the brief of
	// every unit carries; nil asks for none.
	Extra func(ctx context.Context) (Extra, error)
	// Prompts reads the extraction's prompts from the profile, once a pass.
	Prompts func(ctx context.Context) (*Prompts, error)

	// wantMu guards wants, the targets a first turn asked a brief of, and
	// waiting, the targets with changes left to read as the last run left
	// them; Want reads them while a run holds mu.
	wantMu  sync.Mutex
	wants   map[unit.Unit]bool
	waiting map[unit.Unit]bool

	mu     sync.Mutex
	loaded bool
	// repaired says the ledger's people were checked since the process started.
	repaired bool
	scanned  int64
	stored   int64
	swept    time.Time
	pending  map[unit.Unit]*pend
	seqs     map[unit.Unit]int64
	kinds    map[string]slack.ConversationKind
	brk      breaker
	// lastFailed is the target of the last failed call; an unfit answer feeds the breaker
	// only from another target, so one bad unit cannot open it.
	lastFailed unit.Unit
	// oks counts the successful calls, so a target's timeout can tell whether the agent answered
	// another target since the target's previous failure.
	oks int64

	gaveMu   sync.Mutex
	gaveUp   []gaveUp
	timeouts []gaveUp
}

type gaveUp struct {
	at  time.Time
	why string
}

// pend is a target's changes not extracted yet. A target is a thread unit, or a DM
// conversation keyed "<team>/<conv>".
type pend struct {
	conv           string
	dm             bool
	minSeq, maxSeq int64
	oldest, newest time.Time
	minTS          string
	fails          int // answer-dependent failures, toward giving up
	retries        int // every failure after a call, toward the backoff
	retryAt        time.Time
	// overCap is set once an escalated call ran over the per-call cap; later calls stay on the base tier.
	overCap   bool
	oksAtFail int64 // Job.oks at the last failure
}

// Warnings says the breaker is open, names the targets given up on in the last day and
// counts the calls that timed out in it when more than one did; passed to Metrics.Warn.
func (j *Job) Warnings() []string {
	out := j.brk.warnings()
	j.gaveMu.Lock()
	defer j.gaveMu.Unlock()
	now := j.now()
	j.gaveUp = slices.DeleteFunc(j.gaveUp, func(g gaveUp) bool { return now.Sub(g.at) >= gaveUpFor })
	if n := len(j.gaveUp); n > 0 {
		out = append(out, fmt.Sprintf("slack extraction: gave up on %d units in the last day (last: %s)", n, j.gaveUp[n-1].why))
	}
	j.timeouts = slices.DeleteFunc(j.timeouts, func(g gaveUp) bool { return now.Sub(g.at) >= gaveUpFor })
	if n := len(j.timeouts); n > 1 {
		out = append(out, fmt.Sprintf("slack extraction: %d calls timed out in the last day (last: %s)", n, j.timeouts[n-1].why))
	}
	return out
}

func (j *Job) now() time.Time {
	if j.Clock == nil {
		return time.Now()
	}
	return j.Clock.Now()
}

func (j *Job) logf(format string, args ...any) {
	if j.Log != nil {
		j.Log(format, args...)
	}
}

// Run is one pass.
func (j *Job) Run(ctx context.Context) (summary, skipped string, err error) {
	if !j.mu.TryLock() {
		return "", SkipRunning, nil
	}
	defer j.mu.Unlock()
	cfg := j.Cfg()
	ec := cfg.Slack.Extraction
	switch {
	case !ec.On():
		return "", SkipOff, nil
	case j.Asker == nil:
		return "", SkipNoAgent, nil
	case j.Ledger == nil:
		return "", SkipNoLedger, nil
	}
	tier, err := host.ParseTier(ec.TierName())
	if err != nil {
		return "", "", fmt.Errorf("plugins.slack.extraction.tier: %w", err)
	}
	team, viewer, err := j.Facts.Viewer(ctx)
	if err != nil {
		return "", "slack extraction: " + err.Error(), nil
	}
	if j.Prompts == nil {
		return "", "", errNoPrompts
	}
	prompts, err := j.Prompts(ctx)
	if err != nil {
		return "", "", err
	}
	now := j.now()
	if err := j.scan(ctx, team, now); err != nil {
		return "", "", err
	}
	j.noteWaiting()
	defer j.noteWaiting()
	var errs []error
	if err := j.repair(ctx); err != nil {
		errs = append(errs, err)
	}
	unwatched, err := j.sweep(ctx, cfg, now)
	if err != nil {
		errs = append(errs, err)
	}
	due := j.due(now, ec)
	var done, ops int
	for i, k := range due {
		if ctx.Err() != nil {
			break
		}
		why, err := j.capped(ctx, ec, now)
		if err != nil {
			errs = append(errs, err)
			break
		}
		if why == "" && !j.brk.allow(now) {
			why = "slack extraction: the breaker is open"
		}
		if why == "" && j.now().Sub(now) >= PassBudget {
			why = "slack extraction: the pass budget is spent"
		}
		if why != "" {
			skipped = fmt.Sprintf("%s; %d units wait", why, len(due)-i)
			break
		}
		p := j.pending[k]
		o := j.process(ctx, k, p, team, viewer, cfg, tier, prompts, now)
		unfit := errors.Is(o.callErr, host.ErrUnfit)
		timedOut := errors.Is(o.callErr, context.DeadlineExceeded) && ctx.Err() == nil
		if timedOut {
			j.gaveMu.Lock()
			j.timeouts = append(j.timeouts, gaveUp{at: j.now(), why: k.String()})
			j.gaveMu.Unlock()
		}
		switch {
		case ctx.Err() != nil:
		case o.callErr != nil:
			j.logf("slack extraction %s: %v", k, o.callErr)
			if !unfit || k != j.lastFailed {
				if ev := j.brk.failed(now, o.callErr); ev != "" {
					j.logf("slack extraction: %s: %v", ev, o.callErr)
				}
			}
			j.lastFailed = k
		case o.called:
			j.oks++
			j.lastFailed = unit.Unit{}
			if ev := j.brk.succeeded(); ev != "" {
				j.logf("slack extraction: %s", ev)
			}
		}
		if o.err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", k, o.err))
		}
		var bad *answerError
		if failed := cmp.Or(o.callErr, o.err); o.called && failed != nil && ctx.Err() == nil {
			// A timeout counts toward giving up only once the agent answered another target since
			// this one last failed; in an outage nothing does.
			healthy := p.retries > 0 && j.oks > p.oksAtFail
			j.backOff(k, p, ec, j.now(), failed, unfit || errors.As(o.err, &bad) || timedOut && healthy)
		}
		if o.done {
			done++
			ops += o.ops
		}
	}
	if err := j.saveScan(ctx, now); err != nil {
		errs = append(errs, err)
	}
	if len(due) > 0 || unwatched > 0 {
		summary = fmt.Sprintf("extracted %d of %d due units, %d ops, %d unwatched", done, len(due), ops, unwatched)
	}
	return summary, skipped, errors.Join(errs...)
}

// scan folds the changes after the scanned seq into pending. A change a target already
// extracted (seq at or below its cursor) is left out.
func (j *Job) scan(ctx context.Context, team string, now time.Time) error {
	if !j.loaded {
		st, err := j.Store.state(ctx, scanKey)
		if err != nil {
			return err
		}
		j.scanned, j.stored = st.LastSeq, st.LastSeq
		j.pending, j.seqs, j.kinds = map[unit.Unit]*pend{}, map[unit.Unit]int64{}, map[string]slack.ConversationKind{}
		j.loaded = true
	}
	for {
		cs, err := j.Facts.Changes(ctx, j.scanned, changePage)
		if err != nil {
			return err
		}
		if len(cs) == 0 {
			return nil
		}
		keys := make([]unit.Unit, len(cs))
		dms := make([]bool, len(cs))
		var unknown []unit.Unit
		for i, c := range cs {
			if keys[i], dms[i], err = j.targetOf(ctx, team, c); err != nil {
				return err
			}
			if _, ok := j.seqs[keys[i]]; !ok && !keys[i].IsZero() {
				j.seqs[keys[i]] = 0
				unknown = append(unknown, keys[i])
			}
		}
		if len(unknown) > 0 {
			rows, err := j.Store.DB.States(ctx, unknown)
			if err != nil {
				return err
			}
			for u, r := range rows {
				j.seqs[u] = r.LastSeq
			}
		}
		for i, c := range cs {
			k := keys[i]
			if k.IsZero() || c.Seq <= j.seqs[k] {
				continue
			}
			p := j.pending[k]
			if p == nil {
				p = &pend{conv: c.Conversation, dm: dms[i], minSeq: c.Seq, oldest: c.At, minTS: c.TS}
				j.pending[k] = p
			}
			p.maxSeq = max(p.maxSeq, c.Seq)
			if c.At.Before(p.oldest) {
				p.oldest = c.At
			}
			if c.At.After(p.newest) {
				p.newest = c.At
			}
			if slack.CompareTS(c.TS, p.minTS) < 0 {
				p.minTS = c.TS
			}
		}
		j.scanned = cs[len(cs)-1].Seq
		if len(cs) < changePage {
			return nil
		}
	}
}

// saveScan keeps, across restarts, the seq before the oldest pending change.
func (j *Job) saveScan(ctx context.Context, now time.Time) error {
	low := j.scanned
	for _, p := range j.pending {
		low = min(low, p.minSeq-1)
	}
	if low == j.stored {
		return nil
	}
	err := j.Store.DB.Save(ctx, store.ExtractionSave{States: map[unit.Unit]store.ExtractionState{scanKey: {LastSeq: low, At: now}}})
	if err == nil {
		j.stored = low
	}
	return err
}

// targetOf is the target a change belongs to; zero when its conversation is unknown. Any
// other error stops the scan before the change, so the next pass reads it again.
func (j *Job) targetOf(ctx context.Context, team string, c slack.Change) (unit.Unit, bool, error) {
	kind, ok := j.kinds[c.Conversation]
	if !ok {
		conv, err := j.Facts.Conversation(ctx, c.Conversation)
		if errors.Is(err, slack.ErrUnknownUnit) {
			j.logf("slack extraction: change %d left out: %v", c.Seq, err)
			return unit.Unit{}, false, nil
		}
		if err != nil {
			return unit.Unit{}, false, fmt.Errorf("conversation of change %d: %w", c.Seq, err)
		}
		kind = conv.Kind
		j.kinds[c.Conversation] = kind
	}
	if kind.DM() {
		return dmKey(team, c.Conversation), true, nil
	}
	return slack.ThreadUnit(team, c.Conversation, cmp.Or(c.ThreadTS, c.TS)), false, nil
}

func dmKey(team, conv string) unit.Unit {
	return unit.Unit{Provider: slack.Provider, Key: team + "/" + conv}
}

// due are the pending targets whose newest change is older than quiet, or whose oldest is
// older than max_wait, oldest first.
func (j *Job) due(now time.Time, ec config.SlackExtraction) []unit.Unit {
	var out []unit.Unit
	for k, p := range j.pending {
		if now.Before(p.retryAt) {
			continue
		}
		if now.Sub(p.newest) >= ec.QuietFor() || now.Sub(p.oldest) >= ec.MaxWaitFor() || j.wanted(k) {
			out = append(out, k)
		}
	}
	slices.SortFunc(out, func(a, b unit.Unit) int {
		if c := j.pending[a].oldest.Compare(j.pending[b].oldest); c != 0 {
			return c
		}
		return strings.Compare(a.Key, b.Key)
	})
	return out
}

type outcome struct {
	called, done bool
	ops          int
	callErr, err error
}

// process extracts one target. Its cursor, brief and verdict are saved only after its ops
// were recorded, so a failure anywhere leaves it pending for the next pass.
func (j *Job) process(ctx context.Context, k unit.Unit, p *pend, team, viewer string, cfg config.Config, tier host.Tier,
	prompts *Prompts, now time.Time) outcome {
	ec := cfg.Slack.Extraction
	st, err := j.Store.state(ctx, k)
	if err != nil {
		return outcome{err: err}
	}
	conv, err := j.Facts.Conversation(ctx, p.conv)
	if err != nil {
		return outcome{err: err}
	}
	in := Input{Unit: k, Now: now, Language: ec.LanguageName(), Conversation: conv, Brief: st.Brief, WindowChars: ec.Window(), Prompts: prompts}
	if j.Extra != nil {
		x, err := j.Extra(ctx)
		if err != nil {
			return outcome{err: err}
		}
		in.Extra = &x
	}
	for _, c := range cfg.Placement {
		in.Contexts = append(in.Contexts, c.Match)
	}
	var units []unit.Unit
	if p.dm {
		units, err = j.readDM(ctx, team, ec, &in)
	} else {
		units = []unit.Unit{k}
		in.Messages, err = j.Facts.Messages(ctx, k, "", 0)
	}
	if errors.Is(err, slack.ErrUnknownUnit) {
		j.logf("slack extraction: %s left out: %v", k, err)
		j.finish(k, p)
		return outcome{}
	}
	if err != nil {
		return outcome{err: err}
	}
	for _, u := range units {
		its, err := j.Ledger.Items(ctx, u)
		if err != nil {
			return outcome{err: err}
		}
		in.Ledger = append(in.Ledger, its...)
	}
	if len(in.Messages) == 0 {
		j.finish(k, p)
		return outcome{done: true}
	}
	if in.Users, err = j.Facts.Users(ctx, userIDs(append(slices.Clone(in.Context), in.Messages...), conv, viewer)); err != nil {
		return outcome{err: err}
	}
	in.Viewer = Viewer{ID: viewer, Name: userName(in, viewer)}
	in.NewFrom = newFrom(st, p, in.Messages)

	r, o := j.call(ctx, k, p, in, ec, tier, now)
	if !o.called || o.callErr != nil || o.err != nil {
		return o
	}
	n, err := j.apply(ctx, k, p, team, in, r, st, cfg, now)
	if err != nil {
		return outcome{called: true, err: err}
	}
	return outcome{called: true, done: true, ops: n}
}

// call asks for in's result and records the call. An escalated call that runs over the per-call
// cap runs again on the base tier, within the daily caps, so the escalation never feeds the breaker
// on its own.
func (j *Job) call(ctx context.Context, k unit.Unit, p *pend, in Input, ec config.SlackExtraction, base host.Tier,
	now time.Time) (Result, outcome) {
	tier := base
	if !p.overCap {
		tier = TierFor(in, base, ec.EscalateOver())
	}
	if tier != base {
		j.logf("slack extraction %s: over %d chars, escalated to %s", k, ec.EscalateOver(), tier)
	}
	for {
		cctx, cancel := context.WithTimeout(ctx, CallTimeout)
		r, ans, callErr := Extract(cctx, j.Asker, host.Ask{Tier: tier, MaxCostUSD: ec.MaxCallUSD()}, in)
		cancel()
		run := store.ExtractionRun{Unit: k, At: now, Model: ans.Model, CostUSD: ans.CostUSD, Outcome: "ok"}
		if callErr != nil {
			run.Outcome, run.Error = "failed", callErr.Error()
		}
		if err := errors.Join(j.spend(ctx, ec, now, ans), j.Store.DB.Record(ctx, run)); err != nil {
			return Result{}, outcome{called: true, callErr: callErr, err: err}
		}
		if tier == base || !errors.Is(callErr, host.ErrOverBudget) {
			return r, outcome{called: true, callErr: callErr}
		}
		j.logf("slack extraction %s: the %s call ran over $%.2f, next on %s", k, tier, ec.MaxCallUSD(), base)
		p.overCap, tier = true, base
		if why, err := j.capped(ctx, ec, now); err != nil || why != "" {
			return Result{}, outcome{err: err}
		}
	}
}

// answerError is a failure the answer caused after a good call: ops, segments or their
// placement refused it. Only these and unfit answers count toward giving up on a target.
type answerError struct{ error }

func (e *answerError) Unwrap() error { return e.error }

// backOff delays a failed target by quiet·2^(retries-1) from now, at most maxBackoff. A failure
// the answer caused, or a timeout while other targets succeed, also counts toward GiveUpAfter,
// after which the job gives up on the target until its next change; an outage or a store
// failure never does.
func (j *Job) backOff(k unit.Unit, p *pend, ec config.SlackExtraction, now time.Time, err error, answer bool) {
	p.retries++
	p.oksAtFail = j.oks
	if answer {
		p.fails++
	}
	if answer && p.fails >= ec.GiveUpAfter() {
		why := fmt.Sprintf("%s after %d failed calls: %v", k, p.fails, err)
		j.logf("slack extraction: gave up on %s", why)
		j.finish(k, p)
		j.gaveMu.Lock()
		j.gaveUp = append(j.gaveUp, gaveUp{at: now, why: why})
		j.gaveMu.Unlock()
		return
	}
	wait := ec.QuietFor() << min(p.retries-1, 16)
	if wait <= 0 || wait > maxBackoff {
		wait = maxBackoff
	}
	p.retryAt = now.Add(wait)
}

func (j *Job) finish(k unit.Unit, p *pend) {
	delete(j.pending, k)
	j.seqs[k] = max(j.seqs[k], p.maxSeq)
}

// readDM fills in's window and spans from a DM's latest open segments, and returns their units.
func (j *Job) readDM(ctx context.Context, team string, ec config.SlackExtraction, in *Input) ([]unit.Unit, error) {
	if j.DM == nil || j.Writer == nil || j.Segments == nil {
		return nil, errors.New("no DM reader or segment map")
	}
	spans, units, err := j.Store.spans(ctx, team, in.Conversation.ID)
	if err != nil {
		return nil, err
	}
	if len(spans) > dmSpans {
		spans, units = spans[len(spans)-dmSpans:], units[len(units)-dmSpans:]
	}
	from := ""
	if len(spans) > 0 {
		from = spans[0].FromTS
	}
	in.Segments = spans
	states, err := j.Store.States(ctx, units)
	if err != nil {
		return units, err
	}
	in.SegmentBriefs = make([]string, len(units))
	for i, u := range units {
		in.SegmentBriefs[i] = states[u].Brief
	}
	if in.Messages, err = j.DM(ctx, in.Conversation.ID, from); err != nil {
		return units, err
	}
	if from != "" && j.DMBefore != nil && ec.ContextN() > 0 {
		before, err := j.DMBefore(ctx, in.Conversation.ID, from, ec.ContextN())
		if err != nil {
			return units, err
		}
		in.Context = ContextBefore(before, ec.ContextN(), ec.ContextBudget())
	}
	return units, nil
}

func (j *Job) apply(ctx context.Context, k unit.Unit, p *pend, team string, in Input, r Result, st store.ExtractionState,
	cfg config.Config, now time.Time) (int, error) {
	parse := j.Parse
	if parse == nil {
		parse = func(string) (unit.Unit, bool) { return unit.Unit{}, false }
	}
	ops, dropped, err := Ops(in, r, team, parse)
	if err != nil {
		return 0, &answerError{err}
	}
	if dropped > 0 {
		j.logf("slack extraction %s: %d links dropped", k, dropped)
	}
	if r.Dropped > 0 {
		j.logf("slack extraction %s: %d items or links citing a message outside the window dropped", k, r.Dropped)
	}
	if r.ExtrasDropped > 0 {
		j.logf("slack extraction %s: %d briefs kept without extra fields that did not fit the brief intent's schema", k, r.ExtrasDropped)
	}
	states := map[unit.Unit]store.ExtractionState{}
	state := store.ExtractionState{LastTS: in.Messages[len(in.Messages)-1].TS, LastSeq: p.maxSeq, Brief: r.Brief, Verdict: r.Verdict,
		VerdictWhy: r.VerdictWhy, ViewerRole: r.ViewerRole, StartedByViewer: r.StartedByViewer, ClosurePosted: r.ClosurePosted, At: now}
	states[k] = state
	itemUnit := map[string]unit.Unit{}
	for _, it := range in.Ledger {
		itemUnit[it.ID] = it.Unit
	}
	units := []unit.Unit{k}
	obj := slack.ObjectRef{Conversation: p.conv}
	if p.dm {
		applied, segs, err := j.segment(ctx, team, in, r)
		if err != nil {
			return 0, &answerError{err}
		}
		for i, u := range applied {
			s := state
			s.LastTS, s.LastSeq, s.Topic, s.Brief = "", 0, segs[i].Topic, segs[i].Brief
			states[u] = s
		}
		if ops, err = j.place(ctx, in, ops, applied, itemUnit); err != nil {
			return 0, &answerError{err}
		}
		units = unitsOf(applied, itemUnit)
	} else {
		key, _ := slack.ParseUnit(k)
		obj.TS = key.TS
		for _, op := range ops {
			itemUnit[op.Item] = k
		}
	}
	if err := j.applyOps(ctx, k, ops); err != nil {
		return 0, err
	}
	hints := hintsOf(r, ops, itemUnit, k)
	// A DM conversation is no unit of the host, which knows its segments: its brief stays in the plugin's store.
	hosted := func(u unit.Unit) bool { return !(p.dm && u == k) }
	if j.PutBrief != nil {
		for _, u := range slices.SortedFunc(maps.Keys(states), unit.Compare) {
			if b := states[u].Brief; b != "" && hosted(u) {
				if err := j.PutBrief(ctx, u, b, now); err != nil {
					return 0, err
				}
			}
		}
	}
	if err := j.Store.DB.Save(ctx, store.ExtractionSave{States: states, Hints: hints}); err != nil {
		return 0, err
	}
	j.finish(k, p)
	if j.Briefed != nil {
		for u, s := range states {
			if s.Brief != "" && hosted(u) {
				j.Briefed(u)
			}
		}
	}
	state.Watched = st.Watched
	if _, err := j.watch(ctx, cfg, k, obj, units, state, now, true); err != nil {
		j.logf("slack extraction %s: watch: %v", k, err)
	}
	return len(ops), nil
}

// repair rewrites, once per process, the people of the ledger's items whose keys an answer
// echoed as the ledger rendered them ("group:group:ana (U0001)"), so each names its id again.
func (j *Job) repair(ctx context.Context) error {
	if j.repaired {
		return nil
	}
	states, err := j.Store.DB.All(ctx, slack.Provider)
	if err != nil {
		return err
	}
	var ops []items.Op
	for _, u := range slices.SortedFunc(maps.Keys(states), unit.Compare) {
		its, err := j.Ledger.Items(ctx, u)
		if err != nil {
			return err
		}
		for _, it := range its {
			ops = append(ops, repairOps(u, it)...)
		}
	}
	j.repaired = true
	if len(ops) == 0 {
		return nil
	}
	res, err := j.Ledger.Apply(ctx, items.Extracted, ops)
	if err != nil {
		return fmt.Errorf("slack extraction: repair people: %w", err)
	}
	j.logf("slack extraction: %d items' people repaired, %d refused", len(ops)-len(res.Refused), len(res.Refused))
	return nil
}

// repairOps are the ops that give it's people their ids back; none when every key is sound.
func repairOps(u unit.Unit, it items.Item) []items.Op {
	b := opBuilder{in: Input{Unit: u}}
	fix := func(ps []items.Person) ([]items.Person, bool) {
		out, changed := make([]items.Person, len(ps)), false
		for i, p := range ps {
			out[i] = p
			if id := personID(p.Key); id != p.Key && (slackUserID.MatchString(id) || slackGroupID.MatchString(id) || strings.HasPrefix(p.Key, "group:group:")) {
				out[i], changed = b.person(p.Key), true
				if out[i].Name == id && p.Name != "" {
					out[i].Name = personID(p.Name)
				}
			}
		}
		return out, changed
	}
	anchor := it.Origin
	anchor.Unit = u
	var ops []items.Op
	var f items.Fields
	changed := false
	if it.Requester.Key != "" {
		if ps, ok := fix([]items.Person{it.Requester}); ok {
			f.Requester, changed = &ps[0], true
		}
	}
	if ps, ok := fix(it.WaitingOn); ok {
		f.WaitingOn, changed = &ps, true
	}
	if changed {
		ops = append(ops, items.Op{Op: items.OpUpdate, Unit: u, Item: it.ID, Anchor: anchor, Fields: &f})
	}
	if ps, ok := fix(it.Assignees); ok {
		ops = append(ops, items.Op{Op: items.OpAssign, Unit: u, Item: it.ID, Anchor: anchor, Assignees: ps, Basis: it.Basis})
	}
	return ops
}

// applyOps applies ops; a failed Apply is the host's, which the target retries.
func (j *Job) applyOps(ctx context.Context, k unit.Unit, ops []items.Op) error {
	if len(ops) == 0 {
		return nil
	}
	res, err := j.Ledger.Apply(ctx, items.Extracted, ops)
	if err != nil {
		return err
	}
	if len(res.Refused) > 0 {
		j.logf("slack extraction %s: %d ops refused, first: %s", k, len(res.Refused), res.Refused[0].Reason)
	}
	return nil
}

// segment writes the DM's segments the answer names before any op lands on them. Without
// segments, a DM never segmented gets one open segment from its first message.
// segment applies the answer's segments and returns each applied unit with
// the answer's segment it came from; a DM answered without segments is one
// segment, briefed as the conversation.
func (j *Job) segment(ctx context.Context, team string, in Input, r Result) ([]unit.Unit, []SegmentOut, error) {
	var spans []slack.Span
	segs := r.Segments
	for _, s := range segs {
		sp := slack.Span{FromTS: s.FromTS, ToTS: s.ToTS}
		if s.Ref != "new" {
			n, _ := strconv.Atoi(strings.TrimPrefix(s.Ref, "s"))
			sp.ID = in.Segments[n-1].ID
		}
		spans = append(spans, sp)
	}
	if len(spans) == 0 {
		if len(in.Segments) > 0 {
			return nil, nil, nil
		}
		spans, segs = []slack.Span{{FromTS: in.Messages[0].TS}}, []SegmentOut{{Brief: r.Brief}}
	}
	spans, segs = consecutive(spans, segs, in.Messages)
	applied, err := j.Writer.Apply(ctx, team, in.Conversation.ID, spans)
	if err != nil {
		return nil, nil, fmt.Errorf("segments: %w", err)
	}
	return applied, segs, nil
}

// consecutive orders spans by their start and ends every span but the last at the window's
// last message before the next one starts: what an answer that leaves a continued topic open
// beside a new one means. segs follows the spans.
func consecutive(spans []slack.Span, segs []SegmentOut, msgs []slack.Message) ([]slack.Span, []SegmentOut) {
	order := make([]int, len(spans))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return strings.Compare(spans[a].FromTS, spans[b].FromTS) })
	outSpans, outSegs := make([]slack.Span, len(spans)), make([]SegmentOut, len(segs))
	for i, at := range order {
		outSpans[i], outSegs[i] = spans[at], segs[at]
	}
	for i := range outSpans[:max(len(outSpans)-1, 0)] {
		sp, next := &outSpans[i], outSpans[i+1].FromTS
		if sp.ToTS != "" && sp.ToTS < next {
			continue
		}
		sp.ToTS = sp.FromTS
		for _, m := range msgs {
			if m.TS > sp.ToTS && m.TS < next {
				sp.ToTS = m.TS
			}
		}
	}
	return outSpans, outSegs
}

// place moves every op onto the segment unit of its item: a new item's segment covers its
// origin (a reply's parent), an existing item stays on its unit, and a unit-level link
// lands on the segment of its evidence.
func (j *Job) place(ctx context.Context, in Input, ops []items.Op, applied []unit.Unit, itemUnit map[string]unit.Unit) ([]items.Op, error) {
	parent := map[string]string{}
	for _, m := range in.Messages {
		parent[m.TS] = m.Anchor()
	}
	segOf := func(ref string) (unit.Unit, error) {
		_, ts, _ := strings.Cut(ref, "/")
		us, err := j.Segments.UnitsFor(ctx, in.Conversation.ID, cmp.Or(parent[ts], ts))
		switch {
		case err != nil:
			return unit.Unit{}, err
		case len(us) > 0:
			return us[0], nil
		case len(applied) > 0:
			return applied[len(applied)-1], nil
		}
		return unit.Unit{}, fmt.Errorf("no segment covers %s", ref)
	}
	out := slices.Clone(ops)
	for i, op := range out {
		u, ok := itemUnit[op.Item]
		if !ok || op.Item == "" {
			var err error
			if u, err = segOf(op.Anchor.Ref); err != nil {
				return nil, err
			}
			if op.Item != "" {
				itemUnit[op.Item] = u
			}
		}
		out[i].Unit, out[i].Anchor.Unit = u, u
		if op.Evidence != nil {
			e := *op.Evidence
			e.Anchor.Unit = u
			out[i].Evidence = &e
		}
	}
	return out, nil
}

func unitsOf(applied []unit.Unit, itemUnit map[string]unit.Unit) []unit.Unit {
	out := slices.Clone(applied)
	for _, u := range itemUnit {
		out = append(out, u)
	}
	slices.SortFunc(out, func(a, b unit.Unit) int { return strings.Compare(a.Key, b.Key) })
	return slices.Compact(out)
}

// hintsOf are the mode and place of every item of r, by unit and item id. A new item's id is
// the one its create op carries; Ops creates new items in the order of r.Items.
func hintsOf(r Result, ops []items.Op, itemUnit map[string]unit.Unit, fallback unit.Unit) map[unit.Unit]map[string]store.ExtractionHint {
	var created []string
	for _, op := range ops {
		if op.Op == items.OpCreate {
			created = append(created, op.Item)
		}
	}
	out := map[unit.Unit]map[string]store.ExtractionHint{}
	for _, it := range r.Items {
		id := it.Ref
		if isNewRef(it.Ref) {
			if len(created) == 0 {
				continue
			}
			id, created = created[0], created[1:]
		}
		u := cmp.Or(itemUnit[id], fallback)
		if out[u] == nil {
			out[u] = map[string]store.ExtractionHint{}
		}
		out[u][id] = store.ExtractionHint{Mode: it.Mode, Context: it.Place.Context, Why: it.Place.Why}
	}
	return out
}

// newFrom is the first ts the run shows as new: the oldest changed message or the first
// message after the cursor; "" before the first run.
func newFrom(st store.ExtractionState, p *pend, msgs []slack.Message) string {
	if st.LastTS == "" {
		return ""
	}
	nf := p.minTS
	for _, m := range msgs {
		if slack.CompareTS(m.TS, st.LastTS) > 0 {
			if nf == "" || slack.CompareTS(m.TS, nf) < 0 {
				nf = m.TS
			}
			break
		}
	}
	return nf
}

func userIDs(msgs []slack.Message, conv slack.Conversation, viewer string) []string {
	ids := []string{viewer}
	if conv.DMUser != "" {
		ids = append(ids, conv.DMUser)
	}
	for _, m := range msgs {
		ids = append(ids, m.User, m.ParentUser)
		ids = append(ids, m.Mentions...)
		for _, r := range m.Reactions {
			ids = append(ids, r.Users...)
		}
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if len(ids) > 0 && ids[0] == "" {
		ids = ids[1:]
	}
	return ids
}

// watch watches the target's object while one of its items is open (on a run), and
// unwatches it once all resolved for warm_for and the viewer did not start it or a closure
// was posted.
func (j *Job) watch(ctx context.Context, cfg config.Config, k unit.Unit, obj slack.ObjectRef, units []unit.Unit,
	st store.ExtractionState, now time.Time, ran bool) (unwatched bool, err error) {
	var its []items.Item
	for _, u := range units {
		got, err := j.Ledger.Items(ctx, u)
		if err != nil {
			return false, err
		}
		its = append(its, got...)
	}
	open := slices.ContainsFunc(its, func(it items.Item) bool { return it.State == items.Open })
	switch {
	case open && ran:
		if err := j.Watcher.Watch(ctx, obj, slack.ReasonExtraction); err != nil {
			return false, err
		}
		if !st.Watched {
			return false, j.Store.DB.SetWatched(ctx, k, true, now)
		}
	case !open && st.Watched && len(its) > 0 && settled(its, now, warmFor(cfg)) && (!st.StartedByViewer || st.ClosurePosted || st.ClosureMarked):
		if err := j.Watcher.Unwatch(ctx, obj); err != nil {
			return false, err
		}
		return true, j.Store.DB.SetWatched(ctx, k, false, now)
	}
	return false, nil
}

func settled(its []items.Item, now time.Time, warm time.Duration) bool {
	var last time.Time
	for _, it := range its {
		if it.UpdatedAt.After(last) {
			last = it.UpdatedAt
		}
	}
	return now.Sub(last) >= warm
}

func warmFor(cfg config.Config) time.Duration {
	return cfg.Items.Heat.WarmForOr()
}

// sweep unwatches, every sweepEvery, the watched targets nothing changed in.
func (j *Job) sweep(ctx context.Context, cfg config.Config, now time.Time) (int, error) {
	if !j.swept.IsZero() && now.Sub(j.swept) < sweepEvery {
		return 0, nil
	}
	j.swept = now
	rows, err := j.Store.DB.Watched(ctx, slack.Provider)
	if err != nil {
		return 0, err
	}
	n := 0
	var errs []error
	for k, st := range rows {
		if j.pending[k] != nil {
			continue
		}
		obj, units, err := j.objectOf(ctx, k)
		var off bool
		if err == nil {
			off, err = j.watch(ctx, cfg, k, obj, units, st, now, false)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("unwatch %s: %w", k, err))
		}
		if off {
			n++
		}
	}
	return n, errors.Join(errs...)
}

// objectOf is a target's object and units: a thread, or a DM with its open segments.
func (j *Job) objectOf(ctx context.Context, k unit.Unit) (slack.ObjectRef, []unit.Unit, error) {
	if key, err := slack.ParseUnit(k); err == nil && key.TS != "" {
		return slack.ObjectRef{Conversation: key.Conversation, TS: key.TS}, []unit.Unit{k}, nil
	}
	team, conv, ok := strings.Cut(k.Key, "/")
	if !ok {
		return slack.ObjectRef{}, nil, fmt.Errorf("not a target: %s", k)
	}
	_, units, err := j.Store.spans(ctx, team, conv)
	return slack.ObjectRef{Conversation: conv}, units, err
}

// Want asks the next run to read u's conversation, past quiet_for: true
// when it has changes left to read, so a brief of u is coming.
func (j *Job) Want(u unit.Unit) bool {
	k, ok := targetFor(u)
	if !ok {
		return false
	}
	j.wantMu.Lock()
	defer j.wantMu.Unlock()
	if !j.waiting[k] {
		return false
	}
	if j.wants == nil {
		j.wants = map[unit.Unit]bool{}
	}
	j.wants[k] = true
	return true
}

func (j *Job) wanted(k unit.Unit) bool {
	j.wantMu.Lock()
	defer j.wantMu.Unlock()
	return j.wants[k]
}

// noteWaiting records the targets left to read; a want of a target read
// is done.
func (j *Job) noteWaiting() {
	j.wantMu.Lock()
	defer j.wantMu.Unlock()
	j.waiting = map[unit.Unit]bool{}
	for k := range j.pending {
		j.waiting[k] = true
	}
	for k := range j.wants {
		if !j.waiting[k] {
			delete(j.wants, k)
		}
	}
}

// targetFor is the target a unit's brief is written by: a thread is its
// own, a DM segment its conversation's.
func targetFor(u unit.Unit) (unit.Unit, bool) {
	key, err := slack.ParseUnit(u)
	if err != nil {
		return unit.Unit{}, false
	}
	if key.Segment != "" {
		return dmKey(key.Team, key.Conversation), true
	}
	return u, true
}
