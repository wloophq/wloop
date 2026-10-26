package sqlite

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/views"
	"github.com/wloophq/wloop/internal/domain/event"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Store is the repository of the turns, the sessions and what the views
// applied. Every save writes its events in its own transaction.
type Store struct{ db *DB }

var (
	_ turns.Store    = (*Store)(nil)
	_ turns.Reports  = (*Store)(nil)
	_ turns.Registry = (*Store)(nil)
	_ views.State    = (*Store)(nil)
)

func New(db *DB) *Store { return &Store{db: db} }

// write runs fn and writes evs in one transaction.
func (s *Store) write(ctx context.Context, evs []port.Event, fn func(tx *Tx) error) error {
	for _, ev := range evs {
		if ev.At.IsZero() {
			return fmt.Errorf("the %s event has no time", ev.Kind)
		}
	}
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		if err := fn(tx); err != nil {
			return err
		}
		return writeEvents(ctx, tx, evs)
	})
	return err
}

// RecordEvents writes evs on their units in one transaction.
func (s *Store) RecordEvents(ctx context.Context, evs ...port.Event) error {
	return s.write(ctx, evs, func(*Tx) error { return nil })
}

func writeEvents(ctx context.Context, tx *Tx, evs []port.Event) error {
	for _, ev := range evs {
		u := storedUnit(ev)
		if ev.Key != "" {
			res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO events (provider, unit_key, kind, key, actor, at, data) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				string(u.Provider), u.Key, ev.Kind, ev.Key, ev.Actor, ev.At.UTC().Format(event.TimeLayout), ev.Data)
			if err != nil {
				return err
			}
			if n, err := res.RowsAffected(); err != nil {
				return err
			} else if n > 0 {
				tx.Touch(port.TopicLoopEvents, ev.Unit)
			}
			continue
		}
		if err := writeUnkeyed(ctx, tx, ev, u); err != nil {
			return err
		}
	}
	return nil
}

// handleKeyedKinds are the kinds of the events whose key starts with a
// terminal handle; with no unit, such an event is stored under the session
// unit of that handle.
var handleKeyedKinds = []string{
	turns.KindSessionStarted, turns.KindSessionLaunched, turns.KindSessionAbandoned, turns.KindSessionIdle, turns.KindSessionActive,
	turns.KindSessionClosed, turns.KindTurnStopped, turns.KindStopBlocked, turns.KindMCPSeen, turns.KindSessionUnit,
}

// prOf is the pull request of a github unit, and the zero ref for a unit of
// another provider.
func prOf(u unit.Unit) pr.Ref {
	if u.Provider != unit.GitHub {
		return pr.Ref{}
	}
	ref, _ := pr.ParseKey(u.Key)
	return ref
}

// StoredUnit is the unit an event of u, kind and key is stored under: u,
// or the session unit of the key's handle for a handle-keyed kind with no
// unit. Every writer of events applies it.
func StoredUnit(u unit.Unit, kind, key string) unit.Unit {
	if !u.IsZero() || key == "" || !slices.Contains(handleKeyedKinds, kind) {
		return u
	}
	handle, _, _ := strings.Cut(key, ":")
	return sessionUnit(handle)
}

func storedUnit(ev port.Event) unit.Unit { return StoredUnit(ev.Unit, ev.Kind, ev.Key) }

// compareUnits orders units by provider, then key, with github units by
// owner, repo and number.
func compareUnits(a, b unit.Unit) int {
	if a.Provider == unit.GitHub && b.Provider == unit.GitHub {
		ra, errA := pr.ParseKey(a.Key)
		rb, errB := pr.ParseKey(b.Key)
		if errA == nil && errB == nil {
			return cmp.Or(cmp.Compare(ra.Owner, rb.Owner), cmp.Compare(ra.Repo, rb.Repo), cmp.Compare(ra.Number, rb.Number))
		}
	}
	return cmp.Or(cmp.Compare(a.Provider, b.Provider), cmp.Compare(a.Key, b.Key))
}

