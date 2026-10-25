package api

import (
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// PlanItem is one item of the workflows' plan.
type PlanItem struct {
	ID       string        `json:"id"`
	Workflow string        `json:"workflow"`
	Decision string        `json:"decision"`
	Reason   string        `json:"reason,omitempty"`
	Default  string        `json:"default,omitempty"`
	Mode     string        `json:"mode"`
	Error    string        `json:"error,omitempty"`
	Unit     unit.Unit     `json:"unit"`
	Label    string        `json:"label"` // Unit as a person reads it
	Steps    []string      `json:"steps"`
	Deadline time.Time     `json:"deadline,omitzero"`
	Gain     Gain          `json:"gain"`
	Why      Why           `json:"why"`
	Attempts int           `json:"attempts,omitempty"`
	Actions  []ActionState `json:"actions"` // built by internal/catalog only
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
	Workflows []UnitWorkflow `json:"workflows,omitempty"`
}

// UnitWorkflow is a workflow of the explained unit's provider that gives
// the unit no item: Outcome is "not matched", "matched", "waits" or
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
