package daemon

import (
	"context"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/ingest"
	"github.com/wloophq/wloop/internal/core/port"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
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

// PullRequests reads the stored pull requests and sets the workspace linked
// to one.
type PullRequests interface {
	PR(ctx context.Context, u unit.Unit) (dinv.PR, bool, error)
	PRs(ctx context.Context, units []unit.Unit) (map[unit.Unit]dinv.PR, error)
	ListPRs(ctx context.Context, open bool) ([]dinv.PR, error)
	Tracked(ctx context.Context) ([]unit.Unit, error)
	SetWorkspace(ctx context.Context, u unit.Unit, ws dinv.Workspace) error
}

// EventLog reads and records pull request events.
type EventLog interface {
	Events(ctx context.Context, u unit.Unit) ([]dinv.Event, error)
	RecentActivity(ctx context.Context, limit int) ([]port.Activity, error)
	RecordEvent(ctx context.Context, ev port.Event) error
	RecordSituations(ctx context.Context, sits []dinv.Situation) error
}

// Overrides reads and writes the user's overrides.
type Overrides interface {
	Override(ctx context.Context, u unit.Unit) (dinv.Override, error)
	Overrides(ctx context.Context) (map[unit.Unit]dinv.Override, error)
	// SetOverrideIf writes o only while the stored override is was.
	SetOverrideIf(ctx context.Context, was, o dinv.Override) (written bool, err error)
	ClearMute(ctx context.Context, u unit.Unit, kind, waitingOn string) (cleared bool, err error)
	// SetNextGuidance keeps guidance for u's next automatic round.
	SetNextGuidance(ctx context.Context, u unit.Unit, guidance string) error
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
	BriefRecord(ctx context.Context, u unit.Unit) (dinv.BriefRecord, bool, error)
	BriefRun(ctx context.Context, id int64) (dinv.BriefRun, bool, error)
	Timings(ctx context.Context, since time.Time) (dinv.TimingsData, error)
	// UnitlessEvents are the events of no source's unit, oldest first.
	UnitlessEvents(ctx context.Context) ([]port.Event, error)
}

// Collector rebuilds GitHub's scope and catches what the searches no longer
// return on the slow job; skipped holds truncation and warnings, which
// surface in health without failing the job.
type Collector interface {
	SlowJob(ctx context.Context) (summary, skipped string, err error)
}

// IngestJob is a source's ingestion job; the daemon runs it as the job
// "ingest:<ID>".
type IngestJob struct {
	ID  unit.ProviderID
	Job ingest.Job
}
