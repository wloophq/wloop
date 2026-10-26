package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/event"
	"github.com/wloophq/wloop/internal/domain/mark"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

type ledger struct {
	db  *DB
	now func() time.Time
}

var _ automation.Ledger = ledger{}

// Ledger is the workflows' plan and runs. A write stores its rows and its
// events in one transaction, noting TopicLoopEvents for the events and
// TopicGate only when the pending set changes.
func (db *DB) Ledger() automation.Ledger { return ledger{db, time.Now} }

const itemColumns = `id, workflow, provider, unit_key, key_value, decision, reason, steps, why,
	default_outcome, deadline, guidance, attempts, error, mode, at, gain_memory, gain_disk`

// insertItemSQL takes itemArgs and the write time, which retention ages
// the row by.
const insertItemSQL = `INSERT OR REPLACE INTO plan_items (` + itemColumns + `, saved_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

func (l ledger) Save(ctx context.Context, p automation.PlanWrite, evs ...port.Event) error {
	rows := make([][]any, 0, len(p.Items))
	for _, it := range p.Items {
		args, err := itemArgs(it)
		if err != nil {
			return err
		}
		rows = append(rows, args)
	}
	saved := eventTime(l.now())
	return l.write(ctx, evs, func(tx *Tx) error {
		for _, wf := range p.Workflows {
			if _, err := tx.ExecContext(ctx, `DELETE FROM plan_items WHERE workflow = ?`, wf); err != nil {
				return err
			}
		}
		for i, args := range rows {
			k := p.Items[i].Key
			if _, err := tx.ExecContext(ctx, saveItemSQL, append(append(args, saved), k.Workflow, string(k.Unit.Provider), k.Unit.Key, k.Value)...); err != nil {
				return err
			}
		}
		return nil
	})
}

// saveItemSQL is insertItemSQL unless the item's key is done or dismissed;
// it takes the key's workflow, provider, unit key and value last.
const saveItemSQL = `INSERT OR REPLACE INTO plan_items (` + itemColumns + `, saved_at) SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
	WHERE NOT EXISTS (SELECT 1 FROM workflow_runs WHERE workflow = ? AND provider = ? AND unit_key = ? AND key_value = ? AND status IN ('done', 'dismissed'))`

func (l ledger) Record(ctx context.Context, r workflow.Run, it workflow.Item, evs ...port.Event) error {
	var args []any
	terminal := r.Status == workflow.RunDone || r.Status == workflow.RunDismissed
	if !terminal {
		var err error
		if args, err = itemArgs(it); err != nil {
			return err
		}
	}
	return l.write(ctx, evs, func(tx *Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO workflow_runs (workflow, provider, unit_key, key_value, status, attempts, error, at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (workflow, provider, unit_key, key_value) DO UPDATE SET
			status = excluded.status, attempts = excluded.attempts, error = excluded.error, at = excluded.at`,
			r.Key.Workflow, string(r.Key.Unit.Provider), r.Key.Unit.Key, r.Key.Value, string(r.Status), r.Attempts, r.Error, eventTime(r.At)); err != nil {
			return err
		}
		if terminal {
			for name, v := range it.Marks {
				if err := recordMark(ctx, tx, it.Unit, name, v); err != nil {
					return err
				}
			}
			_, err := tx.ExecContext(ctx, `DELETE FROM plan_items WHERE id = ?`, it.ID)
			return err
		}
		_, err := tx.ExecContext(ctx, insertItemSQL, append(args, eventTime(l.now()))...)
		return err
	})
}

