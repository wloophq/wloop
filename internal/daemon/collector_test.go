package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/core/ingest"
)

// failingSlow is a source's slow job that fails at once, as one over a
// closed store does.
type failingSlow struct{}

func (failingSlow) SlowJob(context.Context) (string, string, error) {
	return "", "", errors.New("sql: database is closed")
}

// failingIngest is an ingestion job that fails at once.
func failingIngest() ingest.Job {
	return ingest.Job{Every: time.Minute, Prepare: func(context.Context, time.Time) (ingest.Plan, string, error) {
		return ingest.Plan{}, "", errors.New("sql: database is closed")
	}}
}

// sweepReport is what a source's sweep reports, as a job's outcome says it.
type sweepReport struct {
	Queries, Fetched int
	Truncated        []string
	Skipped          string
	Warnings         []string
}

// sweepOutcome is rep as a job's outcome: truncation and warnings are the
// skipped text.
func sweepOutcome(rep sweepReport, err error) (summary, skipped string, _ error) {
	if err != nil {
		return "", "", err
	}
	if rep.Skipped != "" {
		return "skipped", rep.Skipped, nil
	}
	summary = fmt.Sprintf("%d queries, %d read", rep.Queries, rep.Fetched)
	var notes []string
	if len(rep.Truncated) > 0 {
		notes = append(notes, "truncated: "+strings.Join(rep.Truncated, ", "))
	}
	notes = append(notes, rep.Warnings...)
	return summary, strings.Join(notes, "; "), nil
}
