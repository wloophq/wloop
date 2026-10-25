// Package workflow holds a parsed workflow and what the engine plans from it:
// triggers, reach, plan items, runs and their idempotency keys.
package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/mark"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type Mode string

const (
	ModeObserve Mode = "observe"
	ModeApply   Mode = "apply"
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

// ReadsMarks says whether p reads the unit's marks, so a caller loads them
// only then.
func ReadsMarks(p Predicate) bool { return len(MarkNames(p)) > 0 }

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
// []string, time.Duration or time.Time. "<slot>.repo" is "owner/repo" or ""
// and is what the pause's Repos reads.
type Facts map[string]any

type Step struct {
	Use   string         // a name of builtin.Steps
	Reach Reach          // from the vocabulary, never from the manifest
	Args  map[string]any // e.g. {"intent": "review", "round": "follow-up"}
}

type Limits struct {
	PerRun, PerDay, PerUnitPerDay int
	Cooldown                      time.Duration
}

type Approval struct {
	Default  Outcome
	Window   time.Duration
	WaitWhen Predicate // a match makes the item pending with default wait, even when a guard refuses
}

type Workflow struct {
	Name, Description string
	Provider          unit.ProviderID // the manifest's `subject`
	Slot              string          // the provider's fact slot: "pr" for github, else the provider id
	Trigger           Trigger
	When              Predicate
	Steps             []Step
	Limits            Limits
	Mode              Mode
	AllowVisible      bool
	Order             int
	Key               []string // fact names; empty means the slot's fingerprint fact
	// Marks are the facts whose values a done or dismissed item records as
	// the unit's marks, by the mark's name under the workflow (mark.Of).
	Marks    map[string]string
	Approval Approval
	Source   string // manifest path
	Bundled  bool
	// Overrides are the files of a bundled workflow a user directory replaces.
	Overrides []string
	// DraftApproval is the manifest's [draft_approval] block; nil when absent.
	DraftApproval *draft.Approval
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

func NewKey(w Workflow, u unit.Unit, f Facts, fired time.Time) Key {
	names := slices.Clone(w.Key)
	if len(names) == 0 {
		names = []string{w.Slot + ".fingerprint"}
	}
	slices.Sort(names)
	h := sha256.New()
	for _, n := range names {
		fmt.Fprintf(h, "%s=%s\n", n, formatValue(f[n]))
	}
	if w.Trigger.Kind == TriggerSchedule {
		fmt.Fprintf(h, "trigger.fired_at=%s\n", formatValue(fired))
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
	At              time.Time
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
	Mode     Mode
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
