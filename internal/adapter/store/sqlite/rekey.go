package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// refForm is how a column spells a task's id.
type refForm int

const (
	refWhole       refForm = iota // the value is the id
	refOwner                      // a dossier's owner: "task/<id>"
	refDossierPath                // a dossier's directory: "task/<id>#"
	refText                       // the id among other text: a JSON value, a rule key
)

func (f refForm) spell(id string) string {
	switch f {
	case refOwner:
		return v7Task + "/" + id
	case refDossierPath:
		return v7Task + "/" + id + "#"
	case refText:
		return `{"task":"` + id + `"}`
	}
	return id
}

// taskColumns are the columns that hold a task's id under a name that does not say so. Every column named
// task_id, and every provider column with its key column, is found by its name.
var taskColumns = []struct {
	table, column string
	form          refForm
}{
	{"tasks", "id", refWhole},
	{"tasks", "project", refWhole},
	{"tasks", "parent", refWhole},
	{"drafts", "task", refWhole},
	{"task_aliases", "old_id", refWhole},
	{"task_aliases", "new_id", refWhole},
	{"dossiers", "owner", refOwner},
	{"dossiers", "rel_path", refDossierPath},
	{"turns", "inputs", refText},
	{"drafts", "rule_key", refText},
	{"workflow_runs", "key_value", refText},
	{"plan_items", "key_value", refText},
	{"plan_items", "id", refText},
	{"events", "key", refText},
	{"events", "data", refText},
}

// taskRef is a column of the live schema that can name a task.
type taskRef struct {
	table, column string
	// provider is the provider column of a unit's key column: the column names a task where it is the task
	// provider.
	provider string
	form     refForm
	// clear says the reference links the row to a task it does not belong to, so dropping the task empties
	// the column instead of deleting the row: a column with the default '' outside the primary key, or a
	// prefixed unit (origin_, to_) with one.
	clear bool
}

