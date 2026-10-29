package ghsource

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// reportedData is the data of the GitHub plugin's snapshot of a pull
// request, in the plugin's shape.
type reportedData struct {
	PR            dinv.PR           `json:"pr"`
	Detail        *inventory.Detail `json:"detail"`
	DetailVersion int               `json:"detail_version"`
	DetailReadAt  time.Time         `json:"detail_read_at"`
	Tracked       bool              `json:"tracked"`
	Adopted       bool              `json:"adopted"`
}

// Reported keeps the pull requests the GitHub plugin reports in st, the
// rows the in-process rows, facts and presenter read.
func Reported(st *inventory.Store) func(ctx context.Context, snaps map[unit.Unit]src.Snapshot) error {
	return func(ctx context.Context, snaps map[unit.Unit]src.Snapshot) error {
		rows := make([]inventory.Reported, 0, len(snaps))
		for u, s := range snaps {
			if s.Gone || len(s.Data) == 0 {
				continue
			}
			var d reportedData
			if err := json.Unmarshal(s.Data, &d); err != nil {
				return fmt.Errorf("the snapshot of %s: %w", u.Label(), err)
			}
			r := inventory.Reported{PR: d.PR, Detail: d.Detail, Tracked: d.Tracked, Adopted: d.Adopted}
			if r.Detail != nil {
				r.Detail.Version, r.Detail.ReadAt = d.DetailVersion, d.DetailReadAt
			}
			rows = append(rows, r)
		}
		return st.SaveReported(ctx, rows)
	}
}

// ReportedInputs are the pull requests the GitHub plugin last reported, as
// the facts, the holdings and a round's send read them: each row and its
// full read from its snapshot, the viewer the host keeps, the ended events
// from the host's log.
type ReportedInputs struct {
	Reports readmodel.Reports
	Store   *inventory.Store
}

var _ dinv.PlanInputs = ReportedInputs{}

func (r ReportedInputs) Viewer(ctx context.Context) (string, error) {
	return r.Store.Viewer(ctx, unit.GitHub)
}

// Rows are every reported pull request, in ref order.
func (r ReportedInputs) Rows(ctx context.Context) ([]dinv.PR, error) {
	data, err := r.data(ctx, nil)
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
	data, err := r.data(ctx, units)
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

// data are the reported data of units, of every reported unit when nil.
func (r ReportedInputs) data(ctx context.Context, units []unit.Unit) (map[unit.Unit]reportedData, error) {
	if r.Reports == nil {
		return nil, nil
	}
	if units == nil {
		var err error
		if units, err = r.Reports.Units(ctx, unit.GitHub); err != nil {
			return nil, err
		}
	}
	snaps, err := r.Reports.Reported(ctx, units)
	if err != nil {
		return nil, err
	}
	out := make(map[unit.Unit]reportedData, len(snaps))
	for u, s := range snaps {
		if s.Gone || len(s.Data) == 0 {
			continue
		}
		var d reportedData
		if err := json.Unmarshal(s.Data, &d); err != nil {
			return nil, fmt.Errorf("the snapshot of %s: %w", u.Label(), err)
		}
		if d.PR.Ref.Number == 0 {
			continue
		}
		out[u] = d
	}
	return out, nil
}

// PR is ref as the plugin last reported it; false when it reported none.
func (r ReportedInputs) PR(ctx context.Context, ref pr.Ref) (dinv.PR, bool, error) {
	data, err := r.data(ctx, []unit.Unit{ref.Unit()})
	if err != nil {
		return dinv.PR{}, false, err
	}
	d, ok := data[ref.Unit()]
	return d.PR, ok, nil
}
