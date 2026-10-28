package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

// The provider of a task's unit and the prefix of its ids, at versions 6 and 7.
const (
	v6Task     = "entry"
	v6IDPrefix = "e-"
	v7Task     = "task"
	v7IDPrefix = "t-"
)

// v7Rename stores the entries as tasks: the entry tables and columns take the task names, every reference
// rekeyTask reaches takes the t- id and the task provider, and the turns' entry input and the nudged effect
// take the task spelling.
func v7Rename(ctx context.Context, tx *sql.Tx) error {
	for _, q := range []string{
		`ALTER TABLE entries RENAME TO tasks`,
		`ALTER TABLE entry_items RENAME TO task_items`,
		`ALTER TABLE task_items RENAME COLUMN entry_id TO task_id`,
		`ALTER TABLE entry_units RENAME TO task_units`,
		`ALTER TABLE task_units RENAME COLUMN entry_id TO task_id`,
		`ALTER TABLE entry_contexts RENAME TO task_contexts`,
		`ALTER TABLE task_contexts RENAME COLUMN entry_id TO task_id`,
		`ALTER TABLE entry_position RENAME TO task_position`,
		`ALTER TABLE projects RENAME COLUMN entry_id TO task_id`,
		`ALTER TABLE messages RENAME COLUMN entry_id TO task_id`,
		`ALTER TABLE drafts RENAME COLUMN entry TO task`,
		`DROP INDEX entries_state`,
		`DROP INDEX entries_rev`,
		`DROP INDEX entry_items_unit`,
		`DROP INDEX entry_units_unit`,
		`DROP INDEX messages_entry`,
		`DROP INDEX drafts_entry`,
		`CREATE INDEX tasks_state ON tasks (state)`,
		`CREATE INDEX tasks_rev ON tasks (rev)`,
		`CREATE INDEX task_items_unit ON task_items (provider, unit_key)`,
		`CREATE INDEX task_units_unit ON task_units (provider, unit_key)`,
		`CREATE INDEX messages_task ON messages (task_id)`,
		`CREATE INDEX drafts_task ON drafts (task, id)`,
		`UPDATE drafts SET effects = replace(effects, '"entry.nudged"', '"task.nudged"') WHERE instr(effects, '"entry.nudged"') > 0`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("%s: %w", q, err)
		}
	}
	if err := v7Providers(ctx, tx); err != nil {
		return err
	}
	if err := v7TaskInput(ctx, tx); err != nil {
		return err
	}
	if err := v7Workflows(ctx, tx); err != nil {
		return err
	}
	refs, err := taskRefs(ctx, tx)
	if err != nil {
		return err
	}
	for _, r := range refs {
		if r.form == refText {
			if err := rewriteText(ctx, tx, r, v7Text); err != nil {
				return err
			}
			continue
		}
		if err := v7IDs(ctx, tx, r); err != nil {
			return err
		}
	}
	return nil
}

// v7IDs gives the ids r's column holds the t- prefix.
func v7IDs(ctx context.Context, tx *sql.Tx, r taskRef) error {
	from, to := v6IDPrefix, v7IDPrefix
	if r.form == refOwner || r.form == refDossierPath {
		from, to = v6Task+"/"+v6IDPrefix, v7Task+"/"+v7IDPrefix
	}
	c := r.column
	q := `UPDATE ` + r.table + ` SET ` + c + ` = ?1 || substr(` + c + `, ?3) WHERE substr(` + c + `, 1, ?4) = ?2` //nolint:gosec // the names come from the store's own schema
	args := []any{to, from, len(from) + 1, len(from)}
	if r.provider != "" {
		q += ` AND ` + r.provider + ` = ?5`
		args = append(args, v7Task)
	}
	if _, err := tx.ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("the ids of %s.%s: %w", r.table, c, err)
	}
	return nil
}

// v7Providers names the task provider in every provider column.
func v7Providers(ctx context.Context, tx *sql.Tx) error {
	var cols [][2]string
	err := eachRow(ctx, tx, `SELECT m.name, p.name FROM sqlite_schema m JOIN pragma_table_info(m.name) p
		WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite_%' AND (p.name = 'provider' OR p.name LIKE '%\_provider' ESCAPE '\')`, nil,
		func(rows *sql.Rows) error {
			var c [2]string
			if err := rows.Scan(&c[0], &c[1]); err != nil {
				return err
			}
			cols = append(cols, c)
			return nil
		})
	if err != nil {
		return err
	}
	for _, c := range cols {
		if _, err := tx.ExecContext(ctx, `UPDATE `+c[0]+` SET `+c[1]+` = ? WHERE `+c[1]+` = ?`, v7Task, v6Task); err != nil { //nolint:gosec // the names come from the store's own schema
			return fmt.Errorf("the provider of %s.%s: %w", c[0], c[1], err)
		}
	}
	return nil
}

