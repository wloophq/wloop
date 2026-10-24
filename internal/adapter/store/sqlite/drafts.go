package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type draftStore struct{ db *DB }

var _ outbox.Store = draftStore{}

// Drafts is the Outbox's store.
func (db *DB) Drafts() outbox.Store { return draftStore{db} }

const (
	draftFields = `provider, unit_key, items, entry, kind, target, text, reaction, original, edited,
	origin_kind, turn_seq, intent, workflow, rule, rule_key, approval_mode, approval_window, deadline, state,
	evidence_ref, evidence_permalink, evidence_at, error, reason, would_policy, would_reason, would_outcome,
	created_at, updated_at, sending_at`
	draftSelect = `SELECT id, rev, ` + draftFields + ` FROM drafts`
	draftOrder  = ` ORDER BY id DESC`
	insertDraft = `INSERT INTO drafts (` + draftFields + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	updateDraft = `UPDATE drafts SET (` + draftFields + `, rev) = (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, rev + 1)
	WHERE id = ? AND state = ? AND rev = ?`
)

func draftTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(TimeLayout)
}

func parseDraftTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(TimeLayout, s)
}

func draftArgs(d draft.Draft) []any {
	its := d.Items
	if its == nil {
		its = []string{}
	}
	list, _ := json.Marshal(its)
	o, a, w, ev := d.Origin, d.Approval, d.Would, d.Evidence
	return []any{string(d.Unit.Provider), d.Unit.Key, string(list), d.Entry, string(d.Kind), d.Target, d.Text, d.Reaction, d.Original, d.Edited,
		string(o.Kind), o.TurnSeq, o.Intent, o.Workflow, o.Rule, o.Key, string(a.Mode), int64(a.Window), draftTime(d.Deadline), string(d.State),
		ev.Ref, ev.Permalink, draftTime(ev.At), d.Error, d.Reason, string(w.Policy), w.Reason, string(w.Outcome),
		draftTime(d.CreatedAt), draftTime(d.UpdatedAt), draftTime(d.SendingAt)}
}

func scanDraft(row scanner) (draft.Draft, error) {
	var d draft.Draft
	var provider, list, kind, originKind, mode, state, policy, outcome string
	var window int64
	var deadline, evAt, created, updated, sending string
	if err := row.Scan(&d.ID, &d.Rev, &provider, &d.Unit.Key, &list, &d.Entry, &kind, &d.Target, &d.Text, &d.Reaction, &d.Original, &d.Edited,
		&originKind, &d.Origin.TurnSeq, &d.Origin.Intent, &d.Origin.Workflow, &d.Origin.Rule, &d.Origin.Key, &mode, &window, &deadline, &state,
		&d.Evidence.Ref, &d.Evidence.Permalink, &evAt, &d.Error, &d.Reason, &policy, &d.Would.Reason, &outcome,
		&created, &updated, &sending); err != nil {
		return draft.Draft{}, err
	}
	d.Unit.Provider, d.Kind, d.Origin.Kind, d.State = unit.ProviderID(provider), draft.Kind(kind), draft.OriginKind(originKind), draft.State(state)
	d.Approval = draft.Approval{Mode: draft.ApprovalMode(mode), Window: time.Duration(window)}
	d.Would.Policy, d.Would.Outcome = draft.WouldPolicy(policy), draft.Outcome(outcome)
	if err := json.Unmarshal([]byte(list), &d.Items); err != nil {
		return draft.Draft{}, fmt.Errorf("the items of draft %d: %w", d.ID, err)
	}
	for _, f := range []struct {
		dst *time.Time
		raw string
	}{{&d.Deadline, deadline}, {&d.Evidence.At, evAt}, {&d.CreatedAt, created}, {&d.UpdatedAt, updated}, {&d.SendingAt, sending}} {
		t, err := parseDraftTime(f.raw)
		if err != nil {
			return draft.Draft{}, fmt.Errorf("a time of draft %d: %w", d.ID, err)
		}
		*f.dst = t
	}
	return d, nil
}

// normal is d as a scan returns it: times in UTC, Items never nil.
func normal(d draft.Draft) draft.Draft {
	for _, t := range []*time.Time{&d.Deadline, &d.Evidence.At, &d.CreatedAt, &d.UpdatedAt, &d.SendingAt} {
		if !t.IsZero() {
			*t = t.UTC()
		}
	}
	if d.Items == nil {
		d.Items = []string{}
	}
	return d
}

func (s draftStore) Insert(ctx context.Context, d draft.Draft) (draft.Draft, error) {
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		res, err := tx.ExecContext(ctx, insertDraft, draftArgs(d)...)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		d.ID, d.Rev = draft.ID(id), 0
		tx.Touch(port.TopicDrafts, d.Unit)
		return nil
	})
	if err != nil {
		return draft.Draft{}, fmt.Errorf("insert draft: %w", err)
	}
	return normal(d), nil
}

func (s draftStore) Get(ctx context.Context, id draft.ID) (draft.Draft, error) {
	d, err := scanDraft(s.db.R().QueryRowContext(ctx, draftSelect+` WHERE id = ?`, int64(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return draft.Draft{}, fmt.Errorf("draft %d: %w", id, outbox.ErrNotFound)
	}
	return d, err
}

func (s draftStore) List(ctx context.Context, f outbox.Filter) ([]draft.Draft, error) {
	var where []string
	var args []any
	if len(f.States) > 0 {
		list, _ := json.Marshal(f.States)
		where, args = append(where, `state IN (SELECT value FROM json_each(?))`), append(args, string(list))
	}
	if f.Unit != nil {
		where, args = append(where, `provider = ? AND unit_key = ?`), append(args, string(f.Unit.Provider), f.Unit.Key)
	}
	for _, c := range []struct{ col, v string }{{"entry", f.Entry}, {"rule", f.Rule}, {"rule_key", f.Key}} {
		if c.v != "" {
			where, args = append(where, c.col+` = ?`), append(args, c.v)
		}
	}
	q := draftSelect
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, ` AND `)
	}
	rows, err := s.db.R().QueryContext(ctx, q+draftOrder, args...)
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

func (s draftStore) Update(ctx context.Context, d draft.Draft, from draft.State) (draft.Draft, error) {
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		res, err := tx.ExecContext(ctx, updateDraft, append(draftArgs(d), int64(d.ID), string(from), d.Rev)...)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 1 {
			if err == nil {
				tx.Touch(port.TopicDrafts, d.Unit)
			}
			return err
		}
		var state string
		var rev int64
		switch err := tx.QueryRowContext(ctx, `SELECT state, rev FROM drafts WHERE id = ?`, int64(d.ID)).Scan(&state, &rev); {
		case errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("draft %d: %w", d.ID, outbox.ErrNotFound)
		case err != nil:
			return err
		}
		return fmt.Errorf("draft %d is %s rev %d, not %s rev %d: %w", d.ID, state, rev, from, d.Rev, outbox.ErrConflict)
	})
	if err != nil {
		return draft.Draft{}, err
	}
	d.Rev++
	return normal(d), nil
}
