package ghsource

import (
	"cmp"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/ingest"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
)

// IngestJob is the fast pass as the source's ingestion job, on a fresh read
// of the workspaces' mutable part.
func (c *Collector) IngestJob() ingest.Job {
	return ingest.Job{Every: time.Duration(cmp.Or(c.Cfg.Sweep.Fast, config.Defaults().Sweep.Fast)), Prepare: func(ctx context.Context, now time.Time) (ingest.Plan, string, error) {
		c.WS.Invalidate(wsdomain.PartMutable)
		return c.fastPlan(ctx, now, func(Report) {})
	}}
}

// SlowJob runs Slow as a daemon job.
func (c *Collector) SlowJob(ctx context.Context) (summary, skipped string, err error) {
	return Outcome(c.Slow(ctx))
}

// Outcome is a sweep as a job's outcome: truncation and warnings become the
// skipped text, so they surface in health without failing the job.
func Outcome(rep Report, err error) (summary, skipped string, _ error) {
	if err != nil {
		return "", "", err
	}
	if rep.Skipped != "" {
		return "skipped", rep.Skipped, nil
	}
	summary = fmt.Sprintf("%d queries, %d stubs, %d read, %d events, %d failed", rep.Queries, rep.Stubs, rep.Fetched, rep.Events, rep.Failed)
	var notes []string
	if len(rep.Truncated) > 0 {
		notes = append(notes, "truncated: "+strings.Join(rep.Truncated, ", "))
	}
	notes = append(notes, rep.Warnings...)
	return summary, strings.Join(notes, "; "), nil
}