// write runs fn and writes evs in one transaction, touching TopicGate with
// the units of the pending items fn added, dropped or re-timed.
func (l ledger) write(ctx context.Context, evs []port.Event, fn func(tx *Tx) error) error {
	for _, ev := range evs {
		if ev.At.IsZero() {
			return fmt.Errorf("the %s event has no time", ev.Kind)
		}
	}
	_, err := l.db.Write(ctx, func(tx *Tx) error {
		before, err := pendingSet(ctx, tx)
		if err != nil {
			return err
		}
		if err := fn(tx); err != nil {
			return err
		}
		after, err := pendingSet(ctx, tx)
		if err != nil {
			return err
		}
		var moved []unit.Unit
		for id, b := range before {
			if a, ok := after[id]; !ok || a != b {
				moved = append(moved, b.unit)
			}
		}
		for id, a := range after {
			if _, ok := before[id]; !ok {
				moved = append(moved, a.unit)
			}
		}
		if len(moved) > 0 {
			tx.Touch(port.TopicGate, moved...)
		}
		return writeEvents(ctx, tx, evs)
	})
	return err
}

type pendingRow struct {
	unit              unit.Unit
	deadline, outcome string
}

func pendingSet(ctx context.Context, tx *Tx) (map[string]pendingRow, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, provider, unit_key, deadline, default_outcome FROM plan_items WHERE decision = 'pending'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]pendingRow{}
	for rows.Next() {
		var id string
		var r pendingRow
		if err := rows.Scan(&id, &r.unit.Provider, &r.unit.Key, &r.deadline, &r.outcome); err != nil {
			return nil, err
		}
		out[id] = r
	}
	return out, rows.Err()
}

