package daemon

import (
	"context"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/ingest"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// Sweeper is the sweep job's port.
type Sweeper interface {
	// Sweep plans without loop and takes loop only to act on the plan.
	Sweep(ctx context.Context, loop sync.Locker) (summary, skipped string, err error)
	// Due is the earliest instant a decision of the last sweep changes with
	// no input changing: conflict window end, start block day, cap rollover
	// with a capped step, a review turn reaching the 30-day cutoff. Zero
	// when none.
	Due() time.Time
	// Seen is the change log generation the last sweep's workspace snapshot
	// read was current at; zero when it read none.
	Seen() port.Generation
}

// Plan is the workflows' plan and the user's actions on it.
type Plan interface {
	// Evaluate is what the workflows of trigger t would decide now; it
	// writes nothing and notes nothing.
	Evaluate(ctx context.Context, t workflow.TriggerKind) (automation.Plan, error)
	// Items are the stored plan's items f selects.
	Items(ctx context.Context, f automation.Filter) ([]workflow.Item, error)
	// Act carries the user's action on item id; the caller holds the loop
	// lock.
	Act(ctx context.Context, id string, a workflow.Action, guidance string) (workflow.Item, error)
	// Drop discards u's pending items without marking them handled.
	Drop(ctx context.Context, u unit.Unit, why string) error
	// DueRun runs the pending items whose window ended; the caller holds the
	// loop lock.
	DueRun(ctx context.Context) error
}

// SweepFunc is a Sweeper with no deadline that reads no snapshot and runs
// whole under the loop lock.
type SweepFunc func(ctx context.Context) (summary, skipped string, err error)

func (f SweepFunc) Sweep(ctx context.Context, loop sync.Locker) (string, string, error) {
	loop.Lock()
	defer loop.Unlock()
	return f(ctx)
}

func (SweepFunc) Due() time.Time        { return time.Time{} }
func (SweepFunc) Seen() port.Generation { return 0 }

// EventLog reads and records the loop's events.
type EventLog interface {
	RecentActivity(ctx context.Context, limit int) ([]port.Activity, error)
	RecordEvent(ctx context.Context, ev port.Event) error
}

// Overrides are the host's overrides of units, by name (port.Overrides).
type Overrides interface {
	Override(ctx context.Context, u unit.Unit) (map[string]string, error)
	// SetOverride writes set in one transaction only while ifMatch holds;
	// a value "" clears one.
	SetOverride(ctx context.Context, u unit.Unit, set, ifMatch map[string]string) (written bool, err error)
	// Held are the units of provider p the user holds.
	Held(ctx context.Context, p unit.ProviderID) ([]unit.Unit, error)
}

// RecordWriter keeps a unit's record of a kind, written by w; a new one replaces the unit's last of that kind.
type RecordWriter interface {
	PutRecord(ctx context.Context, u unit.Unit, kind string, w task.Writer, data []byte, at time.Time) error
}

// Metas keeps the daemon's small values by key.
type Metas interface {
	Meta(ctx context.Context, key string) (string, error)
	MetaPrefixed(ctx context.Context, prefix string) (map[string]string, error)
	SetMeta(ctx context.Context, key, value string) error
}

// Records reads what turns and briefs left behind.
type Records interface {
	Turn(ctx context.Context, id string) (port.SentText, bool, error)
	// Brief is u's stored brief; ok is false for none.
	Brief(ctx context.Context, u unit.Unit) (b intake.StoredBrief, ok bool, err error)
	// UnitlessEvents are the events of no source's unit, oldest first.
	UnitlessEvents(ctx context.Context) ([]port.Event, error)
}

// Collector is the sources' slow job, which rebuilds what their ingestion
// keeps cheap; skipped holds truncation and warnings, which surface in
// health without failing the job.
type Collector interface {
	SlowJob(ctx context.Context) (summary, skipped string, err error)
}

// IngestJob is a source's ingestion job; the daemon runs it as the job
// "ingest:<ID>".
type IngestJob struct {
	ID  unit.ProviderID
	Job ingest.Job
}
