// Package task holds the GTD entries and their pure rules.
package task

import (
	"time"

	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// State is one per entry; lists are views of it.
type State string

const (
	Inbox     State = "inbox"
	Next      State = "next"
	Waiting   State = "waiting"
	Scheduled State = "scheduled"
	Someday   State = "someday"
	Reference State = "reference"
	Done      State = "done"
	Trash     State = "trash"
)

type Kind string

const (
	KindAction    Kind = "action" // derived from items
	KindWaiting   Kind = "waiting"
	KindClose     Kind = "close"
	KindMessage   Kind = "message" // one per message
	KindDraft     Kind = "draft"   // virtual entry "draft:<id>", Origin "draft", served by the outbox ViewSource, never stored
	KindCapture   Kind = "capture"
	KindProject   Kind = "project"
	KindReference Kind = "reference"
)

// ID is "t-" + 16 hex. Derived tasks: sha256(kind|provider|unit_key|group)[:16], so a re-derivation is idempotent;
// captures, projects, messages: random. Draft tasks: "draft:<draft id>".
type ID string

// ItemRef names an items.Item.ID of a unit.
type ItemRef struct {
	Unit unit.Unit
	Item string
}

type Focus struct {
	On   bool
	Auto string // "" set by the user, "deadline", "stale"
	At   time.Time
}

type Snooze struct {
	Until      time.Time // zero: no date
	OnActivity bool
	Return     State // state the entry gets back at Until; activity always returns to Inbox
	Mute       bool  // set from UnitFacts.Mute; cleared when the mute ends
}

type WaitingOn struct {
	On        []items.Person
	Since     time.Time
	LastNudge time.Time
	Resurface time.Time // "if no reply by D, propose a nudge"
}

type Task struct {
	ID         ID
	Kind       Kind
	State      State
	Title      string
	Notes      string
	Items      []ItemRef
	Units      []unit.Unit // attached units; their cards render in the detail
	Focus      Focus
	Contexts   []string // "@decide", "@reply", "@agent", "@repo-<name>"
	Area       string   // "" none
	Project    ID       // "" none
	Position   int      // order inside its project
	Sequential bool     // KindProject only
	Snooze     Snooze
	Waiting    *WaitingOn // KindWaiting only
	Deadline   time.Time
	Changed    bool // "changed since you saw it"; cleared by the seen verb
	SeenAt     time.Time
	Origin     string // "derived", "user", "message", "draft"

	Created, Updated time.Time

	// KindProject only: the review interval (zero: DefaultReviewEvery), the last review and the dossier's slug.
	ReviewEvery time.Duration
	ReviewedAt  time.Time
	Slug        string
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
}

type ChangeOp string

const (
	OpCreate  ChangeOp = "create"
	OpUpdate  ChangeOp = "update"
	OpResolve ChangeOp = "resolve"
	OpReinbox ChangeOp = "reinbox"
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
