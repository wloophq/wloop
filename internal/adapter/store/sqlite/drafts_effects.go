package sqlite

import (
	"context"
	"time"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
)

type draftEffects struct{ db *DB }

var _ outbox.AppliedStore = draftEffects{}

// DraftEffects keeps when the effects of a sent draft were applied.
func (db *DB) DraftEffects() outbox.AppliedStore { return draftEffects{db} }

func (s draftEffects) Unapplied(ctx context.Context) ([]draft.Draft, error) {
	rows, err := s.db.R().QueryContext(ctx, draftSelect+` WHERE state = 'sent' AND effects_applied_at = '' AND effects != '[]' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []draft.Draft
	for rows.Next() {
		d, err := scanDraft(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// MarkApplied writes no change note: nothing a reader shows moves.
func (s draftEffects) MarkApplied(ctx context.Context, id draft.ID, at time.Time) error {
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE drafts SET effects_applied_at = ? WHERE id = ? AND effects_applied_at = ''`, draftTime(at), int64(id))
		return err
	})
	return err
}
