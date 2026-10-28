package ingest

import (
	"context"
	"fmt"
	"time"
)

// Job is a source's ingestion job: the daemon runs it every Every with RunJob.
type Job struct {
	Every time.Duration
	// Prepare builds one run's registration; a non-empty skip, such as a
	// low budget, runs nothing and is the job's skipped text.
	Prepare func(ctx context.Context, now time.Time) (p Plan, skip string, err error)
}

// Plan is one run of a Job.
type Plan struct {
	Registration
	// After runs once Run returns, with its report and error, and gives the
	// job's outcome; nil summarizes the report and returns Run's error.
	After func(ctx context.Context, rep Report, err error) (summary, skipped string, _ error)
}

// RunJob is one run of j: Prepare, Run, After.
func RunJob(ctx context.Context, j Job, now time.Time) (summary, skipped string, err error) {
	p, skip, err := j.Prepare(ctx, now)
	if err != nil {
		return "", "", err
	}
	if skip != "" {
		return "skipped", skip, nil
	}
	rep, err := Run(ctx, p.Registration, now)
	if p.After != nil {
		return p.After(ctx, rep, err)
	}
	if err != nil {
		return "", "", err
	}
	return fmt.Sprintf("%d discovered, %d read", rep.Discovered, rep.Moved), "", nil
}
