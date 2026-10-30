package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var reviewLabel = map[string]string{"APPROVED": "approved", "CHANGES_REQUESTED": "changes requested", "REVIEW_REQUIRED": "review required"}

func runExplore(ctx context.Context, e *Env, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: " + brand.Command + " explore <url|owner/repo|repo|login|@org/team|search>")
	}
	c, err := e.daemonClient(ctx)
	if err != nil {
		return err
	}
	res, err := c.Explore(ctx, strings.Join(args, " "))
	if err != nil {
		return err
	}
	rows, err := c.Rows(ctx)
	if err != nil {
		return err
	}
	printCandidates(e.Out, res, rows, e.Now())
	return nil
}

// printCandidates writes what the daemon already passed through OneLine.
func printCandidates(w io.Writer, res api.ExploreResult, rows []api.Row, now time.Time) {
	inventory := map[unit.Unit]api.Row{}
	for _, r := range rows {
		inventory[r.Unit] = r
	}
	if res.Read != "" {
		fmt.Fprintln(w, res.Read)
	}
	if res.Empty != "" {
		fmt.Fprintln(w, res.Empty)
		return
	}
	for _, c := range res.Candidates {
		review := reviewLabel[c.ReviewDecision]
		if review == "" {
			review = "no review required"
		}
		checks := "no checks"
		if c.ChecksState != "" {
			checks = "checks " + strings.ToLower(c.ChecksState)
		}
		facts := []string{c.Label, fmt.Sprintf("+%d -%d", c.Additions, c.Deletions), plural(c.ChangedFiles, "file", "files"),
			review, checks, "by " + c.Author}
		if since := waitingSince(c, inventory); !since.IsZero() {
			facts = append(facts, "waiting "+shortAge(now.Sub(since), false))
		}
		if c.IsDraft {
			facts = append(facts, "draft")
		}
		if c.State != "" && c.State != dgh.StateOpen {
			facts = append(facts, strings.ToLower(c.State))
		}
		fmt.Fprintln(w, strings.Join(facts, "  "))
		fmt.Fprintf(w, "  %s\n", c.Title)
		if r, ok := inventory[c.Unit]; ok {
			fmt.Fprintf(w, "  in the inventory: %s, waiting on %s\n", r.Kind, r.Summary.WaitsOn)
		}
		if c.Brief != nil && c.Brief.Brief != nil {
			fmt.Fprintf(w, "  brief: %s\n", c.Brief.Brief.Goal)
		}
	}
	fmt.Fprintf(w, "%d of %d for %s; cost %d, budget %d/%d\n", len(res.Candidates), res.Total, res.Query, res.Cost, res.RateLimit.Remaining, res.RateLimit.Limit)
}

// waitingSince prefers the inventory's own clock to the last update.
func waitingSince(c api.Candidate, inventory map[unit.Unit]api.Row) time.Time {
	if r, ok := inventory[c.Unit]; ok && !r.Summary.Since.IsZero() {
		return r.Summary.Since
	}
	return c.UpdatedAt
}
