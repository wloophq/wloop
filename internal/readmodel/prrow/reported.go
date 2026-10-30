package prrow

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
)

// ReportedData is the data of the GitHub plugin's snapshot of a pull
// request that the host reads: the pull request, its full read and its
// overrides.
type ReportedData struct {
	PR            dinv.PR           `json:"pr"`
	Detail        *inventory.Detail `json:"detail"`
	DetailVersion int               `json:"detail_version"`
	DetailReadAt  time.Time         `json:"detail_read_at"`
	Tracked       bool              `json:"tracked"`
	Adopted       bool              `json:"adopted"`
}

// ReportedPRs are the pull requests the GitHub plugin last reported, as
// the host reads them; the host keeps no copy of them.
type ReportedPRs struct{ Reports readmodel.Reports }

// Data are the reported data of units, of every reported unit when nil;
// a unit with no pull request is absent.
func (r ReportedPRs) Data(ctx context.Context, units []unit.Unit) (map[unit.Unit]ReportedData, error) {
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
	out := make(map[unit.Unit]ReportedData, len(snaps))
	for u, s := range snaps {
		if s.Gone || len(s.Data) == 0 {
			continue
		}
		var d ReportedData
		if err := json.Unmarshal(s.Data, &d); err != nil {
			return nil, fmt.Errorf("the snapshot of %s: %w", u.Label(), err)
		}
		if d.PR.Ref.Number == 0 {
			continue
		}
		if d.Detail != nil {
			d.Detail.Version, d.Detail.ReadAt = d.DetailVersion, d.DetailReadAt
		}
		out[u] = d
	}
	return out, nil
}

// PR is ref as the plugin last reported it; false when it reported none.
func (r ReportedPRs) PR(ctx context.Context, ref pr.Ref) (dinv.PR, bool, error) {
	data, err := r.Data(ctx, []unit.Unit{ref.Unit()})
	if err != nil {
		return dinv.PR{}, false, err
	}
	d, ok := data[ref.Unit()]
	return d.PR, ok, nil
}

// PRs are refs as the plugin last reported them; a ref it reported none of
// is absent.
func (r ReportedPRs) PRs(ctx context.Context, refs []pr.Ref) (map[pr.Ref]dinv.PR, error) {
	units := make([]unit.Unit, len(refs))
	for i, ref := range refs {
		units[i] = ref.Unit()
	}
	data, err := r.Data(ctx, units)
	if err != nil {
		return nil, err
	}
	out := make(map[pr.Ref]dinv.PR, len(data))
	for _, d := range data {
		out[d.PR.Ref] = d.PR
	}
	return out, nil
}

// List are every reported pull request, the open ones alone when open, the
// last updated first.
func (r ReportedPRs) List(ctx context.Context, open bool) ([]dinv.PR, error) {
	data, err := r.Data(ctx, nil)
	if err != nil {
		return nil, err
	}
	out := make([]dinv.PR, 0, len(data))
	for _, d := range data {
		if !open || d.PR.State == "OPEN" {
			out = append(out, d.PR)
		}
	}
	slices.SortFunc(out, func(a, b dinv.PR) int {
		if c := b.UpdatedAt.Compare(a.UpdatedAt); c != 0 {
			return c
		}
		if c := strings.Compare(a.Ref.Owner, b.Ref.Owner); c != 0 {
			return c
		}
		if c := strings.Compare(a.Ref.Repo, b.Ref.Repo); c != 0 {
			return c
		}
		return b.Ref.Number - a.Ref.Number
	})
	return out, nil
}

// Details are the full reads of refs as reported; a ref with none is
// absent.
func (r ReportedPRs) Details(ctx context.Context, refs []pr.Ref) (map[pr.Ref]inventory.Detail, error) {
	units := make([]unit.Unit, len(refs))
	for i, ref := range refs {
		units[i] = ref.Unit()
	}
	data, err := r.Data(ctx, units)
	if err != nil {
		return nil, err
	}
	out := make(map[pr.Ref]inventory.Detail, len(data))
	for _, d := range data {
		if d.Detail != nil {
			out[d.PR.Ref] = *d.Detail
		}
	}
	return out, nil
}