func (l ledger) Items(ctx context.Context, f automation.Filter) ([]workflow.Item, error) {
	var where []string
	var args []any
	add := func(cond string, vals ...any) {
		where = append(where, cond)
		args = append(args, vals...)
	}
	if f.ID != "" {
		add(`id = ?`, f.ID)
	}
	if f.Workflow != "" {
		add(`workflow = ?`, f.Workflow)
	}
	if f.Provider != "" {
		add(`provider = ?`, string(f.Provider))
	}
	if f.Unit != nil {
		add(`provider = ? AND unit_key = ?`, string(f.Unit.Provider), f.Unit.Key)
	}
	if len(f.Decisions) > 0 {
		list, err := json.Marshal(f.Decisions)
		if err != nil {
			return nil, err
		}
		add(`decision IN (SELECT value FROM json_each(?))`, string(list))
	}
	q := `SELECT ` + itemColumns + ` FROM plan_items`
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, ` AND `)
	}
	rows, err := l.db.R().QueryContext(ctx, q+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []workflow.Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (l ledger) Run(ctx context.Context, k workflow.Key) (workflow.Run, bool, error) {
	r := workflow.Run{Key: k}
	var status, at string
	err := l.db.R().QueryRowContext(ctx, `SELECT status, attempts, error, at FROM workflow_runs
		WHERE workflow = ? AND provider = ? AND unit_key = ? AND key_value = ?`,
		k.Workflow, string(k.Unit.Provider), k.Unit.Key, k.Value).Scan(&status, &r.Attempts, &r.Error, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return workflow.Run{}, false, nil
	}
	if err != nil {
		return workflow.Run{}, false, err
	}
	r.Status = workflow.RunStatus(status)
	if r.At, err = parseStoredTime(at); err != nil {
		return workflow.Run{}, false, fmt.Errorf("the run of %s: %w", k.ID(), err)
	}
	return r, true, nil
}

func (l ledger) Count(ctx context.Context, wf string, since time.Time, u *unit.Unit) (int, error) {
	q := `SELECT count(*) FROM workflow_runs WHERE workflow = ? AND status = 'done' AND at >= ?`
	args := []any{wf, eventTime(since)}
	if u != nil {
		q += ` AND provider = ? AND unit_key = ?`
		args = append(args, string(u.Provider), u.Key)
	}
	var n int
	err := l.db.R().QueryRowContext(ctx, q, args...).Scan(&n)
	return n, err
}

type storedStep struct {
	Use   string            `json:"use"`
	Reach string            `json:"reach,omitempty"`
	Args  map[string]string `json:"args,omitempty"`
}

type storedEvent struct {
	ID       int64  `json:"id"`
	Provider string `json:"provider,omitempty"`
	UnitKey  string `json:"unit_key,omitempty"`
	Kind     string `json:"kind"`
	Key      string `json:"key,omitempty"`
	Data     string `json:"data,omitempty"`
	At       string `json:"at,omitempty"`
}

type storedWhy struct {
	Workflow string        `json:"workflow,omitempty"`
	Trigger  string        `json:"trigger,omitempty"`
	Event    *storedEvent  `json:"event,omitempty"`
	FiredAt  string        `json:"fired_at,omitempty"`
	Matched  []storedValue `json:"matched,omitempty"`
	// Marks are the item's, kept with its why since both come from the
	// facts it was planned with.
	Marks map[string]mark.Value `json:"marks,omitempty"`
}

// storedValue keeps a fact's Go type, which JSON alone loses.
type storedValue struct {
	Name  string          `json:"name"`
	Type  string          `json:"type"`
	Value json.RawMessage `json:"value,omitempty"`
}

func itemArgs(it workflow.Item) ([]any, error) {
	steps := make([]storedStep, 0, len(it.Steps))
	for _, st := range it.Steps {
		args, err := stringArgs(st.Args)
		if err != nil {
			return nil, fmt.Errorf("step %s of %s: %w", st.Use, it.ID, err)
		}
		steps = append(steps, storedStep{st.Use, string(st.Reach), args})
	}
	stepsJSON, err := json.Marshal(steps)
	if err != nil {
		return nil, fmt.Errorf("the steps of %s: %w", it.ID, err)
	}
	why := storedWhy{Workflow: it.Why.Workflow, Trigger: string(it.Why.Trigger), FiredAt: eventTime(it.Why.FiredAt), Marks: it.Marks}
	if ev := it.Why.Event; ev != nil {
		why.Event = &storedEvent{ev.ID, string(ev.Unit.Provider), ev.Unit.Key, ev.Kind, ev.Key, ev.Data, eventTime(ev.At)}
	}
	for _, f := range it.Why.Matched {
		v, err := encodeFact(f)
		if err != nil {
			return nil, fmt.Errorf("the why of %s: %w", it.ID, err)
		}
		why.Matched = append(why.Matched, v)
	}
	whyJSON, err := json.Marshal(why)
	if err != nil {
		return nil, fmt.Errorf("the why of %s: %w", it.ID, err)
	}
	return []any{it.ID, it.Workflow, string(it.Unit.Provider), it.Unit.Key, it.Key.Value, string(it.Decision), it.Reason,
		string(stepsJSON), string(whyJSON), string(it.Default), eventTime(it.Deadline), it.Guidance, it.Attempts, it.Error,
		string(workflow.ModeOf(it.Observe)), eventTime(it.At), it.Gain.MemoryBytes, it.Gain.DiskBytes}, nil
}

// stringArgs stores step arguments as the manifest gives them, strings; a
// scalar of another type is formatted, a table or list refused.
func stringArgs(in map[string]any) (map[string]string, error) {
	if in == nil {
		return nil, nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		switch x := v.(type) {
		case string:
			out[k] = x
		case bool, int, int64, float64:
			out[k] = fmt.Sprint(x)
		default:
			return nil, fmt.Errorf("argument %s has a value of type %T", k, v)
		}
	}
	return out, nil
}

func encodeFact(f workflow.Fact) (storedValue, error) {
	var typ string
	v := f.Value
	switch x := f.Value.(type) {
	case nil:
		return storedValue{Name: f.Name, Type: "nil"}, nil
	case bool:
		typ = "bool"
	case int:
		typ = "int"
	case int64:
		typ = "int64"
	case string:
		typ = "string"
	case []string:
		typ = "strings"
	case time.Duration:
		typ, v = "duration", int64(x)
	case time.Time:
		typ, v = "time", x.UTC().Format(time.RFC3339Nano)
	default:
		return storedValue{}, fmt.Errorf("fact %s has a value of type %T", f.Name, f.Value)
	}
	raw, err := json.Marshal(v)
	return storedValue{Name: f.Name, Type: typ, Value: raw}, err
}

func decodeFact(s storedValue) (workflow.Fact, error) {
	f := workflow.Fact{Name: s.Name}
	var err error
	switch s.Type {
	case "nil":
	case "bool":
		var b bool
		err = json.Unmarshal(s.Value, &b)
		f.Value = b
	case "int":
		var n int
		err = json.Unmarshal(s.Value, &n)
		f.Value = n
	case "int64":
		var n int64
		err = json.Unmarshal(s.Value, &n)
		f.Value = n
	case "string":
		var str string
		err = json.Unmarshal(s.Value, &str)
		f.Value = str
	case "strings":
		var l []string
		err = json.Unmarshal(s.Value, &l)
		f.Value = l
	case "duration":
		var n int64
		err = json.Unmarshal(s.Value, &n)
		f.Value = time.Duration(n)
	case "time":
		var str string
		if err = json.Unmarshal(s.Value, &str); err == nil {
			f.Value, err = time.Parse(time.RFC3339Nano, str)
		}
	default:
		err = fmt.Errorf("unknown type %q", s.Type)
	}
	if err != nil {
		return workflow.Fact{}, fmt.Errorf("fact %s: %w", s.Name, err)
	}
	return f, nil
}

func scanItem(row scanner) (workflow.Item, error) {
	var it workflow.Item
	var decision, steps, why, def, deadline, mode, at string
	if err := row.Scan(&it.ID, &it.Workflow, &it.Unit.Provider, &it.Unit.Key, &it.Key.Value, &decision, &it.Reason, &steps, &why,
		&def, &deadline, &it.Guidance, &it.Attempts, &it.Error, &mode, &at, &it.Gain.MemoryBytes, &it.Gain.DiskBytes); err != nil {
		return workflow.Item{}, err
	}
	it.Key.Workflow, it.Key.Unit = it.Workflow, it.Unit
	it.Decision, it.Default, it.Observe = workflow.Decision(decision), workflow.Outcome(def), mode == string(workflow.ModeObserve)
	fail := func(what string, err error) (workflow.Item, error) {
		return workflow.Item{}, fmt.Errorf("the %s of plan item %s: %w", what, it.ID, err)
	}
	var err error
	if it.Deadline, err = parseStoredTime(deadline); err != nil {
		return fail("deadline", err)
	}
	if it.At, err = parseStoredTime(at); err != nil {
		return fail("time", err)
	}
	var ss []storedStep
	if err := json.Unmarshal([]byte(steps), &ss); err != nil {
		return fail("steps", err)
	}
	for _, s := range ss {
		var args map[string]any
		if s.Args != nil {
			args = make(map[string]any, len(s.Args))
			for k, v := range s.Args {
				args[k] = v
			}
		}
		it.Steps = append(it.Steps, workflow.Step{Use: s.Use, Reach: workflow.Reach(s.Reach), Args: args})
	}
	var w storedWhy
	if err := json.Unmarshal([]byte(why), &w); err != nil {
		return fail("why", err)
	}
	it.Why = workflow.Why{Workflow: w.Workflow, Trigger: workflow.TriggerKind(w.Trigger)}
	it.Marks = w.Marks
	if it.Why.FiredAt, err = parseStoredTime(w.FiredAt); err != nil {
		return fail("firing time", err)
	}
	if e := w.Event; e != nil {
		ev := workflow.Event{ID: e.ID, Unit: unit.Unit{Provider: unit.ProviderID(e.Provider), Key: e.UnitKey}, Kind: e.Kind, Key: e.Key, Data: e.Data}
		if ev.At, err = parseStoredTime(e.At); err != nil {
			return fail("event", err)
		}
		it.Why.Event = &ev
	}
	for _, s := range w.Matched {
		f, err := decodeFact(s)
		if err != nil {
			return fail("why", err)
		}
		it.Why.Matched = append(it.Why.Matched, f)
	}
	return it, nil
}

// parseStoredTime reads a time eventTime wrote; empty is the zero time.
func parseStoredTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(event.TimeLayout, s)
}
