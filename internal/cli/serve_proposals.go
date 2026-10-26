package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/config"
	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/proposals"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/source"
)

const (
	proposalsDebounce = 2 * time.Second
	proposalsEvery    = time.Hour
)

// serveProposals runs the plugins' proposals job until ctx ends, when a
// built module proposes on its own; serve --no-jobs proposes nothing.
func (e *Env) serveProposals(ctx context.Context, db *sqlite.DB, cfg config.Config, mods []source.Module, logf func(string, ...any)) {
	if e.noJobs || e.changes == nil {
		return
	}
	job := e.proposalsJob(db, cfg, mods, logf)
	if len(job.Sources) == 0 {
		return
	}
	go func() {
		lctx, scope := meter.Start(ctx, meter.Loop("proposals"))
		defer scope.End()
		job.Watch(lctx, e.changes, proposalsDebounce, proposalsEvery)
	}()
}

// proposalsJob proposes the built modules' own drafts through an outbox
// service of its own, over their publishers; the sender that publishes the
// drafts, and applies their effects, runs elsewhere.
func (e *Env) proposalsJob(db *sqlite.DB, cfg config.Config, mods []source.Module, logf func(string, ...any)) *proposals.Job {
	renudge := time.Duration(cfg.Proposals.RenudgeAfter)
	svc := &outbox.Service{Store: db.Drafts(), Effects: publishersOf(mods), Items: e.itemsLedger(db, cfg, logf), Clock: clockFunc(e.Now), Log: logf,
		ObservedFor: renudge}
	var srcs []proposals.Source
	for _, m := range mods {
		if m.Proposals != nil {
			srcs = append(srcs, proposals.Source{Provider: m.ID, Proposals: m.Proposals})
		}
	}
	enabled := func(p unit.ProviderID) bool { return cfg.PluginEnabled(string(p)) }
	return &proposals.Job{
		Outbox: svc, Sources: srcs, Enabled: enabled,
		Facts:        coregtd.CardFacts{Cards: db.Cards(), Enabled: enabled},
		Approval:     cfg.Proposals.Approval,
		RenudgeAfter: renudge,
		Clock:        clockFunc(e.Now), Log: logf,
	}
}

// publishersOf is the outbox's Effects over the built modules: a provider whose module is not built
// (disabled) or has no Publisher answers ErrDisabled.
func publishersOf(mods []source.Module) outbox.Effects {
	return func(p unit.ProviderID) (port.Effects, error) {
		for _, m := range mods {
			if m.ID == p && m.Publisher != nil {
				return m.Publisher, nil
			}
		}
		return nil, fmt.Errorf("%s: %w", p, outbox.ErrDisabled)
	}
}
