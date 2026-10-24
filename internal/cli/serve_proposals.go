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

// serveProposals runs the proposals job until ctx ends; serve --no-jobs proposes nothing.
func (e *Env) serveProposals(ctx context.Context, db *sqlite.DB, cfg config.Config, mods []source.Module, logf func(string, ...any)) {
	if e.noJobs || e.changes == nil {
		return
	}
	job := e.proposalsJob(db, cfg, mods, logf)
	go func() {
		lctx, scope := meter.Start(ctx, meter.Loop("proposals"))
		defer scope.End()
		job.Watch(lctx, e.changes, proposalsDebounce, proposalsEvery)
	}()
}

// proposalsJob proposes through an outbox service of its own, over the built modules' publishers; the sender
// that publishes the drafts runs elsewhere, so the job's Stamp catches up with the nudges it sent.
func (e *Env) proposalsJob(db *sqlite.DB, cfg config.Config, mods []source.Module, logf func(string, ...any)) *proposals.Job {
	ledger := e.itemsLedger(db, cfg, logf)
	renudge := time.Duration(cfg.Proposals.RenudgeAfter)
	svc := &outbox.Service{Store: db.Drafts(), Effects: publishersOf(mods), Items: ledger, Clock: clockFunc(e.Now), Log: logf,
		ObservedFor: renudge}
	var srcs []proposals.Source
	for _, m := range mods {
		if m.Proposals != nil {
			srcs = append(srcs, proposals.Source{Provider: m.ID, Proposals: m.Proposals})
		}
	}
	return &proposals.Job{
		Outbox: svc, Entries: db.Entries(), Nudges: e.entries(db, cfg, ledger), Items: ledger, Sources: srcs,
		Enabled:   func(p unit.ProviderID) bool { return cfg.PluginEnabled(string(p)) },
		Facts:     coregtd.CardFacts{Cards: db.Cards(), Enabled: func(p unit.ProviderID) bool { return cfg.PluginEnabled(string(p)) }},
		Approval:  cfg.Proposals.Approval,
		StaleDays: cfg.GTD.StaleWaitingDays, RenudgeAfter: renudge,
		Clock: clockFunc(e.Now), Log: logf,
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
