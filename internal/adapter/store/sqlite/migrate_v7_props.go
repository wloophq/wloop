package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// v7Props gives every task its category from its version 6 state, moves the user fields into task_props with
// who wrote them, sets the kind property from the task's origin, and stores projects as parents: tasks.project
// becomes tasks.parent and a project's review moves onto its task, so projects keeps only the slug.
func v7Props(ctx context.Context, tx *sql.Tx) error {
	for _, q := range []string{
		`ALTER TABLE tasks ADD COLUMN category TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE task_props (
  task_id TEXT NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
  name TEXT NOT NULL, value TEXT NOT NULL, writer TEXT NOT NULL, at TEXT NOT NULL,
  PRIMARY KEY (task_id, name)
) WITHOUT ROWID`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("%s: %w", q, err)
		}
	}
	for _, s := range v6States {
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET category = ? WHERE state = ?`, s.category, s.state); err != nil {
			return err
		}
	}
	if err := v7UserProps(ctx, tx); err != nil {
		return err
	}
	if err := v7KindProps(ctx, tx); err != nil {
		return err
	}
	for _, q := range []string{
		`ALTER TABLE tasks RENAME COLUMN project TO parent`,
		`ALTER TABLE tasks ADD COLUMN review_every INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE tasks ADD COLUMN reviewed_at TEXT NOT NULL DEFAULT ''`,
		`UPDATE tasks SET review_every = p.review_every, reviewed_at = p.reviewed_at FROM projects p WHERE p.task_id = tasks.id`,
		`ALTER TABLE projects DROP COLUMN review_every`,
		`ALTER TABLE projects DROP COLUMN reviewed_at`,
		`DROP TABLE task_contexts`,
		`ALTER TABLE tasks DROP COLUMN focus_on`,
		`ALTER TABLE tasks DROP COLUMN focus_auto`,
		`ALTER TABLE tasks DROP COLUMN focus_at`,
		`ALTER TABLE tasks DROP COLUMN area`,
		`ALTER TABLE tasks DROP COLUMN deadline`,
		`CREATE INDEX tasks_category ON tasks (category)`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("%s: %w", q, err)
		}
	}
	return nil
}

type v7Prop struct {
	id, name string
	value    any
	writer   string
	at       string
}

func putV7Props(ctx context.Context, tx *sql.Tx, ps []v7Prop) error {
	for _, p := range ps {
		body, err := json.Marshal(p.value)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_props (task_id, name, value, writer, at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (task_id, name) DO NOTHING`, p.id, p.name, string(body), p.writer, p.at); err != nil {
			return fmt.Errorf("property %s of %s: %w", p.name, p.id, err)
		}
	}
	return nil
}

// v7UserProps moves area, focus, deadline and the contexts into properties. A derived task's deadline and
// contexts came from its items, so the plugin wrote them; anything else the person did, but a star the loop
// set on its own, which a workflow wrote.
func v7UserProps(ctx context.Context, tx *sql.Tx) error {
	var ps []v7Prop
	at := func(s string, t time.Time) string {
		if t.IsZero() {
			return s
		}
		return formatTime(t)
	}
	err := eachRow(ctx, tx, `SELECT id, origin, area, focus_on, focus_auto, focus_at, deadline, updated FROM tasks ORDER BY id`, nil,
		func(r *sql.Rows) error {
			var id, origin, area, focusAuto, focusAt, deadline, updated string
			var focusOn bool
			if err := r.Scan(&id, &origin, &area, &focusOn, &focusAuto, &focusAt, &deadline, &updated); err != nil {
				return err
			}
			derived := "person"
			if origin == "derived" {
				derived = "plugin"
			}
			if area != "" {
				ps = append(ps, v7Prop{id, "area", map[string]string{"text": area}, "person", updated})
			}
			switch {
			case focusOn && focusAuto != "":
				ps = append(ps, v7Prop{id, "focus", map[string]string{"text": "on"}, "workflow", at(updated, v6Time(focusAt))})
			case focusOn:
				ps = append(ps, v7Prop{id, "focus", map[string]string{"text": "on"}, "person", at(updated, v6Time(focusAt))})
			case focusAt != "":
				ps = append(ps, v7Prop{id, "focus", map[string]string{"text": "off"}, "person", at(updated, v6Time(focusAt))})
			}
			if due := v6Time(deadline); !due.IsZero() {
				ps = append(ps, v7Prop{id, "due", map[string]time.Time{"time": due}, derived, updated})
			}
			return nil
		})
	if err != nil {
		return err
	}
	contexts := map[string][]string{}
	var ids []string
	err = eachRow(ctx, tx, `SELECT task_id, context FROM task_contexts ORDER BY task_id, ord`, nil, func(r *sql.Rows) error {
		var id, c string
		if err := r.Scan(&id, &c); err != nil {
			return err
		}
		if _, ok := contexts[id]; !ok {
			ids = append(ids, id)
		}
		contexts[id] = append(contexts[id], c)
		return nil
	})
	if err != nil {
		return err
	}
	for _, id := range ids {
		var origin, updated string
		if err := tx.QueryRowContext(ctx, `SELECT origin, updated FROM tasks WHERE id = ?`, id).Scan(&origin, &updated); err != nil {
			return fmt.Errorf("the contexts of %s: %w", id, err)
		}
		writer := "person"
		if origin == "derived" {
			writer = "plugin"
		}
		ps = append(ps, v7Prop{id, "contexts", map[string][]string{"list": contexts[id]}, writer, updated})
	}
	return putV7Props(ctx, tx, ps)
}

// v7KindProps sets the kind property: an action's from the provider of its unit, the kinds of task version 6
// derived actions on, a capture's start when a start
// turn ran on it and capture otherwise, a project's project.
func v7KindProps(ctx context.Context, tx *sql.Tx) error {
	var ps []v7Prop
	err := eachRow(ctx, tx, `SELECT t.id, t.kind, t.updated,
		CASE COALESCE((SELECT provider FROM task_units WHERE task_id = t.id ORDER BY ord LIMIT 1),
			(SELECT provider FROM task_items WHERE task_id = t.id ORDER BY provider, unit_key, item LIMIT 1), '')
			WHEN 'github' THEN 'pull-request' WHEN 'slack' THEN 'thread' ELSE '' END,
		EXISTS (SELECT 1 FROM turns WHERE provider = 'task' AND unit_key = t.id AND intent = 'start')
		FROM tasks t WHERE t.kind IN ('action', 'capture', 'project') ORDER BY t.id`, nil, func(r *sql.Rows) error {
		var id, kind, updated, unitKind string
		var started bool
		if err := r.Scan(&id, &kind, &updated, &unitKind, &started); err != nil {
			return err
		}
		value, writer := "", "workflow"
		switch kind {
		case "action":
			value, writer = unitKind, "plugin"
		case "capture":
			value = "capture"
			if started {
				value = "start"
			}
		case "project":
			value = "project"
		}
		if value != "" {
			ps = append(ps, v7Prop{id, "kind", map[string]string{"text": value}, writer, updated})
		}
		return nil
	})
	if err != nil {
		return err
	}
	return putV7Props(ctx, tx, ps)
}
