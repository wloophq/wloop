package automation

import (
	"context"
	"errors"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// ErrUnitGone is what Units.Facts returns, wrapped or not, for a unit that
// no longer exists: an event of that unit is passed instead of held.
var ErrUnitGone = errors.New("unit gone")

// Units is a unit provider: the units it holds and each unit's facts.
type Units interface {
	List(ctx context.Context, p unit.ProviderID) ([]unit.Unit, error)
	Facts(ctx context.Context, u unit.Unit) (workflow.Facts, error)
}

// Refresher is an optional capability of Units: the provider's own writes
// (keeping idle periods, registering terminals, census marks), run by Sweep
// only, before Evaluate.
type Refresher interface {
	Refresh(ctx context.Context) error
}

// Settler is an optional capability of Units: planning bookkeeping, run by
// an applying Sweep only, after the steps.
type Settler interface {
	Settle(ctx context.Context, st State) error
}

// State is the sweep's round state, opaque to the engine.
type State interface {
	Save(ctx context.Context) error
}

type StateLoader interface {
	Load(ctx context.Context) (State, error)
}

// Gainer is an optional capability of Units: what acting on a unit frees,
// read for items only.
type Gainer interface {
	Gain(ctx context.Context, u unit.Unit) (workflow.Gain, error)
}

// Owner is an optional capability of Units: the units of work a subject
// belongs to, whose timelines show the steps run on it. None keeps them on
// the subject.
type Owner interface {
	Owners(ctx context.Context, u unit.Unit) ([]unit.Unit, error)
}

// Freshener is an optional capability of Units, asked before the loop lock:
// the units whose source object moved since their facts were read.
type Freshener interface {
	Moved(ctx context.Context, units []unit.Unit) ([]unit.Unit, error)
}

// Seener is an optional capability of Units: the generation of the
// workspace snapshot its facts came from.
type Seener interface {
	Seen() port.Generation
}

// Dueer is an optional capability of Units: the provider's own next deadline.
type Dueer interface {
	Due() time.Time
}

// Events are the durable events event workflows read, and each workflow's cursor.
type Events interface {
	Since(ctx context.Context, cursor int64, kinds []string) ([]workflow.Event, error)
	Head(ctx context.Context) (int64, error)
	Cursor(ctx context.Context, workflow string) (cursor int64, lastFire time.Time, ok bool, err error)
	Advance(ctx context.Context, workflow string, cursor int64, lastFire time.Time) error
}

type CheckOpts struct {
	Mode   workflow.Mode
	ByHand bool
}

type RunOpts struct {
	Guidance          string
	ByHand, Confirmed bool
}

// Steps run the builtin steps a workflow names.
type Steps interface {
	// Check is pure: no store write, no note, no brief request, no network.
	Check(ctx context.Context, st workflow.Step, u unit.Unit, why workflow.Why, o CheckOpts) (refusal string, err error)
	// Note writes what a refusal records (brief request and brief_waiting;
	// round_held and MarkUnread on a cap's first hit; the daily fetch); an
	// applying Sweep only.
	Note(ctx context.Context, st workflow.Step, u unit.Unit, why workflow.Why, refusal string) error
	// Run must be idempotent per item key: a retry runs the item's steps from the first.
	Run(ctx context.Context, st workflow.Step, u unit.Unit, why workflow.Why, o RunOpts) (detail string, err error)
	// Dismiss writes the step's "handled until something new" records and its event.
	Dismiss(ctx context.Context, st workflow.Step, u unit.Unit, why workflow.Why) error
}

// Refused is a guard refusing at run time: the item is blocked and
// re-evaluated, its attempts untouched.
type Refused struct{ Reason string }

func (r Refused) Error() string { return r.Reason }

// Transient is a failure worth retrying (manager unreachable, context ended,
// GitHub budget): attempts untouched.
type Transient struct{ Err error }

func (t Transient) Error() string {
	if t.Err == nil {
		return "transient failure"
	}
	return t.Err.Error()
}

func (t Transient) Unwrap() error { return t.Err }

// Deferred is a step started asynchronously: the item stays pending until a
// later Run returns done.
type Deferred struct{ Reason string }

func (d Deferred) Error() string { return d.Reason }

// Marks are what a unit's triggers already handled, by mark name, which a
// workflow's handled() reads.
type Marks interface {
	Marks(ctx context.Context, u unit.Unit) (map[string]string, error)
}

// Ledger stores the plan and the runs. Recording a done or dismissed run
// also sets the item's Marks on its unit, in the same write.
type Ledger interface {
	// Save replaces the items of p's workflows, leaving out, in the same
	// write, an item whose key is done or dismissed: a writer that planned
	// before a run or a dismiss never brings the item back. It notes
	// TopicGate only when the pending set changes.
	Save(ctx context.Context, p PlanWrite, evs ...port.Event) error
	Items(ctx context.Context, f Filter) ([]workflow.Item, error)
	Run(ctx context.Context, k workflow.Key) (workflow.Run, bool, error)
	Record(ctx context.Context, r workflow.Run, it workflow.Item, evs ...port.Event) error
	Count(ctx context.Context, workflow string, since time.Time, u *unit.Unit) (int, error)
}

type PlanWrite struct {
	Workflows []string
	Items     []workflow.Item
}

type Filter struct {
	Workflow  string
	Provider  unit.ProviderID
	Unit      *unit.Unit
	Decisions []workflow.Decision
	ID        string
}

// Guidance is the user's stored guidance for a unit's next turn.
type Guidance interface {
	Peek(ctx context.Context, u unit.Unit) (string, error)
	Take(ctx context.Context, u unit.Unit) (string, error)
	// Keep stores guidance a run took and did not use, unless newer guidance is stored.
	Keep(ctx context.Context, u unit.Unit, guidance string) error
}

// Pause holds every unit when On, else the units of the repositories in
// Repos.
type Pause struct {
	On    bool
	Repos []string // owner/repo
}
