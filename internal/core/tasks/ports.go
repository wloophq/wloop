// Package tasks is the tasks service: tasks derived from items, captured by
// the user or opened by other services, and the verbs that move them.
package tasks

import (
	"context"
	"errors"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/merge"
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
	// Task reads task id, else follows the aliases of id, hop by hop, to the stored task they lead to: at each
	// hop, the alias to the task the hop's latest merge record (merge.RecordKind) names when it has one, else a
	// stored task first, an open one before a closed one, then the oldest. A task merged into one merged in
	// turn reads as the last. ErrNoTask when none leads to a stored task, a cycle included; a merge record it
	// cannot read fails the read.
	Task(ctx context.Context, id task.ID) (task.Task, error)
	TasksOf(ctx context.Context, u unit.Unit) ([]task.Task, error)
}

type Store interface {
	Reader
	// Apply writes cs and evs in one transaction, bumps Rev and notes port.TopicTasks; when cs change
	// nothing it writes and notes nothing, and with no cs it writes evs alone. It refuses, with ErrNoTask, an
	// update of an id an alias names and no row holds, as a sync that read a task before its merge writes.
	Apply(ctx context.Context, cs []task.Change, evs ...port.Event) error
}

// Merger is a Store that folds one task into another and gives it back, each in one transaction; a Store
// without it serves no merge and no split. Both start a new epoch of the tasks' position, so a page since
// an older one is Full and no client keeps the task that went.
type Merger interface {
	// Merge writes into as r's fold left it, drops task r.From and aliases it to r.Into, points the gates,
	// drafts and messages that name r.From at r.Into, and keeps r, completed with their ids, r.From's wake and
	// its unit's marks, as r.From's merge record; evs are written with it, and port.TopicTasks notes both
	// tasks' units. r.Superseded are gates the caller superseded already. ErrNoTask when either is not stored.
	Merge(ctx context.Context, r merge.Record, into task.Task, evs ...port.Event) error
	// Split undoes the merge r records as sp, merge.Unfold's, says: writes sp.From back with r's wake and marks,
	// points at r.From the gates, drafts and messages of sp.Units and of r's ids but those another record of
	// sp.Others lists, keeps sp.Heirs as their tasks' merge records, drops the alias, and writes sp.Rest, with
	// evs. ErrVerb when sp names another pair than r, r.From is stored, r is not its latest record, or r.Into
	// is not stored.
	Split(ctx context.Context, r merge.Record, sp merge.Split, evs ...port.Event) error
	// MergeOf is the record of the merge that took task id; false when id is stored or was never merged.
	MergeOf(ctx context.Context, id task.ID) (merge.Record, bool, error)
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
	// Survives says whether the predicate verb's survivor names (tasks.toml) holds on task t, over its facts:
	// the task it holds on alone is the one a merge keeps.
	Survives func(ctx context.Context, verb string, t task.Task) (bool, error)
	// GateText is the words of gate g a gated verb opens, the gate's text of gates.toml over the proposal facts
	// of w; "" for a gate with none.
	GateText func(ctx context.Context, g gate.Gate, w GateWords) (string, error)
	// TakenApart says whether the user took task t and other apart (task.undone), so a pair a workflow asked for
	// runs only once they decide.
	TakenApart func(ctx context.Context, t task.Task, other task.ID) (bool, error)
)

// GateWords are what the words of a gated verb's gate read: the verb, the task it runs on, the other task
// task.ArgTask names, zero when none, and the task a merge keeps and the one it folds, zero but for a merge.
type GateWords struct {
	Verb                    string
	Task, Other, Keep, Fold task.Task
}

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
	// Pass is the pass the page was listed in, which a reader of its tasks' facts reuses; nil from a store.
	Pass *Pass
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
	Verb   task.Verb
	Args   map[string]string
	Source string
	// Actor is who runs the verb: an agent only through a session the user drives, which the daemon checks
	// before it asks.
	Actor task.Actor
	// Session is the session an agent's request came through, "" for any other actor.
	Session  string
	Chain    workflow.Chain
	Approved int64
}

// Ending settles what a task that ended still holds, its drafts and gates: e as it was left, done or dropped, by
// verb v of by, through session when by is an agent, or by no verb when the source ended it. The end stands
// whatever happens to them, so Ended reports its own failures.
type Ending interface {
	Ended(ctx context.Context, e task.Task, v task.Verb, by task.Actor, session string)
}

// Gates are the gates a gated verb opens and reads back: one open gate per proposal.
type Gates interface {
	// Open opens g under the approval that decides it, or waiting for a person whatever that decides when wait.
	Open(ctx context.Context, g gate.Gate, wait bool) (gate.Gate, error)
	Get(ctx context.Context, id int64) (gate.Gate, error)
	OfTask(ctx context.Context, id task.ID) ([]gate.Gate, error)
	// HoldsTask says whether an open gate of task id holds it open until a person settles it or its unit ends.
	HoldsTask(ctx context.Context, id task.ID) (bool, error)
}

// GateSweeper is the Gates' capability a merge and a split need, served by the gates service, the only code
// that resolves a gate; Gates without it refuse both.
type GateSweeper interface {
	// ActingFor are the open gates that would act in task id's name: those of kind proposal, plan or step on
	// its own unit, and the proposals on any task whose proposed verb names id in task.ArgTask.
	ActingFor(ctx context.Context, id task.ID) ([]gate.Gate, error)
	// Supersede resolves each open gate of ids as superseded; a gate no longer open is left as it is.
	Supersede(ctx context.Context, ids ...int64) error
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
	// ErrMerged refuses a verb sent to the id of a task merged into another, which reads as that one.
	ErrMerged = errors.New("the task was merged into another")
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

// Recategorizer rewrites the stored tasks' categories from their states, as of reads them, at at, noting the
// tasks it moved. Unknown are the states tasks hold that of does not know; their tasks keep their category.
type Recategorizer interface {
	Recategorize(ctx context.Context, of func(state string) (task.Category, bool), at time.Time) (unknown []string, err error)
}
