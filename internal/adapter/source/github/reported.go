package ghsource

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/unit"
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
