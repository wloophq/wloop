// Package workflow holds a parsed workflow and what the engine plans from it:
// triggers, reach, plan items, runs and their idempotency keys.
package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/mark"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type Reach string

const (
	ReachLocal   Reach = "local"
	ReachPrivate Reach = "private"
	ReachVisible Reach = "visible"
)

// Outcome is what a pending item does when its window ends unanswered.
type Outcome string

const (
	OutcomeRun  Outcome = "run"
	OutcomeWait Outcome = "wait"
)

type Decision string

const (
	DecisionRun     Decision = "run"
	DecisionPending Decision = "pending"
	DecisionBlocked Decision = "blocked"
	DecisionSkipped Decision = "skipped"
	// DecisionDeferred is an item whose step started asynchronously: it runs
	// again at its Deadline until a run is done. No person decides it.
	DecisionDeferred Decision = "deferred"
)

type TriggerKind string

const (
	TriggerSweep    TriggerKind = "sweep"
	TriggerEvent    TriggerKind = "event"
	TriggerSchedule TriggerKind = "schedule"
)

type Trigger struct {
	Kind     TriggerKind
	On       []string // event kinds, Kind == TriggerEvent
	Schedule *Cron    // Kind == TriggerSchedule
	// Actors are the kinds of actor whose events plan the workflow, every
	// one when empty; Depth the longest chain an event it reacts to may
	// close, DefaultChainDepth when 0. Both read with Kind == TriggerEvent.
	Actors []string
	Depth  int
}

// Fires says whether an event of the trigger's kinds plans workflow w: its
// actor is one the trigger listens to, its chain is shorter than the
// trigger's depth, and w is not in that chain already.
func (t Trigger) Fires(w string, ev Event) bool {
	if len(t.Actors) > 0 && !slices.Contains(t.Actors, ActorKind(ev.Actor)) {
		return false
	}
	depth := t.Depth
	if depth == 0 {
		depth = DefaultChainDepth
	}
	return ev.Chain.Depth < depth && !ev.Chain.Has(w)
}

// Predicate is a compiled `when`; Reads are the facts it names, for the why.
type Predicate interface {
	Match(v Vars) (bool, error)
	Reads() []string
}

// MarkNames are the marks p reads with handled(); a predicate lists them
// through a MarkNames() []string method.
func MarkNames(p Predicate) []string {
	if r, ok := p.(interface{ MarkNames() []string }); ok {
		return r.MarkNames()
	}
	return nil
}

// ReadsMarks says whether one of ps reads the unit's marks, so a caller
// loads them only then.
func ReadsMarks(ps ...Predicate) bool {
	for _, p := range ps {
		if len(MarkNames(p)) > 0 {
			return true
		}
	}
	return false
}

// All is a predicate that matches when every one of ps does; a nil one
// matches.
func All(ps ...Predicate) Predicate { return all(ps) }

type all []Predicate

