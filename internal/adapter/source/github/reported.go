package ghsource

import (
	"context"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
)

// ReportedInputs are the pull requests the GitHub plugin last reported, as
// the facts, the holdings and a round's send read them: each row and its
// full read from its snapshot, the viewer the host keeps, the ended events
// from the host's log.
type ReportedInputs struct {
	Reports readmodel.Reports
	Store   *inventory.Store
}

func (r ReportedInputs) reported() prrow.ReportedPRs { return prrow.ReportedPRs{Reports: r.Reports} }

var _ dinv.PlanInputs = ReportedInputs{}

func (r ReportedInputs) Viewer(ctx context.Context) (string, error) {
	return r.Store.Viewer(ctx, unit.GitHub)
}

// Rows are every reported pull request, in ref order.
func (r ReportedInputs) Rows(ctx context.Context) ([]dinv.PR, error) {
	data, err := r.reported().Data(ctx, nil)
	if err != nil {
		return nil, err
	}
	out := make([]dinv.PR, 0, len(data))
	for _, d := range data {
		out = append(out, d.PR)
	}
	slices.SortFunc(out, func(a, b dinv.PR) int { return strings.Compare(a.Ref.String(), b.Ref.String()) })
	return out, nil
}

// Facts are rows as their reported full reads hold them; a read of another
// version gives a zero ReadAt, as the plugin has yet to read it again.
func (r ReportedInputs) Facts(ctx context.Context, rows []dinv.PR, viewer string) (map[unit.Unit]dinv.PlanFacts, error) {
	units := make([]unit.Unit, len(rows))
	for i, row := range rows {
		units[i] = row.Ref.Unit()
	}
	data, err := r.reported().Data(ctx, units)
	if err != nil {
		return nil, err
	}
	out := make(map[unit.Unit]dinv.PlanFacts, len(rows))
	for _, row := range rows {
		d := data[row.Ref.Unit()]
		var detail inventory.Detail
		if d.Detail != nil {
			detail = *d.Detail
		}
		f := dinv.PlanFacts{Facts: inventory.FactsOf(row, detail, viewer)}
		if d.Detail != nil && d.DetailVersion == inventory.DetailVersion {
			f.ReadAt = d.DetailReadAt
		}
		out[row.Ref.Unit()] = f
	}
	return out, nil
}

func (r ReportedInputs) Ended(ctx context.Context, kind string, units []unit.Unit) (map[unit.Unit]bool, error) {
	refs := make([]pr.Ref, 0, len(units))
	for _, u := range units {
		if ref, ok := pr.RefOf(u); ok {
			refs = append(refs, ref)
		}
	}
	ended, err := r.Store.Ended(ctx, kind, refs)
	if err != nil {
		return nil, err
	}
	out := make(map[unit.Unit]bool, len(ended))
	for ref, ok := range ended {
		out[ref.Unit()] = ok
	}
	return out, nil
}

// PR is ref as the plugin last reported it; false when it reported none.
func (r ReportedInputs) PR(ctx context.Context, ref pr.Ref) (dinv.PR, bool, error) {
	return r.reported().PR(ctx, ref)
}
