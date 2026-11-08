package api

import (
	"cmp"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// PlanReasons are the plan item reasons strings.toml words, by their English
// text, which a client reads through its catalog; any other reason reads as
// the daemon gives it.
var PlanReasons = []string{
	"its terminal is closed; it ends on its own",
	"its terminal is open with no agent in it: close that terminal, or dismiss this",
	"it has no workspace to find its terminal in: dismiss this",
}

// PlanItem is one item of the workflows' plan.
type PlanItem struct {
	ID       string `json:"id"`
	Workflow string `json:"workflow"`
	Summary  string `json:"summary,omitempty"` // Workflow's summary, in the profile's locale
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`
	// ReasonKey is the engine's template Reason renders, the catalog's key for it, with the values it carries and,
	// for a wait on facts, those facts; "" for a reason no template renders, which reads as Reason.
	ReasonKey    string            `json:"reason_key,omitempty"`
	ReasonParams map[string]string `json:"reason_params,omitempty"`
	ReasonFacts  []Fact            `json:"reason_facts,omitempty"`
	Default      string            `json:"default,omitempty"`
	Observe      ObserveMode       `json:"observe"`
	Error        string            `json:"error,omitempty"`
	Unit         unit.Unit         `json:"unit"`
	Label        string            `json:"label"`           // Unit as a person reads it
	Title        string            `json:"title,omitempty"` // Unit's row title
	Link         string            `json:"link,omitempty"`  // Unit's row link
	Steps        []string          `json:"steps"`
	// Target is the task a task's item acts on, as a person finds it; nil for another unit.
	Target *PlanTarget `json:"target,omitempty"`
	// Does are its steps with what each needs said of it.
	Does     []PlanStep    `json:"does,omitempty"`
	Deadline time.Time     `json:"deadline,omitzero"`
	Gain     Gain          `json:"gain"`
	Why      Why           `json:"why"`
	Attempts int           `json:"attempts,omitempty"`
	Actions  []ActionState `json:"actions"` // built by internal/catalog only
}

// UnitLabel is the item's unit as a person reads it.
func (it PlanItem) UnitLabel() string { return cmp.Or(it.Label, it.Unit.Label()) }

// PlanTarget is a task a plan item acts on: its id, its title and the units it came from.
type PlanTarget struct {
	Task  string     `json:"task"`
	Title string     `json:"title"`
	Units []PlanUnit `json:"units,omitempty"`
}

// PlanUnit is a unit as a person reads it, with its link when it has one.
type PlanUnit struct {
	Label string `json:"label"`
	Link  string `json:"link,omitempty"`
}

// PlanStep is a step of a plan item: what it uses, its reach (local, private, visible; "" for the intent's), its
// text arguments, and for items.resolve the summaries of the open items it resolves.
type PlanStep struct {
	Use   string `json:"use"`
	Reach string `json:"reach,omitempty"`
	// Says is the catalog key of what the step does, its {name}s its Args; "" for a step the catalog does not word.
	Says string `json:"says,omitempty"`
	// Posts says the step may post where others see it: its reach is visible or its intent's, or it drafts.
	Posts bool              `json:"posts,omitempty"`
	Args  map[string]string `json:"args,omitempty"`
	Items []string          `json:"items,omitempty"`
}

type Gain struct {
	MemoryBytes int64 `json:"memory_bytes"`
	DiskBytes   int64 `json:"disk_bytes"`
}

type Why struct {
	Trigger string `json:"trigger"`
	Event   string `json:"event,omitempty"`
	Matched []Fact `json:"matched"`
}

type Fact struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type PlanQuery struct {
	Workflow string `json:"workflow,omitempty"`
	Provider string `json:"provider,omitempty"`
	Trigger  string `json:"trigger,omitempty"`
	// Closed asks for the sessions closed in the last day with the stored
	// plan.
	Closed bool `json:"closed,omitempty"`
	// Unit, provider:key, asks what every workflow decides for that unit
	// now; the other fields are then ignored.
	Unit string `json:"unit,omitempty"`
}

type PlanResponse struct {
	Items  []PlanItem      `json:"items"`
	Errors []string        `json:"errors"`
	Closed []ClosedSession `json:"closed,omitempty"`
	// Unit is the unit a Unit query explained, as its provider lists it.
	Unit      string         `json:"unit,omitempty"`
	Label     string         `json:"label,omitempty"` // Unit as a person reads it
	Title     string         `json:"title,omitempty"` // Unit's row title
	Workflows []UnitWorkflow `json:"workflows,omitempty"`
}

// UnitWorkflow is a workflow of the explained unit's provider that gives
// the unit no item: Outcome is "not_matched", "matched", "waits" or
// "error", Facts the facts its `when` read.
type UnitWorkflow struct {
	Workflow string `json:"workflow"`
	Trigger  string `json:"trigger"`
	Outcome  string `json:"outcome"`
	Reason   string `json:"reason,omitempty"`
	Facts    []Fact `json:"facts,omitempty"`
}

// ClosedSession is a session closed with the line that resumes it.
type ClosedSession struct {
	Session string    `json:"session"`
	At      time.Time `json:"at"`
	Reason  string    `json:"reason"`
	Resume  string    `json:"resume,omitempty"`
}

type PlanActRequest struct {
	IDs      []string `json:"ids"`
	Action   string   `json:"action"`
	Guidance string   `json:"guidance,omitempty"`
}

// ObserveMode is a plan item's observe flag: its workflow only plans, and
// a person applies the item by hand.
type ObserveMode bool
