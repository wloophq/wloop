package inventory

import (
	"context"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/port"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
)

// FillMerged writes the merger and merge commit of e, a KindMerged event, into
// the stored one at the same time where those are empty: merged events stored
// before the collector read them have neither. It changes no other field.
func (s *Store) FillMerged(ctx context.Context, e dinv.Event) (bool, error) {
	var filled bool
	_, err := s.db.Write(ctx, func(tx *sqlite.Tx) (err error) {
		filled, err = fillMerged(ctx, tx, e)
		return err
	})
	return filled, err
}

func fillMerged(ctx context.Context, tx *sqlite.Tx, e dinv.Event) (bool, error) {
	res, err := tx.ExecContext(ctx, `UPDATE events SET actor = iif(actor = '', ?, actor), data = iif(data = '', ?, data)
		WHERE provider = ? AND unit_key = ? AND kind = ? AND key = '' AND at = ?
		AND ((actor = '' AND ? != '') OR (data = '' AND ? != ''))`,
		append(append([]any{e.Actor, e.Data}, unitArgs(e.PR)...), dinv.KindMerged, ts(e.At), e.Actor, e.Data)...)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if n > 0 {
		tx.Touch(port.TopicSourceEvents, e.PR.Unit())
	}
	return n > 0, err
}
