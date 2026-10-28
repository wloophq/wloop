package ghsource

import (
	"context"
	"time"

	factsgithub "github.com/wloophq/wloop/internal/adapter/facts/github"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/ingest"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
	sourcereg "github.com/wloophq/wloop/internal/source"
)

// Module is the GitHub source: pull requests read by the fast pass and
// served as prrow's rows.
func Module(cfg config.Config, d sourcereg.Deps) (sourcereg.Module, error) {
	c := &Collector{Store: d.Inventory, GH: d.GitHub, WS: d.WS, Sink: d.Sink, Cfg: cfg, Now: d.Clock.Now, Log: d.Log, Units: d.Units, Links: d.Links}
	var briefs prrow.Briefs
	if d.Briefs != nil {
		briefs = d.Briefs
	}
	var read func(context.Context, github.Ref) (port.TrackRead, error)
	if d.GitHub != nil {
		read = c.Track
	}
	track, untrack := Follows(d.Inventory, read, d.Units, d.Clock.Now)
	untrackFinished := func(ctx context.Context) { UntrackFinished(ctx, d.Inventory, d.Clock.Now, logOf(d.Log)) }
	n := &nudges{viewer: func(ctx context.Context) (string, error) { return d.Inventory.Viewer(ctx, unit.GitHub) },
		teams: func(ctx context.Context) ([]string, error) { return MyTeams(ctx, d.Inventory) }}
	return sourcereg.Module{
		ID:     unit.GitHub,
		Codec:  pr.Codec{},
		Ingest: untrackingAfter(c.IngestJob(), untrackFinished),
		Rows: n.rows(func(rd readmodel.RowDeps) readmodel.Provider {
			return prrow.New(prrow.Source{RowDeps: rd, Store: d.Inventory, Briefs: briefs})
		}),
		Track:   track,
		Untrack: untrack,
		Facts:   factsOf(cfg, d),
		Slow: func(ctx context.Context) (string, string, error) {
			summary, skipped, err := c.SlowJob(ctx)
			untrackFinished(ctx)
			return summary, skipped, err
		},
		Items:      Items,
		Card:       Card,
		Identity:   identity(d.Inventory),
		UnitEvents: unitEvents{d.Inventory},
		UI:         UI(),
		Publisher:  publisherOf(d),
		Proposals:  n.Proposals,
		Checkouts:  checkouts{store: storeOrNil(d)},
		Detail:     true,
		Briefs:     brief.GitHubSource{Store: d.Inventory, Source: brief.Source{GitHub: d.GitHub}, MaxDiffBytes: cfg.Brief.MaxInputBytes, Retry: time.Duration(cfg.Sweep.Slow)},
	}, nil
}

// storeOrNil is d's inventory, nil when there is none.
func storeOrNil(d sourcereg.Deps) prReader {
	if d.Inventory == nil {
		return nil
	}
	return d.Inventory
}

// factsOf is the source's workflow facts over the shared handles.
func factsOf(cfg config.Config, d sourcereg.Deps) *factsgithub.Provider {
	fd := factsgithub.Deps{
		Inputs:   factsgithub.InventoryInputs(d.Inventory),
		Store:    d.Inventory,
		Snapshot: factsgithub.SnapshotOf(d.WS),
		Git:      factsgithub.LocalGit(d.Git),
		Branches: d.Git,
		Cfg:      func() config.Config { return cfg },
		Sink:     d.Sink,
		Clock:    d.Clock,
		Teams:    func(ctx context.Context) ([]string, error) { return MyTeams(ctx, d.Inventory) },
		Agents:   d.Agents,
		Intents:  d.Intents,
	}
	if d.Changes != nil {
		fd.Changes = d.Changes
	}
	if d.DB != nil {
		fd.Rounds = sqlite.NewRounds(d.DB)
	}
	if d.Turns != nil {
		fd.Books = d.Turns.Store
	}
	return factsgithub.New(fd)
}

// untrackingAfter is j with untrack run after each run.
func untrackingAfter(j ingest.Job, untrack func(context.Context)) ingest.Job {
	prepare := j.Prepare
	j.Prepare = func(ctx context.Context, now time.Time) (ingest.Plan, string, error) {
		p, skip, err := prepare(ctx, now)
		if err != nil || skip != "" || p.After == nil {
			return p, skip, err
		}
		after := p.After
		p.After = func(ctx context.Context, rep ingest.Report, err error) (string, string, error) {
			summary, skipped, err := after(ctx, rep, err)
			untrack(ctx)
			return summary, skipped, err
		}
		return p, skip, err
	}
	return j
}

func logOf(logf func(string, ...any)) func(string, ...any) {
	if logf == nil {
		return func(string, ...any) {}
	}
	return logf
}
