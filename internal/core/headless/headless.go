// Package headless runs a turn as one restricted agent call, with no session.
package headless

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type Request struct {
	Unit     unit.Unit
	Items    []string // item ids acted on
	Task     string
	Intent   turn.Intent // "" is the Service's Default
	Guidance string
	Inputs   map[string]any
	Trigger  turn.Trigger
	Workflow string // "" by hand
	// ResumeOf is the headless turn whose question this run answers, 0 none.
	ResumeOf int64
	// ByPerson is a turn a person asked for, whatever its Trigger.
	ByPerson bool
}

type Result struct {
	Seq     int64         // the turn
	Record  record.Record // as stored
	Drafts  []draft.Draft // proposed, origin turn Seq
	Ops     int           // ledger ops applied
	CostUSD *float64
}

// Builder renders a unit's instructions for an intent; implemented in internal/cli over internal/turn.
type Builder interface {
	Instructions(ctx context.Context, u unit.Unit, intent turn.Intent, items []string, inputs map[string]any) (string, error)
}

// Placer resolves a placement to an existing directory, implemented in internal/cli:
// an absolute Checkout as is, "main" the repo's main checkout path; "" (a new worktree) and
// ErrUnplaced give ok false: a headless turn never creates a worktree.
type Placer interface {
	Checkout(ctx context.Context, u unit.Unit, item string) (dir string, ok bool, err error)
}

type Caps struct {
	PerCallUSD, PerDayUSD float64
	// Tier is the model tier of the calls; "" is balanced.
	Tier turns.Tier
}

type Service struct {
	Turns *turns.Service // records the turn; its Items ledger applies the record's ops with provenance turn
	// Starter admits each new turn; a run again resumes through it.
	Starter *turns.Starter
	Build   Builder
	Place   Placer
	Asker   turns.Asker
	// Ledger reads the unit's items, which a draft's items must name; nil checks none.
	Ledger  items.Applier
	Outbox  *outbox.Service
	Intents func(name turn.Intent) (intent.Def, bool)
	// Default is the intent a request that names none runs on units of p.
	Default func(p unit.ProviderID) (intent.Def, bool)
	Caps    Caps // from config "headless"
	// Context is the daemon's lifetime: a stop cancels the calls Start runs; nil is context.Background.
	Context context.Context
	Timeout time.Duration                                             // a call's limit; 0 is DefaultTimeout
	Spent   func(ctx context.Context, day time.Time) (float64, error) // turn_usage of headless turns that day
	Dossier func(ctx context.Context, u unit.Unit) (path string, ok bool)
	// Logged writes the answer's log to the unit's dossier once the turn's record is stored; nil writes none.
	Logged func(ctx context.Context, u unit.Unit, seq int64, log string)
	Clock  port.Clock
	// Go runs a started call in the background, for Start and Again; nil runs it on a new goroutine.
	Go  func(run func())
	Log func(format string, args ...any)
	// Wrapper is the profile's prompt around the unit's instructions, read at each call.
	Wrapper func() (Wrapper, error)

	// mu makes the cap check and the turn's start one step.
	mu sync.Mutex
}

// Readier is an Asker that tells, spawning nothing, whether a call can run.
type Readier interface {
	Ready(ctx context.Context, a turns.Ask) error
}

var (
	ErrBusy        = turns.ErrHeadlessBusy
	ErrOverCap     = turns.ErrOverCap
	ErrNotHeadless = errors.New("the turn is not headless")
)

// DefaultTimeout bounds a headless call when Service.Timeout is zero.
const DefaultTimeout = 10 * time.Minute

// MaxDrafts is how many drafts one headless answer may propose.
const MaxDrafts = 3

//go:embed schema.json
var schemaJSON []byte

// Schema is the answer schema of a headless call.
func Schema() string {
	var b bytes.Buffer
	if err := json.Compact(&b, schemaJSON); err != nil {
		panic(err)
	}
	return b.String()
}

