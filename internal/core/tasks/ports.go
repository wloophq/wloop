// Package tasks is the tasks service: tasks derived from items, captured by
// the user or opened by other services, and the verbs that move them.
package tasks

import (
	"context"
	"errors"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/wake"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// Reader is the read half of Store. Its lists read q.All only: every task, or the open ones; the views over
// them are the service's.
type Reader interface {
	Tasks(ctx context.Context, q task.Query) ([]task.Task, error)
	// TasksSince is the page after position since: Full, with q's tasks, when since is from another epoch or
	// ahead of the store; else the tasks written after since's Rev, whatever their category. Removed is empty
	// and Counters zero: the service fills them.
	TasksSince(ctx context.Context, q task.Query, since Since) (Page, error)
	Task(ctx context.Context, id task.ID) (task.Task, error) // ErrNoTask; ErrVerb is a verb the task's state refuses, naming both
	TasksOf(ctx context.Context, u unit.Unit) ([]task.Task, error)
}

type Store interface {
	Reader
	// Apply writes cs and evs in one transaction, bumps Rev and notes port.TopicTasks; when cs change
	// nothing it writes and notes nothing, and with no cs it writes evs alone.
	Apply(ctx context.Context, cs []task.Change, evs ...port.Event) error
}

// PropStore keeps the tasks' properties. SetProp replaces the property's
// value and records its writer; a zero Value deletes the property, which
// PropsOf then leaves out. ErrNoTask when the task does not exist. It checks
// no type and no writer: the caller holds the profile that declares them.
// PropsOf are a task's properties, empty for one with none.
type PropStore interface {
	SetProp(ctx context.Context, id task.ID, name string, v task.Value, w task.Writer) error
	PropsOf(ctx context.Context, id task.ID) (task.Props, error)
}

// The profile's evaluators the core calls, each injected, so the core runs
// the profile's predicates and expressions without importing the package
// that evaluates them.
type (
	// ViewEval says whether task t is in the profile's view; the service calls it inside a Pass, which ctx
	// carries.
	ViewEval func(ctx context.Context, view string, t task.Task) (bool, error)
	// Authority says whether actor a may run verb on task t (allow), and
	// whether running it opens the verb's gate instead (gate).
	Authority func(ctx context.Context, verb string, a task.Actor, t task.Task) (allow, gate bool, err error)
	// OwedBy is who owes task t's next step, as tasks.toml's owed_by says: "me",
	// a person's key, or "" when nobody does.
	OwedBy func(ctx context.Context, t task.Task) (string, error)
)

type Since struct {
	Epoch string
	Rev   uint64
}

type Page struct {
	Epoch    string
	Rev      uint64
	Full     bool
	Tasks    []task.Task
	Removed  []task.ID
	Counters task.Counters
}

// Wakes keeps the tasks' wakes, one per task: Put replaces a task's, Drop removes it, dropping none is no
// error, and Of is false for a task with none.
type Wakes interface {
	Put(ctx context.Context, w wake.Wake) error
	Drop(ctx context.Context, id task.ID) error
	Of(ctx context.Context, id task.ID) (wake.Wake, bool, error)
}

// Items is the items ledger's reader.
type Items interface {
	Items(ctx context.Context, u unit.Unit) ([]items.Item, error)
}

// Facts reads a unit's facts; ok false: plugin disabled.
type Facts interface {
	UnitFacts(ctx context.Context, u unit.Unit) (task.UnitFacts, bool, error)
}

// ViewSource serves the views over another thing than the stored tasks, such as the gates: Tasks are view
// q.View's, each what the view lists stands for, and Task reads a task of its own, whose id carries the prefix it
// is registered with; false for none.
type ViewSource interface {
	Tasks(ctx context.Context, q task.Query) ([]task.Task, error)
	Task(ctx context.Context, id task.ID) (task.Task, bool, error)
}

// VerbHandler runs a verb another service owns (answer/approve/dismiss on messages, approve/dismiss on drafts);
// one on a stored task records the verb with Service.RecordVerb once it succeeded.
type VerbHandler func(ctx context.Context, e task.Task, req Request) (Result, error)

// Request is a verb to run. Actor is who runs it, required; Source where it
// came from (the cockpit, a workflow's name). Chain is the chain of
// workflows that led to it, which its event carries on. Approved is the
// approved gate a gated run of the verb opened, which ProposedVerb sets and
// no route copies: the person's approval stands for the verb's by.
type Request struct {
	Verb     task.Verb
	Args     map[string]string
	Source   string
	Actor    task.Actor
	Chain    workflow.Chain
	Approved int64
}

// Drafts settles the drafts of a task a verb ended: e as the verb left it, done or dropped. The verb stands
// whatever happens to them, so Ended reports its own failures.
type Drafts interface {
	Ended(ctx context.Context, e task.Task, v task.Verb)
}

// Gates are the gates a gated verb opens and reads back: one open gate per proposal.
type Gates interface {
	Open(ctx context.Context, g gate.Gate) (gate.Gate, error)
	Get(ctx context.Context, id int64) (gate.Gate, error)
	OfTask(ctx context.Context, id task.ID) ([]gate.Gate, error)
}

type Result struct {
	Task  task.Task
	Lines []string
}

type OpenRequest struct {
	Title    string
	Unit     *unit.Unit
	Origin   string
	Contexts []string
}

var (
	ErrNoTask = errors.New("no such task")
	ErrVerb   = errors.New("the task's state refuses the verb")
	// ErrArgs is a request the service cannot read: an unknown verb or view, or a missing or bad argument.
	ErrArgs = errors.New("bad task request")
	// ErrPluginDisabled refuses a change to a task whose unit's plugin is disabled.
	ErrPluginDisabled = errors.New("the unit's plugin is disabled")
	// ErrForbidden refuses a verb its actor may not run on the task, as the profile's by says.
	ErrForbidden = errors.New("the actor may not run the verb")
)

// States are the profile's states (tasks.toml), the properties its roles act on and the transitions of its verbs.
type States interface {
	task.Profile
	// Move is the states verb moves a task from (every state when empty) and the state it moves it to ("" for
	// none); false when the profile declares no such verb.
	Move(verb string) (from []string, to string, ok bool)
	// Handler is the handler verb runs besides its move, "" for none.
	Handler(verb string) string
	// Gate is the gate a by.<actor> of "gate" on verb opens, as the profile declares it: its Name, Kind and
	// Intents; false when the verb names none.
	Gate(verb string) (gate.Gate, bool)
}

// Recategorizer rewrites the stored tasks' categories from their states, as of reads them, noting the tasks
// it moved. Unknown are the states tasks hold that of does not know; their tasks keep their category.
type Recategorizer interface {
	Recategorize(ctx context.Context, of func(state string) (task.Category, bool)) (unknown []string, err error)
}
