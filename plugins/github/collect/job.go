package collect

import (
	"fmt"
	"strings"
)

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
