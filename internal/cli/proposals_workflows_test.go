package cli

import (
	"context"
	"sync"
	"testing"
	"time"

	slacksource "github.com/wloophq/wloop/internal/adapter/source/slack"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/source"
)

// workflowProposer is the sweep engine as serve wires it, running the
// default workflows over the store, with the Slack plugin built and the
// drafts' effects applied once sent.
type workflowProposer struct {
	r   *replay
	eng *automation.Engine
}

func newWorkflowProposer(r *replay) *workflowProposer {
	r.t.Helper()
	te := r.te
	clock := clockFunc(func() time.Time { return r.now })
	m, err := slacksource.Module(te.cfg, source.Deps{DB: r.db, Clock: clock}, slacksource.Slack{})
	if err != nil {
		r.t.Fatal(err)
	}
	mods := []source.Module{m}
	ledger := te.itemsLedger(r.db, te.cfg, r.t.Logf)
	r.svc.Applier = outbox.EffectApplier{Store: r.db.DraftEffects(), Clock: clock,
		Effects: effectLookup(effectDeps{Nudges: te.entries(r.db, te.cfg, ledger), Modules: mods})}
	te.Env.outbox = r.svc
	ed := engineDeps{DB: r.db, Modules: mods, SessionFacts: noUnits{}, WorkspaceFacts: noUnits{}, Log: r.t.Logf}
	w := &serveWiring{DB: r.db, Cfg: te.cfg, Modules: mods, Log: r.t.Logf, Engine: &ed}
	te.wireSubjects(w)
	te.wireDrafts(w)
	return &workflowProposer{r: r, eng: te.newEngine(ed)}
}

func (p *workflowProposer) pass(ctx context.Context) error {
	return p.r.untilQuiet(func() error {
		_, _, err := p.eng.Sweep(ctx, &sync.Mutex{})
		return err
	})
}

func TestTheDefaultWorkflowsReproduceTheJobsDrafts(t *testing.T) {
	t.Parallel()
	r := newReplay(t)
	if d := firstDiff(r.run(newWorkflowProposer(r)), readGolden(t)); d != "" {
		t.Fatalf("the default workflows' drafts differ from the job's at %s", d)
	}
}

// TestACutOverReplayProposesNothingNewOnDayOne hands the store over from
// drafts shaped as the job left them, with no ledger run or mark of the
// workflows, to the default workflows after v4's adoption, midway through
// the replay: the record equals the job's alone, so the cut-over proposes
// nothing the job would not have, sent closures and open nudges included.
func TestACutOverReplayProposesNothingNewOnDayOne(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newReplay(t)
	cut := replayStart.AddDate(0, 0, 3)
	got := r.runFrom(newWorkflowProposer(r), cut, func() proposer {
		sqlitetest.Exec(t, r.db, `UPDATE drafts SET rule = substr(rule, 7), workflow = '', effects = '[]' WHERE workflow LIKE 'slack-%'`)
		for _, table := range []string{"plan_items", "workflow_runs", "workflow_cursors", "unit_marks"} {
			sqlitetest.Exec(t, r.db, "DELETE FROM "+table)
		}
		if _, err := r.db.Write(ctx, func(tx *sqlite.Tx) error { return sqlite.AdoptJobDrafts(ctx, tx.Tx) }); err != nil {
			t.Fatal(err)
		}
		return newWorkflowProposer(r)
	})
	if d := firstDiff(got, readGolden(t)); d != "" {
		t.Fatalf("the cut-over moved the drafts from the job's at %s", d)
	}
}
