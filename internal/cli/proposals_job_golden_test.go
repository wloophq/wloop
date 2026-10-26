package cli

import (
	"context"
	"os"
	"testing"
	"time"

	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/proposals"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// jobProposer is the proposals job as serve ran it: GitHub's plugin rules
// plugged in, so its entries get no generic nudge, and the sent nudges
// stamped by Stamp.
type jobProposer struct {
	r   *replay
	job *proposals.Job
}

func newJobProposer(r *replay) *jobProposer {
	cfg := r.te.cfg
	ledger := r.te.itemsLedger(r.db, cfg, r.t.Logf)
	none := func(context.Context, time.Time) ([]outbox.Proposal, error) { return nil, nil }
	return &jobProposer{r: r, job: &proposals.Job{
		Outbox: r.svc, Entries: r.db.Entries(), Nudges: r.te.entries(r.db, cfg, ledger), Items: ledger,
		Sources:   []proposals.Source{{Provider: unit.GitHub, Proposals: none}},
		Facts:     coregtd.CardFacts{Cards: r.db.Cards()},
		Approval:  cfg.Proposals.Approval,
		StaleDays: cfg.GTD.StaleWaitingDays, RenudgeAfter: time.Duration(cfg.Proposals.RenudgeAfter),
		Clock: clockFunc(func() time.Time { return r.now }), Log: r.t.Logf,
	}}
}

func (j *jobProposer) pass(ctx context.Context) error {
	return j.r.untilQuiet(func() error {
		if err := j.job.Stamp(ctx); err != nil {
			return err
		}
		_, err := j.job.Run(ctx)
		return err
	})
}

// TestTheProposalsJobsGolden captures what the proposals job proposes over
// the seeded fourteen days; go test -update rewrites it.
func TestTheProposalsJobsGolden(t *testing.T) {
	r := newReplay(t)
	got := r.run(newJobProposer(r))
	if *update {
		if err := os.WriteFile(replayGolden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if d := firstDiff(got, readGolden(t)); d != "" {
		t.Fatalf("the job's drafts moved from the golden at %s", d)
	}
}
