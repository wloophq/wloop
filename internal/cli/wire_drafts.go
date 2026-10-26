package cli

import (
	"context"
	"errors"
	"strings"

	"github.com/wloophq/wloop/internal/core/automation"
	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// wireDrafts wires draft.propose's reads and the workflows' draft
// withdrawals; the effects' applier is the Outbox's (serveOutbox).
func (e *Env) wireDrafts(w *serveWiring) {
	d := e.draftDeps(w.DB.Entries(), e.itemsLedger(w.DB, w.Cfg, w.Log))
	var codecs []unit.Codec
	for _, m := range w.Modules {
		if m.Codec != nil {
			codecs = append(codecs, m.Codec)
		}
	}
	d.Codecs = unit.NewRegistry(codecs...)
	w.Engine.Drafts = d
	w.Engine.Withdrawals = draftWithdrawals{d: d}
}

func (e *Env) draftDeps(entries draftEntries, its draftItems) *draftDeps {
	return &draftDeps{
		Outbox: func() draftOutbox {
			if e.outbox == nil {
				return nil
			}
			return e.outbox
		},
		Entries: entries, Items: its,
		Workflow: func(name string) (workflow.Workflow, bool) {
			for _, w := range e.loadedIntents().Workflows {
				if w.Name == name {
					return w, true
				}
			}
			return workflow.Workflow{}, false
		},
		Config:  e.currentConfig,
		Now:     e.Now,
		Enabled: func(p unit.ProviderID) bool { return e.currentConfig().PluginEnabled(string(p)) },
	}
}

// draftWithdrawals are the proposed drafts the workflows proposed, with
// their draft slot's facts, and their withdrawal through the Outbox.
type draftWithdrawals struct{ d *draftDeps }

var _ automation.Withdrawals = draftWithdrawals{}

func (w draftWithdrawals) Proposed(ctx context.Context, name string) ([]automation.ProposedDraft, error) {
	ob, err := w.d.outbox()
	if errors.Is(err, errNoDraftDeps) {
		return nil, nil
	}
	ds, err := ob.List(ctx, outbox.Filter{States: []draft.State{draft.Proposed}, Workflow: name})
	if err != nil {
		return nil, err
	}
	var out []automation.ProposedDraft
	for _, d := range ds {
		if d.Origin.Kind != draft.OriginAuto || d.Origin.Workflow == "" || w.d.Enabled != nil && !w.d.Enabled(d.Unit.Provider) {
			continue
		}
		subject := d.Unit
		if d.Entry != "" {
			subject = gtd.UnitOf(gtd.ID(d.Entry))
		}
		pd := automation.ProposedDraft{ID: int64(d.ID), Workflow: d.Origin.Workflow, Subject: subject}
		sub, err := w.d.subject(ctx, subject)
		switch {
		case err != nil && d.Entry != "" && errors.Is(err, coregtd.ErrNoEntry):
			pd.Facts = workflow.Facts{"draft.person_waited": false, "draft.items_owed": 0, "draft.items_assigned": 0, "draft.unit_open": false}
		case err != nil:
			return nil, err
		default:
			person, _ := strings.CutPrefix(d.Origin.Key, subject.Key+":")
			if person == d.Origin.Key {
				person = ""
			}
			pd.Facts = draftFactsOf(sub, person, d.Items)
		}
		out = append(out, pd)
	}
	return out, nil
}

func (w draftWithdrawals) Withdraw(ctx context.Context, id int64, reason string) error {
	ob, err := w.d.outbox()
	if err != nil {
		return err
	}
	_, err = ob.Discard(ctx, draft.ID(id), reason)
	if errors.Is(err, outbox.ErrConflict) {
		return nil
	}
	return err
}