// v7TaskInput renames the reserved input entry of every turn to task.
func v7TaskInput(ctx context.Context, tx *sql.Tx) error {
	type row struct {
		seq    int64
		inputs string
	}
	var rows []row
	err := eachRow(ctx, tx, `SELECT seq, inputs FROM turns WHERE json_type(inputs, '$.entry') IS NOT NULL`, nil, func(r *sql.Rows) error {
		var x row
		if err := r.Scan(&x.seq, &x.inputs); err != nil {
			return err
		}
		rows = append(rows, x)
		return nil
	})
	if err != nil {
		return err
	}
	for _, x := range rows {
		in, err := decodeInputs(x.inputs)
		if err != nil {
			return fmt.Errorf("the inputs of turn %d: %w", x.seq, err)
		}
		v := in[v6Task]
		delete(in, v6Task)
		if s, ok := v.(string); ok {
			if rest, ok := strings.CutPrefix(s, v6IDPrefix); ok {
				v = v7IDPrefix + rest
			}
		}
		in[v7Task] = v
		out, err := encodeInputs(in)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE turns SET inputs = ? WHERE seq = ?`, out, x.seq); err != nil {
			return err
		}
	}
	return nil
}

var v6TextID = regexp.MustCompile(`(` + v6Task + `[:/])?` + v6IDPrefix + `([0-9a-f]{16})`)

// v7Text rewrites the version 6 task ids that stand as whole tokens in s, with their provider when s names
// a task's unit as entry:<id> or entry/<id>.
func v7Text(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range v6TextID.FindAllStringSubmatchIndex(s, -1) {
		start, end := m[0], m[1]
		if (start > 0 && idByte(s[start-1])) || (end < len(s) && idByte(s[end])) {
			continue
		}
		b.WriteString(s[last:start])
		if m[2] >= 0 {
			b.WriteString(v7Task + s[m[3]-1:m[3]])
		}
		b.WriteString(v7IDPrefix + s[m[4]:m[5]])
		last = end
	}
	b.WriteString(s[last:])
	return b.String()
}

// v7RenamedWorkflows are the bundled workflows version 7 renames, by their version 6 name.
var v7RenamedWorkflows = map[string]string{"entry-results-ended": "task-results-ended"}

// workflowColumns are the columns that hold a workflow's name under another name than workflow; prefixed
// holds it as "<name>/..." (a mark, a plan item's id).
var workflowColumns = []struct {
	table, column string
	prefixed      bool
}{
	{"drafts", "rule", false},
	{"unit_marks", "mark", true},
	{"plan_items", "id", true},
}

// v7Workflows renames the renamed workflows in every column named workflow and in workflowColumns, so a
// renamed workflow finds what it already handled.
func v7Workflows(ctx context.Context, tx *sql.Tx) error {
	type col struct {
		table, column string
		prefixed      bool
	}
	var cols []col
	err := eachRow(ctx, tx, `SELECT m.name, p.name FROM sqlite_schema m JOIN pragma_table_info(m.name) p
		WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite_%' ORDER BY m.name, p.cid`, nil, func(rows *sql.Rows) error {
		var c col
		if err := rows.Scan(&c.table, &c.column); err != nil {
			return err
		}
		if c.column == "workflow" {
			cols = append(cols, c)
		}
		for _, w := range workflowColumns {
			if w.table == c.table && w.column == c.column {
				cols = append(cols, col{c.table, c.column, w.prefixed})
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for from, to := range v7RenamedWorkflows {
		for _, c := range cols {
			q := `UPDATE ` + c.table + ` SET ` + c.column + ` = ?2 WHERE ` + c.column + ` = ?1` //nolint:gosec // the names come from the store's own schema
			args := []any{from, to}
			if c.prefixed {
				q = `UPDATE ` + c.table + ` SET ` + c.column + ` = ?2 || substr(` + c.column + `, ?3) WHERE substr(` + c.column + `, 1, ?4) = ?1` //nolint:gosec // the names come from the store's own schema
				args = []any{from + "/", to + "/", len(from) + 2, len(from) + 1}
			}
			if _, err := tx.ExecContext(ctx, q, args...); err != nil {
				return fmt.Errorf("the workflow %s in %s.%s: %w", from, c.table, c.column, err)
			}
		}
	}
	return nil
}