// started is a headless turn stored and ready to call.
type started struct {
	seq  int64
	turn string
	req  Request
	def  intent.Def
	ask  turns.Ask
}

// Run admits and stores the turn, calls the agent and records its answer; it passes turns.ErrNoRestricted through.
func (s *Service) Run(ctx context.Context, r Request) (Result, error) {
	st, err := s.admit(ctx, r)
	if err != nil {
		return Result{}, err
	}
	return s.finish(ctx, st)
}

// Start admits and stores the turn and runs its call in the background; the turn's seq is known once it returns.
func (s *Service) Start(ctx context.Context, r Request) (int64, error) {
	st, err := s.admit(ctx, r)
	if err != nil {
		return 0, err
	}
	s.background(st)
	return st.seq, nil
}

// admit starts r's turn through the Starter, which runs the intent's role
// and effects around the turn's record.
func (s *Service) admit(ctx context.Context, r Request) (started, error) {
	if s.Starter == nil {
		return started{}, errors.New("no turn starter is wired")
	}
	if r.Intent == "" && s.Default != nil {
		if d, ok := s.Default(r.Unit.Provider); ok {
			r.Intent = turn.Intent(d.Name)
		}
	}
	if r.Trigger == "" {
		r.Trigger = turn.ByHand
	}
	x := &runner{s: s, r: r}
	if _, err := s.Starter.Start(ctx, turns.Request{Unit: r.Unit, Intent: r.Intent, Guidance: r.Guidance, Inputs: r.Inputs, Trigger: r.Trigger,
		Route: turns.RouteLaunch, ByPerson: r.ByPerson}, x); err != nil {
		return started{}, err
	}
	return x.st, nil
}

// runner is a headless turn as Start runs it: checked before any effect,
// then rendered and stored with the guidance and inputs Start settled.
type runner struct {
	s  *Service
	r  Request
	st started
}

func (x *runner) Sessionless() bool { return true }

func (x *runner) Guard(_ context.Context, t turns.Turn) error {
	_, err := x.s.def(t.Unit(), t.Intent())
	return err
}

func (x *runner) Run(ctx context.Context, t turns.Turn) (turns.Outcome, error) {
	st, err := x.s.begin(ctx, t, x.r)
	if err != nil {
		return turns.Outcome{}, err
	}
	x.st = st
	return turns.Outcome{ID: st.turn, Submitted: true}, nil
}

// background runs st's call on its own.
func (s *Service) background(st started) {
	bg := s.Context
	if bg == nil {
		bg = context.Background()
	}
	run := func() {
		if _, err := s.finish(bg, st); err != nil {
			s.logf("headless turn %d on %s: %v", st.seq, st.req.Unit, err)
		}
	}
	if s.Go != nil {
		s.Go(run)
	} else {
		go run()
	}
}

// Again resumes q through the Starter and runs its intent on q's unit, items, task and inputs with said as
// Guidance, q.Trigger and q.Workflow; ErrNotHeadless when q.Mode is not headless. The route is Resumed.
func (s *Service) Again(ctx context.Context, q turn.Turn, said string) (turns.AnswerResult, error) {
	if q.Runner != turn.RunnerHeadless {
		return turns.AnswerResult{}, ErrNotHeadless
	}
	if s.Starter == nil {
		return turns.AnswerResult{}, errors.New("no turn starter is wired")
	}
	u, err := s.unitOf(ctx, q)
	if err != nil {
		return turns.AnswerResult{}, err
	}
	t, err := s.Starter.ResumeAnswered(ctx, u, q.Seq, said)
	if err != nil {
		return turns.AnswerResult{}, err
	}
	st, err := s.begin(ctx, t, Request{Items: turns.ItemsOf(q), Task: turns.TaskOf(q), Workflow: q.Workflow})
	if err != nil {
		return turns.AnswerResult{}, err
	}
	s.background(st)
	return turns.AnswerResult{Route: turn.Resumed}, nil
}