// taskRefs walks the live schema for the columns that can name a task.
func taskRefs(ctx context.Context, q rowsQueryer) ([]taskRef, error) {
	type col struct {
		name, dflt string
		pk         int
	}
	tables := map[string]map[string]col{}
	var order []string
	rows, err := q.QueryContext(ctx, `SELECT m.name, p.name, coalesce(p.dflt_value, ''), p.pk
		FROM sqlite_schema m JOIN pragma_table_info(m.name) p
		WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite_%' ORDER BY m.name, p.cid`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var table string
		var c col
		if err := rows.Scan(&table, &c.name, &c.dflt, &c.pk); err != nil {
			rows.Close()
			return nil, err
		}
		if tables[table] == nil {
			tables[table] = map[string]col{}
		}
		tables[table][c.name] = c
		order = append(order, table+"\x00"+c.name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	link := func(c col) bool { return c.dflt == "''" && c.pk == 0 }
	var out []taskRef
	for _, tc := range order {
		table, name, _ := strings.Cut(tc, "\x00")
		cols := tables[table]
		c := cols[name]
		switch {
		case name == "task_id":
			out = append(out, taskRef{table: table, column: name, clear: link(c)})
		case name == "provider" || strings.HasSuffix(name, "_provider"):
			prefix := strings.TrimSuffix(name, "provider")
			key, ok := cols[prefix+"unit_key"]
			if !ok {
				key, ok = cols[prefix+"key"]
			}
			if ok {
				out = append(out, taskRef{table: table, column: key.name, provider: name, clear: prefix != "" && link(c) && link(key)})
			}
		}
		for _, r := range taskColumns {
			if r.table == table && r.column == name {
				out = append(out, taskRef{table: table, column: name, form: r.form, clear: r.form == refWhole && link(c)})
			}
		}
	}
	return out, nil
}

// where is the condition that holds on a row of r naming value.
func (r taskRef) where() string {
	w := r.column + ` = ?1`
	if r.provider != "" {
		w += ` AND ` + r.provider + ` = '` + v7Task + `'`
	}
	return w
}

// rekeyTask moves every reference of task from to task to. A row that would then repeat one of to's is
// dropped, so to keeps its own.
func rekeyTask(ctx context.Context, tx *sql.Tx, from, to string) error {
	refs, err := taskRefs(ctx, tx)
	if err != nil {
		return err
	}
	for _, r := range refs {
		if r.form == refText {
			if err := rewriteText(ctx, tx, r, func(s string) string { return replaceID(s, from, to) }); err != nil {
				return err
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE OR IGNORE `+r.table+` SET `+r.column+` = ?2 WHERE `+r.where(), r.form.spell(from), r.form.spell(to)); err != nil { //nolint:gosec // the names come from the store's own schema
			return fmt.Errorf("rekeying %s.%s: %w", r.table, r.column, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+r.table+` WHERE `+r.where(), r.form.spell(from)); err != nil { //nolint:gosec // the names come from the store's own schema
			return fmt.Errorf("rekeying %s.%s: %w", r.table, r.column, err)
		}
	}
	return nil
}

// dropTask deletes the rows of task id and empties the references to it of rows that are not its own. Text
// that mentions it (a turn's inputs, a rule key) stays as history.
func dropTask(ctx context.Context, tx *sql.Tx, id string) error {
	refs, err := taskRefs(ctx, tx)
	if err != nil {
		return err
	}
	for _, r := range refs {
		if r.form == refText {
			continue
		}
		q := `DELETE FROM ` + r.table + ` WHERE ` + r.where() //nolint:gosec // the names come from the store's own schema
		if r.clear {
			set := r.column + ` = ''`
			if r.provider != "" {
				set += `, ` + r.provider + ` = ''`
			}
			q = `UPDATE ` + r.table + ` SET ` + set + ` WHERE ` + r.where()
		}
		if _, err := tx.ExecContext(ctx, q, r.form.spell(id)); err != nil {
			return fmt.Errorf("dropping task %s from %s.%s: %w", id, r.table, r.column, err)
		}
	}
	return nil
}

// rewriteText replaces each value of r's column that holds a task id with what fn makes of it; a row
// whose new value another row already holds is dropped.
func rewriteText(ctx context.Context, tx *sql.Tx, r taskRef, fn func(string) string) error {
	var vals []string
	err := eachRow(ctx, tx, `SELECT DISTINCT `+r.column+` FROM `+r.table+` WHERE instr(`+r.column+`, '-') > 0`, nil, func(rows *sql.Rows) error {
		var v string
		if err := rows.Scan(&v); err != nil {
			return err
		}
		vals = append(vals, v)
		return nil
	})
	if err != nil {
		return fmt.Errorf("reading %s.%s: %w", r.table, r.column, err)
	}
	for _, v := range vals {
		if nv := fn(v); nv != v {
			if _, err := tx.ExecContext(ctx, `UPDATE OR IGNORE `+r.table+` SET `+r.column+` = ? WHERE `+r.column+` = ?`, nv, v); err != nil { //nolint:gosec // the names come from the store's own schema
				return fmt.Errorf("rewriting %s.%s: %w", r.table, r.column, err)
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+r.table+` WHERE `+r.column+` = ?`, v); err != nil { //nolint:gosec // the names come from the store's own schema
				return fmt.Errorf("rewriting %s.%s: %w", r.table, r.column, err)
			}
		}
	}
	return nil
}

// replaceID replaces each occurrence of id in s that stands as a whole token, not inside a longer id.
func replaceID(s, id, to string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, id)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		end := i + len(id)
		whole := (i == 0 || !idByte(s[i-1])) && (end == len(s) || !idByte(s[end]))
		b.WriteString(s[:i])
		if whole {
			b.WriteString(to)
		} else {
			b.WriteString(id)
		}
		s = s[end:]
	}
}

func idByte(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c == '-'
}
