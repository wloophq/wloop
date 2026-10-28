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

type Snooze struct {
	Until      time.Time // zero: no date
	OnActivity bool
	Return     State // state the entry gets back at Until; "" the first inbox state
	Mute       bool  // set from UnitFacts.Mute; cleared when the mute ends
}

type Task struct {
	ID ID
	// State is the task's state; Category is its category, which every rule reads.
	State    State
	Category Category
	Title    string
	Notes    string
	Items    []ItemRef
	Units    []unit.Unit // attached units; their cards render in the detail
	// Props are the task's properties; Written says who wrote each, and when.
	Props      Props
	Written    map[string]Written
	Parent     ID  // "" none
	Position   int // order among its parent's children
	Sequential bool
	Snooze     Snooze
	Changed    bool // "changed since you saw it"; cleared by the seen verb
	SeenAt     time.Time
	Origin     string // "derived", "user", "message", "draft"

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

type Mute struct {
	Until            time.Time // zero: no date
	UntilStateChange bool      // until the unit's state changes
}

// UnitFacts are what the rules need beyond the items, from the plugin's card.
type UnitFacts struct {
	Unit          unit.Unit
	StartedByMe   bool
	ClosurePosted bool
	Cares         bool      // followed/watched by the user, or the user's own unit
	LastHuman     time.Time // newest human event not by the user (card.LastHuman when !Who.Is(Me))
	Mute          *Mute     // the plugin's mute of the unit; nil none
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
	VerbReply     Verb = "reply" // a draft the user writes on the entry's unit, served by the daemon
)

type View string

const (
	ViewFocus    View = "focus"
	ViewInbox    View = "inbox"
	ViewOutbox   View = "outbox"
	ViewNext     View = "next"
	ViewWaiting  View = "waiting"
	ViewProjects View = "projects"
	ViewSomeday  View = "someday"
	ViewLogbook  View = "logbook"
	ViewReview   View = "review"
)

// Query selects a view's entries; contexts AND; Area ignored for Inbox.
type Query struct {
	View     View
	Area     string
	Contexts []string
}

type Counters struct {
	Area     string         // "" all areas
	Views    map[View]int   // every served View except review; Inbox ignores Area; Next counts the sequential rule
	Areas    map[string]int // Next per area
	Contexts map[string]int // Next per context, within Area
	Badge    int            // Inbox + Outbox
}