func (s *Service) unitOf(ctx context.Context, q turn.Turn) (unit.Unit, error) {
	b, ok, err := s.Turns.Store.BookOf(ctx, turns.Key{Turn: q.ID})
	if err != nil {
		return unit.Unit{}, err
	}
	if !ok {
		return unit.Unit{}, fmt.Errorf("%w: no turn %s", turn.ErrNoTurn, q.ID)
	}
	return b.Unit, nil
}

// def is intent's definition when it runs headless on u.
func (s *Service) def(u unit.Unit, name turn.Intent) (intent.Def, error) {
	def, ok := s.Intents(name)
	switch {
	case !ok:
		return def, fmt.Errorf("no intent %s", name)
	case !def.Runs(string(turn.RunnerHeadless)):
		return def, fmt.Errorf("%w: %s does not run headless (its manifest's modes)", ErrNotHeadless, name)
	case !def.Serves(u.Provider):
		return def, fmt.Errorf("%s does not act on %s units", name, u.Provider)
	}
	return def, nil
}

// begin renders and stores t, a new turn Start admitted or a resume, with
// r's items, task and workflow.
func (s *Service) begin(ctx context.Context, t turns.Turn, r Request) (started, error) {
	r.Unit, r.Intent, r.Trigger, r.Guidance, r.Inputs, r.ResumeOf = t.Unit(), t.Intent(), t.Trigger(), t.Guidance(), t.Inputs(), t.ResumeOf()
	def, err := s.def(r.Unit, r.Intent)
	if err != nil {
		return started{}, err
	}
	var first string
	if len(r.Items) > 0 {
		first = r.Items[0]
	}
	dir, placed, err := s.Place.Checkout(ctx, r.Unit, first)
	if err != nil {
		return started{}, fmt.Errorf("placing %s: %w", r.Unit.Label(), err)
	}
	if !placed {
		dir = ""
	}
	text, err := s.Build.Instructions(ctx, r.Unit, r.Intent, r.Items, r.Inputs)
	if err != nil {
		return started{}, err
	}
	ask := turns.Ask{Tier: s.tier(), Schema: Schema(), MaxCostUSD: s.Caps.PerCallUSD}
	var dossier string
	if dir != "" {
		ask.Dirs = append(ask.Dirs, dir)
	}
	if s.Dossier != nil {
		if p, ok := s.Dossier(ctx, r.Unit); ok {
			dossier = p
			ask.Dirs = append(ask.Dirs, p)
		}
	}
	if len(ask.Dirs) > 0 {
		ask.Tools = slices.Clone(turns.ReadOnlyTools)
	}
	if ask.Prompt, err = s.prompt(r, dir, dossier, text); err != nil {
		return started{}, err
	}
	if rd, ok := s.Asker.(Readier); ok {
		if err := rd.Ready(ctx, ask); err != nil {
			return started{}, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.underCap(ctx); err != nil {
		return started{}, err
	}
	id := randomHex()
	seq, err := s.Turns.BeginHeadless(ctx, t, turns.HeadlessStart{Turn: id, Workflow: r.Workflow, Role: def.Role, Items: r.Items, Task: r.Task})
	if err != nil {
		return started{}, err
	}
	return started{seq: seq, turn: id, req: r, def: def, ask: ask}, nil
}

func (s *Service) prompt(r Request, checkout, dossier, instructions string) (string, error) {
	if s.Wrapper == nil {
		return "", errors.New("no headless wrapper is wired")
	}
	w, err := s.Wrapper()
	if err != nil {
		return "", fmt.Errorf("reading the headless wrapper: %w", err)
	}
	return w.Prompt(r, checkout, dossier, instructions)
}

func (s *Service) tier() turns.Tier {
	if s.Caps.Tier == "" {
		return turns.TierBalanced
	}
	return s.Caps.Tier
}

func (s *Service) underCap(ctx context.Context) error {
	if s.Caps.PerDayUSD <= 0 || s.Spent == nil {
		return nil
	}
	spent, err := s.Spent(ctx, s.Clock.Now())
	if err != nil {
		return fmt.Errorf("reading the day's headless spend: %w", err)
	}
	running, err := s.Turns.RunningHeadless(ctx)
	if err != nil {
		return fmt.Errorf("counting the headless calls running: %w", err)
	}
	if reserved := float64(running) * s.Caps.PerCallUSD; spent+reserved >= s.Caps.PerDayUSD {
		return fmt.Errorf("%w: $%.2f spent and $%.2f reserved by %d calls running, of $%.2f today", ErrOverCap, spent, reserved, running, s.Caps.PerDayUSD)
	}
	return nil
}

func randomHex() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// answer is the call's JSON as the schema shapes it.
type answer struct {
	Record json.RawMessage `json:"record"`
	Drafts []proposed      `json:"drafts"`
	Log    string          `json:"log"`
}

type proposed struct {
	Kind     draft.Kind `json:"kind"`
	Target   string     `json:"target"`
	Text     string     `json:"text"`
	Items    []string   `json:"items"`
	Reaction string     `json:"reaction"`
	Unit     string     `json:"unit"`
	Effects  []string   `json:"effects"`
}

func (s *Service) finish(ctx context.Context, st started) (Result, error) {
	out := Result{Seq: st.seq}
	got, callErr := s.ask(ctx, st.ask)
	ctx = context.WithoutCancel(ctx)
	out.CostUSD = got.CostUSD
	if callErr != nil {
		r, err := s.end(ctx, st.seq, turns.HeadlessEnd{Answer: got, Err: fmt.Errorf("the headless call failed: %w", callErr)})
		out.Record = r.rec
		return out, errors.Join(callErr, err)
	}
	a, rec, unfit := parse(got.JSON)
	if unfit == nil {
		unfit = s.fits(rec, st)
	}
	if unfit != nil {
		r, err := s.end(ctx, st.seq, turns.HeadlessEnd{Answer: got, Err: unfit})
		out.Record = r.rec
		return out, err
	}
	ok, refused := s.screen(ctx, st, a.Drafts)
	if len(refused) > 0 {
		rec.Next = cut(strings.TrimSpace(rec.Next+"\n"+"Drafts refused: "+strings.Join(refused, "; ")), record.NextMax)
	}
	r, err := s.end(ctx, st.seq, turns.HeadlessEnd{Rec: rec, Answer: got})
	out.Record = r.rec
	if err != nil {
		return out, err
	}
	if s.Logged != nil && strings.TrimSpace(a.Log) != "" {
		s.Logged(ctx, st.req.Unit, st.seq, a.Log)
	}
	if r.got.Ledger != nil {
		out.Ops = len(r.got.Ledger.Applied)
	}
	if r.rec.State != rec.State {
		return out, nil
	}
	out.Drafts = s.propose(ctx, st, ok)
	return out, nil
}

// ask runs the call within the timeout; a call that reports no cost is charged the per-call cap.
func (s *Service) ask(ctx context.Context, q turns.Ask) (turns.Answer, error) {
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	got, err := s.Asker.Ask(ctx, q)
	if got.CostUSD == nil && s.Caps.PerCallUSD > 0 {
		capped := s.Caps.PerCallUSD
		got.CostUSD = &capped
	}
	return got, err
}

type ended struct {
	got turns.Recorded
	rec record.Record
}

// end records the turn and reads back the record as stored.
func (s *Service) end(ctx context.Context, seq int64, e turns.HeadlessEnd) (ended, error) {
	got, err := s.Turns.EndHeadless(ctx, seq, e)
	out := ended{got: got}
	if err != nil {
		return out, err
	}
	if books, err := s.Turns.Store.Books(ctx, got.Unit); err == nil {
		b := books[got.Unit]
		if r, ok := b.LastRecord(seq); ok {
			out.rec = r.Record
		}
	}
	return out, nil
}

func parse(raw []byte) (answer, record.Record, error) {
	var a answer
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return answer{}, record.Record{}, fmt.Errorf("%w: %v", turns.ErrUnfit, err)
	}
	if len(a.Record) == 0 {
		return answer{}, record.Record{}, fmt.Errorf("%w: no record", turns.ErrUnfit)
	}
	rec, err := record.Parse(a.Record)
	if err != nil {
		return answer{}, record.Record{}, fmt.Errorf("%w: %v", turns.ErrUnfit, err)
	}
	return a, rec, nil
}

// fits checks the record against the intent's rules before anything of the answer applies.
func (s *Service) fits(rec record.Record, st started) error {
	if err := rec.Validate(st.def.Record); err != nil {
		return fmt.Errorf("%w: %v", turns.ErrUnfit, err)
	}
	for i, o := range rec.Ledger {
		if !o.Unit.IsZero() && o.Unit != st.req.Unit {
			return fmt.Errorf("%w: ledger[%d] acts on %s, not this turn's unit", turns.ErrUnfit, i, o.Unit.Label())
		}
	}
	return nil
}

// screen keeps the drafts pinned to the turn's unit and its items, at most MaxDrafts; it returns what it
// refused, by index and why. When the unit's items cannot be read, every draft is refused.
func (s *Service) screen(ctx context.Context, st started, ps []proposed) ([]proposed, []string) {
	var ok []proposed
	var refused []string
	known, err := s.unitItems(ctx, st.req.Unit)
	if err != nil {
		s.logf("headless turn %d: reading the items of %s: %v", st.seq, st.req.Unit, err)
	}
	for i, p := range ps {
		why := ""
		switch {
		case i >= MaxDrafts:
			why = fmt.Sprintf("over the limit of %d", MaxDrafts)
		case p.Unit != "" && p.Unit != st.req.Unit.String():
			why = "on another unit; drafts go on this turn's unit only"
		case s.Outbox == nil:
			why = "no outbox is wired"
		case err != nil:
			why = "the unit's items cannot be read, so the draft's items cannot be checked"
		}
		if why == "" {
			why = undeclared(st.def, p.Effects)
		}
		if why == "" {
			for _, id := range p.Items {
				if !known[id] {
					why = fmt.Sprintf("item %s is not an item of this unit", id)
					break
				}
			}
		}
		if why != "" {
			refused = append(refused, fmt.Sprintf("drafts[%d] (%s): %s", i, p.Kind, why))
			continue
		}
		ok = append(ok, p)
	}
	return ok, refused
}

// propose proposes the screened drafts of a turn whose record stored, with the origin the daemon knows.
func (s *Service) propose(ctx context.Context, st started, ps []proposed) []draft.Draft {
	origin := draft.Origin{Kind: draft.OriginTurn, TurnSeq: st.seq, Intent: string(st.req.Intent), Workflow: st.req.Workflow}
	var out []draft.Draft
	for _, p := range ps {
		d, err := s.Outbox.Propose(ctx, outbox.Proposal{Unit: st.req.Unit, Items: p.Items, Task: st.req.Task, Kind: p.Kind,
			Target: p.Target, Text: p.Text, Reaction: p.Reaction, Effects: p.Effects, Origin: origin})
		if err != nil {
			s.logf("headless turn %d: a %s draft on %s was refused: %v", st.seq, p.Kind, st.req.Unit, err)
			continue
		}
		out = append(out, d)
	}
	return out
}

// undeclared says why effects are refused: one the intent's drafts.allowed_effects
// does not list; "" when it lists them all.
func undeclared(def intent.Def, effects []string) string {
	for _, e := range effects {
		if !slices.Contains(def.AllowedEffects, e) {
			return fmt.Sprintf("it declares the effect %s, which the %s intent does not list in drafts.allowed_effects", e, def.Name)
		}
	}
	return ""
}

func (s *Service) unitItems(ctx context.Context, u unit.Unit) (map[string]bool, error) {
	if s.Ledger == nil {
		return nil, errors.New("no items ledger is wired")
	}
	its, err := s.Ledger.Items(ctx, u)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(its))
	for _, it := range its {
		out[it.ID] = true
	}
	return out, nil
}

func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func (s *Service) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log(format, args...)
	}
}