func writeUnkeyed(ctx context.Context, tx *Tx, ev port.Event, u unit.Unit) error {
	for at := ev.At.UTC(); ; at = at.Add(time.Nanosecond) {
		res, err := tx.ExecContext(ctx, `INSERT INTO events (provider, unit_key, kind, key, actor, at, data) VALUES (?, ?, ?, '', ?, ?, ?) ON CONFLICT DO NOTHING`,
			string(u.Provider), u.Key, ev.Kind, ev.Actor, at.Format(event.TimeLayout), ev.Data)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 1 {
			tx.Touch(port.TopicLoopEvents, ev.Unit)
			return err
		}
	}
}

func (s *Store) Books(ctx context.Context, units ...unit.Unit) (map[unit.Unit]turn.Book, error) {
	var out map[unit.Unit]turn.Book
	err := s.db.Read(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = loadBooks(ctx, tx, units)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

const booksChunk = 500

// loadBooks returns the book of each unit, empty for a unit with no turn;
// with no unit, the book of every unit with a turn. It issues three queries
// per chunk of booksChunk units, or three in all with no unit.
func loadBooks(ctx context.Context, tx *sql.Tx, units []unit.Unit) (map[unit.Unit]turn.Book, error) {
	if len(units) == 0 {
		return loadChunk(ctx, tx, "", nil)
	}
	seen := make(map[unit.Unit]bool, len(units))
	var providers []unit.ProviderID
	keys := map[unit.ProviderID][]string{}
	for _, u := range units {
		if !seen[u] {
			seen[u] = true
			if keys[u.Provider] == nil {
				providers = append(providers, u.Provider)
			}
			keys[u.Provider] = append(keys[u.Provider], u.Key)
		}
	}
	out := make(map[unit.Unit]turn.Book, len(seen))
	for _, p := range providers {
		for chunk := range slices.Chunk(keys[p], booksChunk) {
			books, err := loadChunk(ctx, tx, p, chunk)
			if err != nil {
				return nil, err
			}
			for u, b := range books {
				out[u] = b
			}
		}
	}
	return out, nil
}

const (
	bookTurnsSQL = `SELECT provider, unit_key, seq, intent, turn_id, trigger, guidance, head, sent_to, session, state, sent_at, started_at, ended_at, resume_of, replaced_by, prior_state,
		agent, brief_run, agent_session, inputs, mode, workflow, role FROM turns%s ORDER BY provider, unit_key, seq`
	bookRecordsSQL = `SELECT provider, unit_key, id, turn_seq, session, agent_session, head, hash, at, acked, record
		FROM turn_records%s ORDER BY provider, unit_key, id`
	bookAnswersSQL = `SELECT provider, unit_key, id, turn_seq, at, text, source, option_id, yes, no, route, session
		FROM turn_answers%s ORDER BY provider, unit_key, id`
)

func bookWhere(n int) string {
	if n == 0 {
		return ""
	}
	return " WHERE provider = ? AND unit_key IN (" + strings.TrimSuffix(strings.Repeat("?,", n), ",") + ")"
}

func loadChunk(ctx context.Context, tx *sql.Tx, provider unit.ProviderID, keys []string) (map[unit.Unit]turn.Book, error) {
	var args []any
	var units []unit.Unit
	if len(keys) > 0 {
		args = append(args, string(provider))
		for _, k := range keys {
			args = append(args, k)
			units = append(units, unit.Unit{Provider: provider, Key: k})
		}
	}
	where := bookWhere(len(keys))
	books := map[unit.Unit]*turn.Book{}
	bookOf := func(u unit.Unit) *turn.Book {
		b := books[u]
		if b == nil {
			b = &turn.Book{Unit: u}
			books[u] = b
		}
		return b
	}
	err := eachArgs(ctx, tx, fmt.Sprintf(bookTurnsSQL, where), args, func(rows *sql.Rows) error {
		var r unit.Unit
		var t turn.Turn
		var sent, started, ended, inputs, mode string
		if err := rows.Scan(&r.Provider, &r.Key, &t.Seq, &t.Intent, &t.ID, &t.Trigger, &t.Guidance, &t.Head, &t.SentTo, &t.Session, &t.State, &sent, &started, &ended, &t.ResumeOf, &t.ReplacedBy, &t.Before,
			&t.Agent, &t.BriefRun, &t.AgentSession, &inputs, &mode, &t.Workflow, &t.Role); err != nil {
			return err
		}
		t.Runner = turn.Runner(mode)
		var err error
		if t.Inputs, err = decodeInputs(inputs); err != nil {
			return fmt.Errorf("inputs of turn %d of %s: %w", t.Seq, r, err)
		}
		t.SentAt, t.StartedAt, t.EndedAt = parseTime(sent), parseTime(started), parseTime(ended)
		b := bookOf(r)
		b.Turns = append(b.Turns, t)
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = eachArgs(ctx, tx, fmt.Sprintf(bookRecordsSQL, where), args, func(rows *sql.Rows) error {
		var ref unit.Unit
		var r turn.Record
		var at, raw string
		if err := rows.Scan(&ref.Provider, &ref.Key, &r.ID, &r.TurnSeq, &r.Session, &r.AgentSession, &r.Head, &r.Hash, &at, &r.Acked, &raw); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(raw), &r.Record); err != nil {
			return fmt.Errorf("record %d of %s: %w", r.ID, ref, err)
		}
		r.At = parseTime(at)
		b := bookOf(ref)
		b.Records = append(b.Records, r)
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = eachArgs(ctx, tx, fmt.Sprintf(bookAnswersSQL, where), args, func(rows *sql.Rows) error {
		var ref unit.Unit
		var a turn.Answer
		var at string
		if err := rows.Scan(&ref.Provider, &ref.Key, &a.ID, &a.TurnSeq, &at, &a.Text, &a.Source, &a.OptionID, &a.Yes, &a.No, &a.Route, &a.Session); err != nil {
			return err
		}
		a.At = parseTime(at)
		b := bookOf(ref)
		b.Answers = append(b.Answers, a)
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make(map[unit.Unit]turn.Book, max(len(units), len(books)))
	for _, u := range units {
		out[u] = turn.Book{Unit: u}
	}
	for u, b := range books {
		if len(units) == 0 && len(b.Turns) == 0 {
			continue
		}
		out[u] = *b
	}
	return out, nil
}

func (s *Store) BookOf(ctx context.Context, k turns.Key) (turn.Book, bool, error) {
	if k == (turns.Key{}) {
		return turn.Book{}, false, nil
	}
	var b turn.Book
	var found bool
	err := s.db.Read(ctx, func(tx *sql.Tx) error {
		var u unit.Unit
		err := tx.QueryRowContext(ctx, bookOfQuery(k), bookOfArgs(k)...).Scan(&u.Provider, &u.Key)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		b, err = loadBook(ctx, tx, u)
		found = err == nil
		return err
	})
	return b, found, err
}

const (
	bookOfTurn   = `SELECT provider, unit_key FROM turns WHERE turn_id = ? ORDER BY seq DESC LIMIT 1`
	bookOfBoth   = `SELECT provider, unit_key FROM turns WHERE turn_id = ? AND (session = ? OR sent_to = ?) ORDER BY seq DESC LIMIT 1`
	bookOfHandle = `SELECT provider, unit_key FROM turns WHERE seq = max(
		coalesce((SELECT max(seq) FROM turns WHERE session = ?), 0),
		coalesce((SELECT max(seq) FROM turns WHERE sent_to = ?), 0))`
)

func bookOfQuery(k turns.Key) string {
	switch {
	case k.Turn == "":
		return bookOfHandle
	case k.Session == "":
		return bookOfTurn
	}
	return bookOfBoth
}

func bookOfArgs(k turns.Key) []any {
	switch {
	case k.Turn == "":
		return []any{k.Session, k.Session}
	case k.Session == "":
		return []any{k.Turn}
	}
	return []any{k.Turn, k.Session, k.Session}
}

func unitsOfSQL(n int) string {
	return `WITH h(handle) AS (VALUES ` + strings.TrimSuffix(strings.Repeat("(?),", n), ",") + `)
		SELECT h.handle, t.provider, t.unit_key FROM h
		JOIN turns t ON t.seq = max(
			coalesce((SELECT max(seq) FROM turns WHERE session = h.handle), 0),
			coalesce((SELECT max(seq) FROM turns WHERE sent_to = h.handle), 0))`
}

func (s *Store) UnitsOf(ctx context.Context, handles []string) (map[string]unit.Unit, error) {
	args := make([]any, 0, len(handles))
	seen := make(map[string]bool, len(handles))
	for _, h := range handles {
		if h != "" && !seen[h] {
			seen[h] = true
			args = append(args, h)
		}
	}
	out := make(map[string]unit.Unit, len(args))
	if len(args) == 0 {
		return out, nil
	}
	q := unitsOfSQL(len(args))
	err := s.db.Read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var h string
			var u unit.Unit
			if err := rows.Scan(&h, &u.Provider, &u.Key); err != nil {
				return err
			}
			out[h] = u
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) Save(ctx context.Context, b turn.Book, evs ...port.Event) (turn.Book, error) {
	var saved turn.Book
	err := s.write(ctx, evs, func(tx *Tx) error {
		stored, err := loadBook(ctx, tx.Tx, b.Unit)
		if err != nil {
			return err
		}
		var changed bool
		if saved, changed, err = saveBook(ctx, tx, b.Unit, stored, b); err != nil {
			return err
		}
		if changed {
			tx.Touch(port.TopicTurns, b.Unit)
		}
		return nil
	})
	if err != nil {
		return turn.Book{}, err
	}
	return saved, nil
}

func turnColumns(t turn.Turn) ([]any, string, error) {
	inputs, err := encodeInputs(t.Inputs)
	if err != nil {
		return nil, "", err
	}
	return []any{string(t.Intent), t.ID, string(t.Trigger), t.Guidance, t.Head, t.SentTo, t.Session, string(t.State),
		formatTime(t.SentAt), formatTime(t.StartedAt), formatTime(t.EndedAt), t.ResumeOf, t.ReplacedBy, string(t.Before), t.Agent, t.BriefRun, t.AgentSession, inputs, string(t.Runner), t.Workflow, t.Role}, inputs, nil
}

// storedTurn is t as a reload would return it.
func storedTurn(t turn.Turn, inputs string) turn.Turn {
	t.SentAt, t.StartedAt, t.EndedAt = parseTime(formatTime(t.SentAt)), parseTime(formatTime(t.StartedAt)), parseTime(formatTime(t.EndedAt))
	t.Inputs, _ = decodeInputs(inputs)
	return t
}

func recordColumns(r turn.Record) ([]any, string, error) {
	raw, err := json.Marshal(r.Record)
	if err != nil {
		return nil, "", err
	}
	return []any{r.TurnSeq, r.Session, r.AgentSession, r.Head, r.Hash, formatTime(r.At), r.Acked, string(raw)}, string(raw), nil
}

func storedRecord(r turn.Record, raw string) turn.Record {
	r.At = parseTime(formatTime(r.At))
	var fresh turn.Record
	_ = json.Unmarshal([]byte(raw), &fresh.Record)
	r.Record = fresh.Record
	return r
}

func answerColumns(a turn.Answer) []any {
	return []any{a.TurnSeq, formatTime(a.At), a.Text, string(a.Source), a.OptionID, a.Yes, a.No, string(a.Route), a.Session}
}

// saveBook writes the turns, records and answers of b that differ from stored
// and returns stored with them applied, as a reload would return it.
func saveBook(ctx context.Context, tx *Tx, ref unit.Unit, stored, b turn.Book) (turn.Book, bool, error) {
	out := turn.Book{Unit: stored.Unit, Turns: slices.Clone(stored.Turns), Records: slices.Clone(stored.Records), Answers: slices.Clone(stored.Answers)}
	var changed bool
	var newest int64
	turnAt := make(map[int64]int, len(out.Turns))
	for i, t := range out.Turns {
		turnAt[t.Seq] = i
	}
	for _, t := range b.Turns {
		cols, inputs, err := turnColumns(t)
		if err != nil {
			return turn.Book{}, false, fmt.Errorf("inputs of turn %s of %s: %w", t.ID, ref, err)
		}
		if t.Seq != 0 {
			i, ok := turnAt[t.Seq]
			if !ok {
				return turn.Book{}, false, fmt.Errorf("%s has no turn %d", ref, t.Seq)
			}
			old, _, _ := turnColumns(out.Turns[i])
			if reflect.DeepEqual(old, cols) {
				continue
			}
			if _, err := tx.ExecContext(ctx, `UPDATE turns SET intent = ?, turn_id = ?, trigger = ?, guidance = ?, head = ?, sent_to = ?, session = ?, state = ?,
				sent_at = ?, started_at = ?, ended_at = ?, resume_of = ?, replaced_by = ?, prior_state = ?, agent = ?, brief_run = ?, agent_session = ?, inputs = ?, mode = ?, workflow = ?, role = ? WHERE seq = ? AND provider = ? AND unit_key = ?`,
				append(cols, t.Seq, string(ref.Provider), ref.Key)...); err != nil {
				return turn.Book{}, false, err
			}
			out.Turns[i] = storedTurn(t, inputs)
			changed = true
			continue
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO turns (provider, unit_key, intent, turn_id, trigger, guidance, head, sent_to, session, state, sent_at, started_at, ended_at, resume_of, replaced_by, prior_state,
			agent, brief_run, agent_session, inputs, mode, workflow, role) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, append([]any{string(ref.Provider), ref.Key}, cols...)...)
		if err != nil {
			return turn.Book{}, false, err
		}
		if newest, err = res.LastInsertId(); err != nil {
			return turn.Book{}, false, err
		}
		t.Seq = newest
		turnAt[newest] = len(out.Turns)
		out.Turns = append(out.Turns, storedTurn(t, inputs))
		changed = true
	}
	if newest != 0 {
		var orphans bool
		for i, t := range out.Turns {
			if t.State == turn.Replaced && t.Before != "" && t.ReplacedBy == 0 {
				out.Turns[i].ReplacedBy = newest
				orphans = true
			}
		}
		if orphans {
			if _, err := tx.ExecContext(ctx, `UPDATE turns SET replaced_by = ? WHERE provider = ? AND unit_key = ? AND state = ? AND prior_state != '' AND replaced_by = 0`,
				newest, string(ref.Provider), ref.Key, string(turn.Replaced)); err != nil {
				return turn.Book{}, false, err
			}
		}
	}
	type onceKey struct {
		seq  int64
		hash string
	}
	recordAt := make(map[int64]int, len(out.Records))
	once := make(map[onceKey]bool, len(out.Records))
	for i, r := range out.Records {
		recordAt[r.ID] = i
		once[onceKey{r.TurnSeq, r.Hash}] = true
	}
	for _, r := range b.Records {
		if r.TurnSeq == 0 {
			r.TurnSeq = newest
		}
		cols, raw, err := recordColumns(r)
		if err != nil {
			return turn.Book{}, false, err
		}
		if r.ID != 0 {
			i, ok := recordAt[r.ID]
			if !ok {
				return turn.Book{}, false, fmt.Errorf("%s has no record %d", ref, r.ID)
			}
			old, _, _ := recordColumns(out.Records[i])
			if reflect.DeepEqual(old, cols) {
				continue
			}
			delete(once, onceKey{out.Records[i].TurnSeq, out.Records[i].Hash})
			once[onceKey{r.TurnSeq, r.Hash}] = true
			if _, err := tx.ExecContext(ctx, `UPDATE turn_records SET turn_seq = ?, session = ?, agent_session = ?, head = ?, hash = ?, at = ?, acked = ?, record = ?
				WHERE id = ? AND provider = ? AND unit_key = ?`, append(cols, r.ID, string(ref.Provider), ref.Key)...); err != nil {
				return turn.Book{}, false, err
			}
			out.Records[i] = storedRecord(r, raw)
			changed = true
			continue
		}
		if once[onceKey{r.TurnSeq, r.Hash}] {
			continue
		}
		res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO turn_records (provider, unit_key, turn_seq, session, agent_session, head, hash, at, acked, record)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, append([]any{string(ref.Provider), ref.Key}, cols...)...)
		if err != nil {
			return turn.Book{}, false, err
		}
		if n, err := res.RowsAffected(); err != nil {
			return turn.Book{}, false, err
		} else if n == 0 {
			continue
		}
		if r.ID, err = res.LastInsertId(); err != nil {
			return turn.Book{}, false, err
		}
		once[onceKey{r.TurnSeq, r.Hash}] = true
		recordAt[r.ID] = len(out.Records)
		out.Records = append(out.Records, storedRecord(r, raw))
		changed = true
	}
	answerAt := make(map[int64]int, len(out.Answers))
	for i, a := range out.Answers {
		answerAt[a.ID] = i
	}
	for _, a := range b.Answers {
		if a.TurnSeq == 0 {
			a.TurnSeq = newest
		}
		cols := answerColumns(a)
		a.At = parseTime(formatTime(a.At))
		if a.ID != 0 {
			i, ok := answerAt[a.ID]
			if !ok {
				return turn.Book{}, false, fmt.Errorf("%s has no answer %d", ref, a.ID)
			}
			if reflect.DeepEqual(answerColumns(out.Answers[i]), cols) {
				continue
			}
			if _, err := tx.ExecContext(ctx, `UPDATE turn_answers SET turn_seq = ?, at = ?, text = ?, source = ?, option_id = ?, yes = ?, no = ?, route = ?, session = ?
				WHERE id = ? AND provider = ? AND unit_key = ?`, append(cols, a.ID, string(ref.Provider), ref.Key)...); err != nil {
				return turn.Book{}, false, err
			}
			out.Answers[i] = a
			changed = true
			continue
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO turn_answers (provider, unit_key, turn_seq, at, text, source, option_id, yes, no, route, session)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, append([]any{string(ref.Provider), ref.Key}, cols...)...)
		if err != nil {
			return turn.Book{}, false, err
		}
		if a.ID, err = res.LastInsertId(); err != nil {
			return turn.Book{}, false, err
		}
		answerAt[a.ID] = len(out.Answers)
		out.Answers = append(out.Answers, a)
		changed = true
	}
	return out, changed, nil
}

// encodeInputs is the JSON object of the inputs with sorted keys.
func encodeInputs(in map[string]any) (string, error) {
	if len(in) == 0 {
		return "{}", nil
	}
	raw, err := json.Marshal(in)
	return string(raw), err
}

func decodeInputs(raw string) (map[string]any, error) {
	var in map[string]any
	if err := json.Unmarshal([]byte(raw), &in); err != nil || len(in) == 0 {
		return nil, err
	}
	return in, nil
}

func loadBook(ctx context.Context, tx *sql.Tx, u unit.Unit) (turn.Book, error) {
	books, err := loadBooks(ctx, tx, []unit.Unit{u})
	return books[u], err
}

const openUnitsSQL = `SELECT DISTINCT provider, unit_key FROM turns WHERE state IN ('sent', 'started', 'asked') ORDER BY provider, unit_key`

func (s *Store) Open(ctx context.Context) ([]unit.Unit, error) {
	return queryUnits(ctx, s.db.R(), openUnitsSQL)
}

func (s *Store) WithTurns(ctx context.Context, i turn.Intent) ([]unit.Unit, error) {
	return queryUnits(ctx, s.db.R(), `SELECT DISTINCT provider, unit_key FROM turns WHERE intent = ? AND state != ? ORDER BY provider, unit_key`, string(i), string(turn.Withdrawn))
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func queryUnits(ctx context.Context, q querier, query string, args ...any) ([]unit.Unit, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []unit.Unit
	for rows.Next() {
		var u unit.Unit
		if err := rows.Scan(&u.Provider, &u.Key); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	slices.SortFunc(out, compareUnits)
	return out, rows.Err()
}

func eachArgs(ctx context.Context, tx *sql.Tx, query string, args []any, fn func(*sql.Rows) error) error {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}