func (a all) Match(v Vars) (bool, error) {
	for _, p := range a {
		if p == nil {
			continue
		}
		if ok, err := p.Match(v); err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

func (a all) MarkNames() []string {
	var out []string
	for _, p := range a {
		out = append(out, MarkNames(p)...)
	}
	return out
}

func (a all) Reads() []string {
	var out []string
	for _, p := range a {
		if p != nil {
			out = append(out, p.Reads()...)
		}
	}
	return out
}

// Refused is a predicate that never matches and says why on every match.
type Refused struct{ Err error }

func (r Refused) Match(Vars) (bool, error) { return false, r.Err }
func (Refused) Reads() []string            { return nil }

type Vars struct {
	Facts   Facts             // the unit's slot ("pr.state", "session.idle_for")
	Trigger Facts             // "trigger.kind", "trigger.event.kind", "trigger.event.key", "trigger.event.data", "trigger.fired_at"
	Config  Facts             // "config.cleanup.idle", ...
	Marks   map[string]string // the unit's handled marks by name, which handled() reads
	Now     time.Time
}

// Facts are a unit's facts by dotted name; values are bool, int, string,
// []string, time.Duration or time.Time, or a Temporal a provider reports,
// which the engine resolves to one of those before a predicate reads it. "<slot>.repo" is "owner/repo" or ""
// and is what the pause's Repos reads.
type Facts map[string]any

type Step struct {
	Use   string         // a name of builtin.Steps
	Reach Reach          // from the vocabulary, never from the manifest
	Args  map[string]any // e.g. {"intent": "review", "stage": "follow-up"}
}

type Limits struct {
	PerRun, PerDay, PerUnitPerDay int
	Cooldown                      time.Duration
}

// ApprovalMode is how a workflow's matched item runs: at once, when its
// window ends unless a person stops it, or when a person applies it.
type ApprovalMode string

const (
	ApprovalRun   ApprovalMode = "run"
	ApprovalDelay ApprovalMode = "delay"
	ApprovalWait  ApprovalMode = "wait"
)

// ApprovalModes are the run approval's modes.
var ApprovalModes = []ApprovalMode{ApprovalRun, ApprovalDelay, ApprovalWait}

// Releases says whether m runs an item with no person applying it.
func (m ApprovalMode) Releases() bool { return m == ApprovalRun || m == ApprovalDelay }

// ApprovalRule is one [[approval.rule]]: its mode and window when its When
// holds. A matched wait rule also makes pending an item a guard refuses.
type ApprovalRule struct {
	When   Predicate
	Mode   ApprovalMode
	Window time.Duration
}

// Approval is a workflow's run approval: the first rule whose When holds,
// else Mode and Window.
type Approval struct {
	Mode   ApprovalMode
	Window time.Duration
	Rules  []ApprovalRule
}

// ErrWindow refuses a window off the delay mode, or a delay with none.
var ErrWindow = errors.New("window is read only with mode delay, which needs one longer than zero")

// ValidApproval refuses a mode outside ApprovalModes and a window off delay.
func ValidApproval(m ApprovalMode, window time.Duration) error {
	switch {
	case !slices.Contains(ApprovalModes, m):
		return fmt.Errorf("mode %q is not one of run, delay or wait", m)
	case (m == ApprovalDelay) != (window > 0):
		return ErrWindow
	}
	return nil
}

// Validate refuses a's mode or a rule's when ValidApproval does, or a rule
// with no When.
func (a Approval) Validate() error {
	if err := ValidApproval(a.Mode, a.Window); err != nil {
		return err
	}
	for i, r := range a.Rules {
		if r.When == nil {
			return fmt.Errorf("rule[%d] has no when", i)
		}
		if err := ValidApproval(r.Mode, r.Window); err != nil {
			return fmt.Errorf("rule[%d]: %w", i, err)
		}
	}
	return nil
}

// Predicates are a's rules' whens.
func (a Approval) Predicates() []Predicate {
	out := make([]Predicate, len(a.Rules))
	for i, r := range a.Rules {
		out[i] = r.When
	}
	return out
}

type Workflow struct {
	Name, Summary string
	Provider      unit.ProviderID
	Slot          string // the provider's fact slot: "pr" for github, else the provider id
	Trigger       Trigger
	When          Predicate
	Steps         []Step
	Limits        Limits
	// Observe plans items pending and never runs them by itself.
	Observe bool
	Order   int
	Key     []string // fact names; empty means the slot's fingerprint fact
	// Marks are the facts whose values a done or dismissed item records as
	// the unit's marks, by the mark's name under the workflow (mark.Of).
	Marks map[string]string
	// Approval is the approval of its runs' plan gates.
	Approval Approval
	// Gates are the approval of the gates its gate.open steps open, by
	// kind, every kind but draft and plan.
	Gates   map[gate.Kind]Approval
	Source  string // manifest path
	Bundled bool
	// Overrides are the files of a bundled workflow a user directory replaces.
	Overrides []string
	// DraftApproval is the approval of the drafts its steps and turns
	// propose, from its [gates.approval]; nil when that names none for a
	// draft gate.
	DraftApproval *draft.Policy
	// Withdraw is the manifest's [drafts.withdraw] when, over the subject's facts
	// and each draft's (the draft slot): an open draft the workflow proposed
	// is withdrawn when it matches. Nil withdraws none.
	Withdraw Predicate
	// WithdrawReason is the Reason of a draft Withdraw withdraws: assigned
	// or moot.
	WithdrawReason string
}

// Visible says whether a step of the workflow acts in the user's name where others see it.
func (w Workflow) Visible() bool {
	return slices.ContainsFunc(w.Steps, func(s Step) bool { return s.Reach == ReachVisible })
}

// Key is the idempotency key: workflow, unit and the key facts' values
// (for a schedule trigger, plus the firing time).
type Key struct {
	Workflow string
	Unit     unit.Unit
	Value    string // sha256 hex of the sorted key values, first 16 chars
}

// EventIDFact is the trigger fact an event workflow's key may name, so
// each event plans its own run.
const EventIDFact = "trigger.event.id"

func NewKey(w Workflow, u unit.Unit, f Facts, why Why) Key {
	names := slices.Clone(w.Key)
	if len(names) == 0 {
		names = []string{w.Slot + ".fingerprint"}
	}
	slices.Sort(names)
	h := sha256.New()
	for _, n := range names {
		v := f[n]
		if n == EventIDFact && why.Event != nil {
			v = why.Event.ID
		}
		fmt.Fprintf(h, "%s=%s\n", n, formatValue(v))
	}
	if w.Trigger.Kind == TriggerSchedule {
		fmt.Fprintf(h, "trigger.fired_at=%s\n", formatValue(why.FiredAt))
	}
	return Key{Workflow: w.Name, Unit: u, Value: hex.EncodeToString(h.Sum(nil))[:16]}
}

func formatValue(v any) string {
	switch x := v.(type) {
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano)
	case []string:
		return strings.Join(x, "\x1f")
	case nil:
		return ""
	}
	return fmt.Sprint(v)
}

// ID is "<workflow>/<provider>/<path-escaped-key>/<value>".
func (k Key) ID() string {
	return k.Workflow + "/" + string(k.Unit.Provider) + "/" + url.PathEscape(k.Unit.Key) + "/" + k.Value
}

type Fact struct {
	Name  string
	Value any
}

type Event struct {
	ID              int64 // events.id
	Unit            unit.Unit
	Kind, Key, Data string
	// Actor is who recorded it, "<actor>:<source>" for a verb; Chain the
	// chain of workflows it closes.
	Actor string
	Chain Chain
	At    time.Time
}

type Why struct {
	Workflow string
	Trigger  TriggerKind
	Event    *Event
	FiredAt  time.Time
	Matched  []Fact // the predicate's Reads with their values, sorted by name
}

type Gain struct{ MemoryBytes, DiskBytes int64 }

type Item struct {
	ID       string
	Key      Key
	Workflow string
	Unit     unit.Unit
	Decision Decision
	Reason   string
	Steps    []Step
	Gain     Gain
	Why      Why
	Default  Outcome
	Deadline time.Time
	Guidance string // set by Act(guide) only
	// Marks are the marks a done or dismissed run records, by full name, from
	// the facts the item was planned with.
	Marks    map[string]mark.Value
	Attempts int
	Error    string
	Observe  bool
	At       time.Time
}

type RunStatus string

const (
	RunDone      RunStatus = "done"
	RunDismissed RunStatus = "dismissed"
	RunFailed    RunStatus = "failed"
	RunRefused   RunStatus = "refused"
)

type Run struct {
	Key      Key
	Status   RunStatus
	Attempts int
	Error    string
	At       time.Time
}

// MaxAttempts is how many failed runs make an item's key terminal.
const MaxAttempts = 3

type Action string

const (
	ActionApply   Action = "apply"
	ActionDismiss Action = "dismiss"
	ActionSkip    Action = "skip"
	ActionGuide   Action = "guide"
)

const (
	KindPlanned       = "workflow_planned"   // Key item id:decision, Data reason
	KindStepRan       = "workflow_step_ran"  // Key item id:step index
	KindStepRefused   = "workflow_refused"   // Key item id:attempt
	KindStepFailed    = "workflow_failed"    // Key item id:attempt
	KindItemDismissed = "workflow_dismissed" // Key item id
	KindItemDropped   = "workflow_dropped"   // Key item id:at, Data why
)

// EngineKinds are the engine's own event kinds; a workflow's `on` may not name them.
var EngineKinds = []string{KindPlanned, KindStepRan, KindStepRefused, KindStepFailed, KindItemDismissed, KindItemDropped}

// ModeOf is a gate's approval mode as a run's: send and run run it; dry_run,
// a draft gate's only, waits.
func ModeOf(m gate.Mode) ApprovalMode {
	switch m {
	case gate.ModeSend, gate.ModeRun:
		return ApprovalRun
	case gate.ModeDelay:
		return ApprovalDelay
	}
	return ApprovalWait
}
