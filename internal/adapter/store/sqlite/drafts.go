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
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type draftStore struct{ db *DB }

var _ outbox.Store = draftStore{}

// Drafts is the Outbox's store.
func (db *DB) Drafts() outbox.Store { return draftStore{db} }

const (
	draftContentFields = `provider, unit_key, items, task, kind, target, text, reaction, original, edited,
	origin_kind, turn_seq, intent, workflow, rule, rule_key, state,
	evidence_ref, evidence_permalink, evidence_at, error, reason,
	created_at, updated_at, sending_at, purpose, delivery, effects, effects_applied_at, origin_provider, origin_unit_key, replaced,
	origin_session, why, language, broadcast, payload, basis, hold`
	draftFields        = draftContentFields + `, by_kind, by_session`
	draftSelect = `SELECT id, rev, ` + draftFields + ` FROM drafts`
	draftOrder  = ` ORDER BY id DESC`
	insertDraft = `INSERT INTO drafts (` + draftFields + `, rev) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`
	// updateDraft's author is the stored one while the content stays, and once it changes the one given, a
	// person when none is; the expressions read the row as it was.
	updateDraft = `UPDATE drafts SET (` + draftContentFields + `, rev) = (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, rev + 1),
	by_kind = CASE WHEN text = ? AND payload = ? AND basis = ? THEN by_kind WHEN ? != '' THEN ? ELSE ? END,
	by_session = CASE WHEN text = ? AND payload = ? AND basis = ? THEN by_session WHEN ? != '' THEN ? ELSE '' END
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
	d = shaped(d)
	its := d.Items
	if its == nil {
		its = []string{}
	}
	list, _ := json.Marshal(its)
	effects, _ := json.Marshal(d.Effects)
	o, ev := d.Origin, d.Evidence
	return []any{string(d.Unit.Provider), d.Unit.Key, string(list), d.Task, string(d.Kind), d.Target, d.Text, d.Reaction, d.Original, d.Edited,
		string(o.Kind), o.TurnSeq, o.Intent, o.Workflow, o.Rule, o.Key, string(d.State),
		ev.Ref, ev.Permalink, draftTime(ev.At), d.Error, d.Reason,
		draftTime(d.CreatedAt), draftTime(d.UpdatedAt), draftTime(d.SendingAt), d.Purpose, string(d.Delivery), string(effects),
		draftTime(d.EffectsAppliedAt), string(o.Unit.Provider), o.Unit.Key, d.Replaced, o.Session, d.Why, d.Language, string(d.Broadcast),
		string(d.Payload), d.Basis, d.Hold, string(d.By.Kind), d.By.Session}
}

// shaped is d with the purpose and delivery of its kind when it names
// neither, as a draft an older release wrote does, its kind theirs, and
// Effects never nil.
func shaped(d draft.Draft) draft.Draft {
	if d.Purpose == "" && d.Delivery == "" {
		d.Purpose, d.Delivery = draft.ShapeOf(d.Kind)
	}
	d.Kind = draft.KindOf(d.Purpose, d.Delivery)
	if d.Effects == nil {
		d.Effects = []string{}
	}
	return d
}

func scanDraft(row scanner) (draft.Draft, error) {
	var d draft.Draft
	var provider, list, kind, originKind, state, delivery, effects, originProvider string
	var evAt, created, updated, sending, applied, broadcast, payload, by string
	if err := row.Scan(&d.ID, &d.Rev, &provider, &d.Unit.Key, &list, &d.Task, &kind, &d.Target, &d.Text, &d.Reaction, &d.Original, &d.Edited,
		&originKind, &d.Origin.TurnSeq, &d.Origin.Intent, &d.Origin.Workflow, &d.Origin.Rule, &d.Origin.Key, &state,
		&d.Evidence.Ref, &d.Evidence.Permalink, &evAt, &d.Error, &d.Reason,
		&created, &updated, &sending, &d.Purpose, &delivery, &effects, &applied, &originProvider, &d.Origin.Unit.Key, &d.Replaced,
		&d.Origin.Session, &d.Why, &d.Language, &broadcast, &payload, &d.Basis, &d.Hold, &by, &d.By.Session); err != nil {
		return draft.Draft{}, err
	}
	d.Unit.Provider, d.Kind, d.Origin.Kind, d.State = unit.ProviderID(provider), draft.Kind(kind), draft.OriginKind(originKind), draft.State(state)
	d.Delivery, d.Origin.Unit.Provider, d.Broadcast = draft.Delivery(delivery), unit.ProviderID(originProvider), draft.Broadcast(broadcast)
	if payload != "" {
		d.Payload = json.RawMessage(payload)
	}
	d.By.Kind = draft.ActorKind(by)
	if err := json.Unmarshal([]byte(list), &d.Items); err != nil {
		return draft.Draft{}, fmt.Errorf("the items of draft %d: %w", d.ID, err)
	}
	if err := json.Unmarshal([]byte(effects), &d.Effects); err != nil {
		return draft.Draft{}, fmt.Errorf("the effects of draft %d: %w", d.ID, err)
	}
	for _, f := range []struct {
		dst *time.Time
		raw string
	}{{&d.Evidence.At, evAt}, {&d.CreatedAt, created}, {&d.UpdatedAt, updated}, {&d.SendingAt, sending}, {&d.EffectsAppliedAt, applied}} {
		t, err := parseDraftTime(f.raw)
		if err != nil {
			return draft.Draft{}, fmt.Errorf("a time of draft %d: %w", d.ID, err)
		}
		*f.dst = t
	}
	return shaped(d), nil
}

// normal is d as a scan returns it: shaped, times in UTC, Items never nil.
func normal(d draft.Draft) draft.Draft {
	d = shaped(d)
	for _, t := range []*time.Time{&d.Evidence.At, &d.CreatedAt, &d.UpdatedAt, &d.SendingAt, &d.EffectsAppliedAt} {
		if !t.IsZero() {
			*t = t.UTC()
		}
	}
	if d.Items == nil {
		d.Items = []string{}
	}
	if len(d.Payload) == 0 {
		d.Payload = nil
	}
	return d
}

func (s draftStore) Insert(ctx context.Context, d draft.Draft) (draft.Draft, error) {
	if d.By.Kind == "" {
		d.By = proposer(d.Origin)
	}
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		res, err := tx.ExecContext(ctx, insertDraft, draftArgs(d)...)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		d.ID, d.Rev = draft.ID(id), 1
		if _, err := tx.ExecContext(ctx, insertRevision, revisionArgs(d, d.CreatedAt)...); err != nil {
			return err
		}
		tx.Touch(port.TopicDrafts, d.Unit)
		return touchDraftTask(ctx, tx, d.Task)
	})
	if err != nil {
		return draft.Draft{}, fmt.Errorf("insert draft: %w", err)
	}
	return normal(d), nil
}

// pendingDraft says a draft in state s is one of its task's pending drafts.
func pendingDraft(s draft.State) bool { return s == draft.Held || s == draft.Failed }

// touchDraftTask marks the stored task a draft serves changed at the next task position, so a page read since
// an earlier one lists it with its pending drafts; nothing for no task or one not stored. A stored task has
// written the position.
func touchDraftTask(ctx context.Context, tx *Tx, id string) error {
	if id == "" {
		return nil
	}
	res, err := tx.ExecContext(ctx, `UPDATE task_position SET rev = rev + 1 WHERE only = 1 AND EXISTS (SELECT 1 FROM tasks WHERE id = ?)`, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tasks SET rev = (SELECT rev FROM task_position WHERE only = 1) WHERE id = ?`, id); err != nil {
		return err
	}
	tx.Touch(port.TopicTasks, task.UnitOf(task.ID(id)))
	return nil
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
	for _, c := range []struct{ col, v string }{{"task", f.Task}, {"rule", f.Rule}, {"rule_key", f.Key}, {"workflow", f.Workflow}} {
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

var _ outbox.SettledReader = draftStore{}

func (s draftStore) SettledAt(ctx context.Context, workflow string) (map[unit.Unit]time.Time, error) {
	states, err := json.Marshal(draft.SettledStates)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.R().QueryContext(ctx, `SELECT provider, unit_key, task, updated_at FROM drafts
		WHERE workflow = ? AND state IN (SELECT value FROM json_each(?))`, workflow, string(states))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[unit.Unit]time.Time{}
	for rows.Next() {
		var d draft.Draft
		var updated string
		if err := rows.Scan(&d.Unit.Provider, &d.Unit.Key, &d.Task, &updated); err != nil {
			return nil, err
		}
		if d.UpdatedAt, err = parseDraftTime(updated); err != nil {
			return nil, fmt.Errorf("a settled draft's time: %w", err)
		}
		for _, sub := range draft.Subjects(d) {
			if d.UpdatedAt.After(out[sub]) {
				out[sub] = d.UpdatedAt
			}
		}
	}
	return out, rows.Err()
}

// updateArgs are updateDraft's arguments for writing d over the stored row at d's rev in state from.
func updateArgs(d draft.Draft, from draft.State) []any {
	args := draftArgs(d)
	args = args[:len(args)-2]
	kind, text, payload := string(d.By.Kind), d.Text, string(d.Payload)
	args = append(args, text, payload, d.Basis, kind, kind, string(draft.ActorPerson),
		text, payload, d.Basis, kind, d.By.Session)
	return append(args, int64(d.ID), string(from), d.Rev)
}

func (s draftStore) Update(ctx context.Context, d draft.Draft, from draft.State) (draft.Draft, error) {
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		res, err := tx.ExecContext(ctx, updateDraft, updateArgs(d, from)...)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 1 {
			if err != nil {
				return err
			}
			next := d
			next.Rev++
			if next.By.Kind == "" {
				next.By = draft.Actor{Kind: draft.ActorPerson}
			}
			if _, err := tx.ExecContext(ctx, insertChangedRevision, revisionArgs(next, d.UpdatedAt)...); err != nil {
				return err
			}
			var by string
			if err := tx.QueryRowContext(ctx, `SELECT by_kind, by_session FROM drafts WHERE id = ?`, int64(d.ID)).Scan(&by, &d.By.Session); err != nil {
				return err
			}
			d.By.Kind = draft.ActorKind(by)
			tx.Touch(port.TopicDrafts, d.Unit)
			if pendingDraft(from) == pendingDraft(d.State) {
				return nil
			}
			return touchDraftTask(ctx, tx, d.Task)
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

const (
	insertRevision = `INSERT INTO draft_revisions (draft_id, rev, text, payload, basis, by_kind, by_session, at)
	VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8)`
	// insertChangedRevision writes a revision only when its content is not the newest stored one's.
	insertChangedRevision = `INSERT INTO draft_revisions (draft_id, rev, text, payload, basis, by_kind, by_session, at)
	SELECT ?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8 WHERE NOT EXISTS (SELECT 1 FROM draft_revisions r
		WHERE r.draft_id = ?1 AND r.text = ?3 AND r.payload = ?4 AND r.basis = ?5
		AND r.rev = (SELECT max(rev) FROM draft_revisions WHERE draft_id = ?1))`
	selectRevisions = `SELECT rev, text, payload, basis, by_kind, by_session, at FROM draft_revisions WHERE draft_id = ? ORDER BY rev`
)

func revisionArgs(d draft.Draft, at time.Time) []any {
	return []any{int64(d.ID), d.Rev, d.Text, string(d.Payload), d.Basis, string(d.By.Kind), d.By.Session, draftTime(at)}
}

// proposer is who proposed a draft of o, for an Insert whose draft names no author.
func proposer(o draft.Origin) draft.Actor {
	switch {
	case o.Session != "" || o.Kind == draft.OriginTurn || o.Kind == draft.OriginSession:
		return draft.Actor{Kind: draft.ActorSession, Session: o.Session}
	case o.Kind == draft.OriginAuto:
		return draft.Actor{Kind: draft.ActorRule}
	}
	return draft.Actor{Kind: draft.ActorPerson}
}

func (s draftStore) Revisions(ctx context.Context, id draft.ID) ([]draft.Revision, error) {
	var out []draft.Revision
	err := s.db.Read(ctx, func(tx *sql.Tx) error {
		var one int
		switch err := tx.QueryRowContext(ctx, `SELECT 1 FROM drafts WHERE id = ?`, int64(id)).Scan(&one); {
		case errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("draft %d: %w", id, outbox.ErrNotFound)
		case err != nil:
			return err
		}
		rows, err := tx.QueryContext(ctx, selectRevisions, int64(id))
		if err != nil {
			return err
		}
		defer rows.Close()
		out = []draft.Revision{}
		for rows.Next() {
			var r draft.Revision
			var payload, by, at string
			if err := rows.Scan(&r.Rev, &r.Text, &payload, &r.Basis, &by, &r.By.Session, &at); err != nil {
				return err
			}
			if payload != "" {
				r.Payload = json.RawMessage(payload)
			}
			r.By.Kind = draft.ActorKind(by)
			if r.At, err = parseDraftTime(at); err != nil {
				return fmt.Errorf("the time of rev %d of draft %d: %w", r.Rev, id, err)
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}
