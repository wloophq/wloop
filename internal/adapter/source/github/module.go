package ghsource

import (
	"cmp"
	"context"
	"time"

	factsgithub "github.com/wloophq/wloop/internal/adapter/facts/github"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/ingest"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
	sourcereg "github.com/wloophq/wloop/internal/source"
)

// Module is the GitHub source as it runs in the host's process: the pull
// requests the GitHub plugin reports, served as prrow's rows. Its ingestion
// job reads nothing: the composition root makes the plugin's job its own.
func Module(cfg config.Config, d sourcereg.Deps) (sourcereg.Module, error) {
	var briefs prrow.Briefs
	if d.Briefs != nil {
		briefs = d.Briefs
	}
	n := &nudges{viewer: func(ctx context.Context) (string, error) { return d.Inventory.Viewer(ctx, unit.GitHub) },
		teams: func(ctx context.Context) ([]string, error) { return MyTeams(ctx, d.Inventory) }}
	return sourcereg.Module{
		ID:     unit.GitHub,
		Codec:  pr.Codec{},
		Ingest: idleJob(cfg),
		Rows: n.rows(func(rd readmodel.RowDeps) readmodel.Provider {
			return prrow.New(prrow.Source{RowDeps: rd, Reports: d.Reports, Store: d.Inventory, Briefs: briefs})
		}),
		Facts:      factsOf(cfg, d),
		Slow:       func(context.Context) (string, string, error) { return "skipped", "the GitHub plugin does not run", nil },
		Items:      Cards{Reports: d.Reports}.Items,
		Card:       Cards{Reports: d.Reports}.Card,
		Identity:   identity(d.Inventory),
		UnitEvents: unitEvents{d.Inventory},
		UI:         UI(),
		Publisher:  publisherOf(d),
		Proposals:  n.Proposals,
		Detail:     true,
		Briefs:     brief.GitHubSource{Store: d.Inventory, Source: brief.Source{GitHub: d.GitHub}, MaxDiffBytes: cfg.Brief.MaxInputBytes, Retry: time.Duration(cfg.Sweep.Slow)},
	}, nil
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
		Intents:  d.Intents,
		Reports:  d.Reports,
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

// idleJob is the ingestion job of a module whose plugin does not run: it
// reads nothing.
func idleJob(cfg config.Config) ingest.Job {
	return ingest.Job{Every: time.Duration(cmp.Or(cfg.Sweep.Fast, config.Defaults().Sweep.Fast)),
		Prepare: func(context.Context, time.Time) (ingest.Plan, string, error) {
			return ingest.Plan{}, "the GitHub plugin does not run", nil
		}}
}
