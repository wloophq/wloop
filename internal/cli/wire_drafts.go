package cli

import (
	"context"
	"errors"
	"strings"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/outbox"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// wireDrafts wires draft.propose's reads and the workflows' draft
// withdrawals; the effects' applier is the Outbox's (serveOutbox).
func (e *Env) wireDrafts(w *serveWiring) {
	d := e.draftDeps(w.DB.Tasks(), e.itemsLedger(w.DB, w.Cfg, w.Log))
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

func (e *Env) draftDeps(tasks draftTasks, its draftItems) *draftDeps {
	return &draftDeps{
		Outbox: func() draftOutbox {
			if e.outbox == nil {
				return nil
			}
			return e.outbox
		},
		Tasks: tasks, Items: its,
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
	ds, err := ob.List(ctx, outbox.Filter{States: []draft.State{draft.Held}, Workflow: name})
	if err != nil {
		return nil, err
	}
	var out []automation.ProposedDraft
	for _, d := range ds {
		if d.Origin.Kind != draft.OriginAuto || d.Origin.Workflow == "" || w.d.Enabled != nil && !w.d.Enabled(d.Unit.Provider) {
			continue
		}
		subject := draftSubjectUnit(d)
		pd := automation.ProposedDraft{ID: int64(d.ID), Workflow: d.Origin.Workflow, Subject: subject}
		sub, err := w.d.subject(ctx, subject)
		switch {
		case err != nil && d.Task != "" && errors.Is(err, coretasks.ErrNoTask):
			pd.Facts = workflow.Facts{"draft.person_waited": false, "draft.items_owed": 0, "draft.items_assigned": 0, "draft.unit_open": false}
		case err != nil:
			return nil, err
		default:
			person := draftPerson(d.Origin.Key, d.Unit, subject)
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
	_, err = ob.Withdraw(ctx, draft.ID(id), reason)
	if errors.Is(err, outbox.ErrConflict) {
		return nil
	}
	return err
}

// draftPerson is the person a per-person draft's key names after its unit, or, as drafts proposed before the
// key named the unit, after its subject; "" for a draft of no person.
func draftPerson(key string, u, subject unit.Unit) string {
	for _, prefix := range []string{u.String() + ":", subject.Key + ":"} {
		if p, ok := strings.CutPrefix(key, prefix); ok {
			return p
		}
	}
	return ""
}

// draftSubjectUnit is the subject a draft was proposed on: its task, else its unit.
func draftSubjectUnit(d draft.Draft) unit.Unit {
	if d.Task != "" {
		return task.UnitOf(task.ID(d.Task))
	}
	return d.Unit
}
