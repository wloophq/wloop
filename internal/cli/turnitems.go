package cli

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/core/instructions"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/items"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/turn"
)

// turnItems lists a unit's open items for its turns' instructions.
type turnItems struct{ ledger turns.Ledger }

func (r turnItems) OpenItems(ctx context.Context, u unit.Unit) ([]instructions.OpenItem, error) {
	its, err := r.ledger.Items(ctx, u)
	if err != nil {
		return nil, err
	}
	its = slices.DeleteFunc(its, func(it items.Item) bool { return it.State != items.Open })
	slices.SortFunc(its, func(a, b items.Item) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), strings.Compare(a.ID, b.ID))
	})
	out := make([]instructions.OpenItem, 0, len(its))
	for _, it := range its {
		waiting := make([]string, len(it.WaitingOn))
		for i, p := range it.WaitingOn {
			waiting[i] = p.Key
		}
		o := instructions.OpenItem{ID: it.ID, Kind: string(it.Kind), Summary: it.Summary, WaitingOn: strings.Join(waiting, ", ")}
		if !it.CreatedAt.IsZero() {
			o.Since = it.CreatedAt.UTC().Format(time.DateOnly)
		}
		out = append(out, o)
	}
	return out, nil
}

// itemsReader is the turn builder's items; nil when the turns keep no ledger.
func (e *Env) itemsReader() turn.ItemsReader {
	if e.Turns == nil || e.Turns.Items == nil {
		return nil
	}
	return turnItems{e.Turns.Items}
}

// turnHistory is the earlier turns of u's book b as instructions carry them,
// each naming its ledger ops with their logged outcomes.
func (e *Env) turnHistory(ctx context.Context, u unit.Unit, b dturn.Book) ([]instructions.PastTurn, error) {
	b.Unit, b.HistoryArtifact = u, e.historyArtifact
	log, err := e.turnOpsLog(ctx, b)
	if err != nil {
		return nil, err
	}
	return instructions.History(b, instructions.HistoryMax, log), nil
}

// turnOpsLog is what the ledger logged for the ops of b's turns since the
// oldest turn the history carries; nil when no record of b has an op.
func (e *Env) turnOpsLog(ctx context.Context, b dturn.Book) ([]dturn.OpOutcome, error) {
	if e.Turns == nil || e.Turns.Items == nil || !slices.ContainsFunc(b.Records, func(r dturn.Record) bool { return len(r.Record.Ledger) > 0 }) {
		return nil, nil
	}
	var since time.Time
	for i, n := len(b.Turns)-1, 0; i >= 0 && n < instructions.HistoryMax; i-- {
		if t := b.Turns[i]; t.State != dturn.Withdrawn {
			since, n = t.SentAt, n+1
		}
	}
	logged, err := e.Turns.Items.Log(ctx, b.Unit, since)
	if err != nil {
		return nil, err
	}
	var out []dturn.OpOutcome
	for _, l := range logged {
		if l.Provenance == items.Turn {
			out = append(out, dturn.OpOutcome{Op: l.Op, Outcome: l.Outcome, Reason: l.Reason, At: l.At})
		}
	}
	return out, nil
}
