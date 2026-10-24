package inventory

import (
	"context"
	"database/sql"
	"errors"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/domain/placement"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Placements is the store of the user's placement overrides.
type Placements struct{ *Store }

var _ placement.Overrides = Placements{}

func (s Placements) Override(ctx context.Context, u unit.Unit) (placement.Placement, bool, error) {
	var p placement.Placement
	err := s.db.R().QueryRowContext(ctx, `SELECT repo, checkout, role, shared FROM placements WHERE provider = ? AND unit_key = ?`,
		string(u.Provider), u.Key).Scan(&p.Repo, &p.Checkout, &p.Role, &p.Shared)
	if errors.Is(err, sql.ErrNoRows) {
		return placement.Placement{}, false, nil
	}
	return p, err == nil, err
}

// SetOverride stores p for u; the zero Placement clears it. Source is not kept.
func (s Placements) SetOverride(ctx context.Context, u unit.Unit, p placement.Placement) error {
	p.Source = ""
	_, err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
		if p == (placement.Placement{}) {
			_, err := tx.ExecContext(ctx, `DELETE FROM placements WHERE provider = ? AND unit_key = ?`, string(u.Provider), u.Key)
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO placements (provider, unit_key, repo, checkout, role, shared, set_at) VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (provider, unit_key) DO UPDATE SET repo = excluded.repo, checkout = excluded.checkout, role = excluded.role,
			shared = excluded.shared, set_at = excluded.set_at`,
			string(u.Provider), u.Key, p.Repo, p.Checkout, p.Role, p.Shared, ts(s.clock()))
		return err
	})
	return err
}
