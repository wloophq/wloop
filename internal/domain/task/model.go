// Package task holds the tasks and their pure rules.
package task

import (
	"time"

	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// State is a state the profile declares (tasks.toml); Go reads its
// Category, never its name.
type State string

// KindProject is the value of the kind property the project verb gives the
// parent it creates, and the one it attaches tasks to.
const KindProject = "project"

// ID is "t-" + 16 hex. A task an item calls for: DerivedID, so a re-derivation is idempotent; captures and
// projects: random. Draft tasks: "draft:<draft id>".
type ID string

// ItemRef names an items.Item.ID of a unit.
type ItemRef struct {
	Unit unit.Unit
	Item string
}

type Task struct {
	ID ID
	// State is the task's state; Category is its category, which every rule reads.
	State    State
	Category Category
	Title    string
	Notes    string
	Items    []ItemRef
	// Resolved are the items the task held that no longer call for it, such as one resolved: they keep the
	// task as their holder, so an item that opens again comes back to it.
	Resolved []ItemRef
	Units    []unit.Unit // attached units; their cards render in the detail
	// Props are the task's properties; Written says who wrote each, and when.
	Props      Props
	Written    map[string]Written
	Parent     ID  // "" none
	Position   int // order among its parent's children
	Sequential bool
	// Asleep says the task holds a wake (wake.Wake), as the store reads it; writing a task leaves its wake as
	// it is.
	Asleep  bool
	Changed bool // "changed since you saw it"; cleared by the seen verb
	SeenAt  time.Time
	Origin  string // "derived", "user", "message", "draft"

	Created, Updated time.Time

	// A parent's review interval (zero: DefaultReviewEvery) and last review.
	ReviewEvery time.Duration
	ReviewedAt  time.Time
}

// Written is who last wrote a property, and when.
type Written struct {
	By Writer
	At time.Time
}

// Me is the user's identity over the enabled plugins (items.Identity; Person.Is(Me)).
type Me = items.Identity

// UnitFacts are what the rules need beyond the items, from the plugin's card.
type UnitFacts struct {
	Unit          unit.Unit
	StartedByMe   bool
	ClosurePosted bool
	Cares         bool      // followed/watched by the user, or the user's own unit
	Carded        bool      // the unit's card is stored, so Cares says what its plugin reported
	LastHuman     time.Time // newest human event not by the user (card.LastHuman when !Who.Is(Me))
	// Kind is the value of the kind property the unit's plugin gives the tasks of its units, such as thread;
	// "" gives none.
	Kind string
}

type ChangeOp string

const (
	OpCreate  ChangeOp = "create"
	OpUpdate  ChangeOp = "update"
	OpResolve ChangeOp = "resolve"
)

type Change struct {
	Op   ChangeOp
	Task Task
	Why  string
}

type Verb string

const (
	VerbNext      Verb = "next"
	VerbWaiting   Verb = "waiting"
	VerbSchedule  Verb = "schedule"
	VerbWake      Verb = "wake"
	VerbSomeday   Verb = "someday"
	VerbReference Verb = "reference"
	VerbFocus     Verb = "focus"
	VerbProject   Verb = "project"
	VerbApprove   Verb = "approve"
	VerbDelegate  Verb = "delegate"
	VerbDone      Verb = "done"
	VerbDismiss   Verb = "dismiss"
	VerbSeen      Verb = "seen"
	VerbContext   Verb = "context"
	VerbArea      Verb = "area"
	VerbAnswer    Verb = "answer"
	VerbEdit      Verb = "edit" // drafts only, registered by the outbox
	VerbRetry     Verb = "retry"
	VerbReply     Verb = "reply" // a draft the user writes on the task's unit, served by the daemon
)

// View is a view's id, as the profile's views.toml names it.
type View string

// What a view lists: the stored tasks, or the gates.
const (
	OverTasks = "tasks"
	OverGates = "gates"
)

// Query selects a view's tasks, narrowed to the tasks whose area is Area and that hold every one of Contexts. A
// store reads All only: every task when true, else the open ones (neither done nor dropped).
type Query struct {
	View     View
	Area     string
	Contexts []string
	All      bool
}

// Counters are the counts of the profile's views over Area ("" every area).
type Counters struct {
	Area     string
	Views    map[View]int   // every view that lists something, narrowed to Area
	Areas    map[string]int // the open tasks per area
	Contexts map[string]int // the open tasks per context, within Area
	Badge    int            // the sum of the views the profile's badge names
}
