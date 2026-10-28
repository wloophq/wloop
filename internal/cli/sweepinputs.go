package cli

import (
	"context"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// storeInputs are the sweep's pull requests as the inventory stores them.
type storeInputs struct{ store *inventory.Store }

func (i storeInputs) Viewer(ctx context.Context) (string, error) {
	return i.store.Viewer(ctx, unit.GitHub)
}

func (i storeInputs) Rows(ctx context.Context) ([]dinv.PR, error) {
	return i.store.ListPRs(ctx, false)
}

// Facts reads the details of rows in one query; a detail of another version
// gives its facts with a zero ReadAt, as the collector has yet to re-read it.
func (i storeInputs) Facts(ctx context.Context, rows []dinv.PR, viewer string) (map[unit.Unit]dinv.PlanFacts, error) {
	refs := make([]pr.Ref, len(rows))
	for n, row := range rows {
		refs[n] = row.Ref
	}
	details, err := i.store.Details(ctx, refs)
	if err != nil {
		return nil, err
	}
	out := make(map[unit.Unit]dinv.PlanFacts, len(rows))
	for _, row := range rows {
		d, ok := details[row.Ref]
		f := dinv.PlanFacts{Facts: inventory.FactsOf(row, d, viewer)}
		if ok && d.Version == inventory.DetailVersion {
			f.ReadAt = d.ReadAt
		}
		out[row.Ref.Unit()] = f
	}
	return out, nil
}

func (i storeInputs) Ended(ctx context.Context, kind string, units []unit.Unit) (map[unit.Unit]bool, error) {
	ended, err := i.store.Ended(ctx, kind, refsOf(units))
	if err != nil {
		return nil, err
	}
	out := make(map[unit.Unit]bool, len(ended))
	for ref, ok := range ended {
		out[ref.Unit()] = ok
	}
	return out, nil
}
