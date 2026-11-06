package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/merge"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/wake"
)

var _ coretasks.Merger = taskStore{}

// mergeHandling is how a merge treats a column taskRefs finds naming the merged task.
type mergeHandling int

const (
	// mergeMoves: the rows naming the merged task name the survivor after the merge, and split points back
	// those of its units and of the record's ids.
	mergeMoves mergeHandling = iota
	// mergeDrops: the merged task's own state, which the merge record keeps and split writes back.
	mergeDrops
	// mergeKeeps: history that stays under the merged task's id, which the survivor's readers reach through
	// the aliases.
	mergeKeeps
	// mergeRefuses: a merge refuses a task these columns name, such as a project's child naming its parent.
	mergeRefuses
)

// mergeColumns are, for every column taskRefs finds, how a merge treats it, by table.column.
var mergeColumns = map[string]mergeHandling{
	"gates.task_id":    mergeMoves,
	"drafts.task":      mergeMoves,
	"messages.task_id": mergeMoves,

	"tasks.id":                 mergeDrops,
	"task_items.task_id":       mergeDrops,
	"task_props.task_id":       mergeDrops,
	"task_units.task_id":       mergeDrops,
	"wakes.task_id":            mergeDrops,
	"unit_marks.unit_key":      mergeDrops,
	"task_aliases.old_id":      mergeDrops,
	"task_aliases.new_id":      mergeKeeps,
	"tasks.parent":             mergeRefuses,
	"task_items.unit_key":      mergeKeeps,
	"task_units.unit_key":      mergeKeeps,
	"brief_runs.unit_key":      mergeKeeps,
	"classifications.unit_key": mergeKeeps,
	"dossiers.owner":           mergeKeeps,
	"dossiers.rel_path":        mergeKeeps,
	"drafts.unit_key":          mergeKeeps,
	"drafts.rule_key":          mergeKeeps,
	"drafts.origin_unit_key":   mergeKeeps,
	"events.unit_key":          mergeKeeps,
	"events.key":               mergeKeeps,
	"events.data":              mergeKeeps,
	"gates.unit_key":           mergeKeeps,
	"intakes.unit_key":         mergeKeeps,
	"item_ops.unit_key":        mergeKeeps,
	"item_proposals.unit_key":  mergeKeeps,
	"items.unit_key":           mergeKeeps,
	"links.unit_key":           mergeKeeps,
	"links.to_key":             mergeKeeps,
	"messages.unit_key":        mergeKeeps,
	"overrides.unit_key":       mergeKeeps,
	"placements.unit_key":      mergeKeeps,
	"plan_items.id":            mergeKeeps,
	"plan_items.unit_key":      mergeKeeps,
	"plan_items.key_value":     mergeKeeps,
	"round_day_prs.unit_key":   mergeKeeps,
	"round_state.unit_key":     mergeKeeps,
	"sessions.unit_key":        mergeKeeps,
	"turn_answers.unit_key":    mergeKeeps,
	"turn_records.unit_key":    mergeKeeps,
	"turns.unit_key":           mergeKeeps,
	"turns.inputs":             mergeKeeps,
	"unit_briefs.unit_key":     mergeKeeps,
	"unit_cards.unit_key":      mergeKeeps,
	"unit_snapshots.unit_key":  mergeKeeps,
	"view_applied.unit_key":    mergeKeeps,
	"workflow_runs.unit_key":   mergeKeeps,
	"workflow_runs.key_value":  mergeKeeps,
	"worktree_units.unit_key":  mergeKeeps,
}

// unhandledMergeColumns are the columns of the live schema that can name a task and that mergeColumns does
// not say how a merge treats.
func unhandledMergeColumns(ctx context.Context, q rowsQueryer) ([]string, error) {
	refs, err := taskRefs(ctx, q)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range refs {
		if _, ok := mergeColumns[r.table+"."+r.column]; !ok {
			out = append(out, r.table+"."+r.column)
		}
	}
	return out, nil
}

// movedColumns are the rows a merge points at the survivor: table, id column, task column.
var movedColumns = []struct{ table, task string }{{"gates", "task_id"}, {"drafts", "task"}, {"messages", "task_id"}}

func (s taskStore) Merge(ctx context.Context, r merge.Record, into task.Task, evs ...port.Event) error {
	if into.ID != r.Into || r.From == r.Into {
		return fmt.Errorf("%w: the merge of %s into %s writes %s", coretasks.ErrVerb, r.From, r.Into, into.ID)
	}
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		from, err := loadTasks(ctx, tx, `id = ?`, string(r.From))
		if err != nil {
			return err
		}
		cur, err := loadTasks(ctx, tx, `id = ?`, string(r.Into))
		if err != nil {
			return err
		}
		if len(from) == 0 || len(cur) == 0 {
			return fmt.Errorf("%w: the merge of %s into %s", coretasks.ErrNoTask, r.From, r.Into)
		}
		if !from[0].Same(r.Task) {
			return fmt.Errorf("%w: task %s changed since the merge read it", coretasks.ErrVerb, r.From)
		}
		var children int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM tasks WHERE parent = ?`, string(r.From)).Scan(&children); err != nil {
			return err
		}
		if children > 0 {
			return fmt.Errorf("%w: task %s is the parent of %d tasks", coretasks.ErrVerb, r.From, children)
		}
		ws, err := scanWakes(tx.QueryContext(ctx, `SELECT `+wakeColumns+` FROM wakes WHERE task_id = ?`, string(r.From)))
		if err != nil {
			return err
		}
		r.Wake = nil
		if len(ws) == 1 {
			r.Wake = &ws[0]
		}
		if r.Marks, err = unitMarks(ctx, tx, task.UnitOf(r.From)); err != nil {
			return err
		}
		lists := []*[]int64{&r.Gates, &r.Drafts, &r.Messages}
		for i, c := range movedColumns {
			ids, err := int64s(ctx, tx, `SELECT id FROM `+c.table+` WHERE `+c.task+` = ? ORDER BY id`, string(r.From)) //nolint:gosec // fixed names
			if err != nil {
				return err
			}
			*lists[i] = ids
			if err := pointRows(ctx, tx, c.table, c.task, r.Into, ids); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM tasks WHERE id = ?`, string(r.From)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO task_aliases (old_id, new_id) VALUES (?, ?)`, string(r.From), string(r.Into)); err != nil {
			return err
		}
		rev, err := newPosition(ctx, tx)
		if err != nil {
			return err
		}
		if err := writeTask(ctx, tx, into, rev); err != nil {
			return err
		}
		if err := adoptUnits(ctx, tx, into); err != nil {
			return err
		}
		data, err := json.Marshal(r)
		if err != nil {
			return err
		}
		rec := port.Event{Unit: task.UnitOf(r.From), Kind: merge.RecordKind, At: r.At, Data: string(data)}
		touchMerge(tx, from[0], into, len(r.Gates), len(r.Drafts), r.Wake != nil)
		return writeEvents(ctx, tx, append([]port.Event{rec}, evs...))
	})
	return err
}

func (s taskStore) Split(ctx context.Context, r merge.Record, sp merge.Split, evs ...port.Event) error {
	from, into := sp.From, sp.Rest
	if from.ID != r.From || into.ID != r.Into {
		return fmt.Errorf("%w: the split of %s from %s writes %s and %s", coretasks.ErrVerb, r.From, r.Into, from.ID, into.ID)
	}
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		latest, ok, err := mergeOf(ctx, tx, r.From)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: task %s is stored, or was never merged", coretasks.ErrVerb, r.From)
		}
		if latest.Into != r.Into || !latest.At.Equal(r.At) {
			return fmt.Errorf("%w: task %s was last merged into %s", coretasks.ErrVerb, r.From, latest.Into)
		}
		cur, err := loadTasks(ctx, tx, `id = ?`, string(r.Into))
		if err != nil {
			return err
		}
		if len(cur) == 0 {
			return fmt.Errorf("%w: task %s, which %s was merged into, is not stored", coretasks.ErrVerb, r.Into, r.From)
		}
		rev, err := newPosition(ctx, tx)
		if err != nil {
			return err
		}
		if err := writeTask(ctx, tx, from, rev); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET open_changed_at = ? WHERE id = ?`, eventTime(from.OpenChanged), string(from.ID)); err != nil {
			return err
		}
		if err := writeTask(ctx, tx, into, rev); err != nil {
			return err
		}
		if w := r.Wake; w != nil && from.Open() {
			if err := putWake(ctx, tx, *w); err != nil {
				return err
			}
		}
		for name, value := range r.Marks {
			if err := saveMark(ctx, tx, task.UnitOf(r.From), name, value); err != nil {
				return err
			}
		}
		units := append([]unit.Unit{task.UnitOf(r.From)}, sp.Units...)
		lists := [][]int64{r.Gates, r.Drafts, r.Messages}
		var kept [3][]int64
		for _, o := range sp.Others {
			for i, ids := range [][]int64{o.Gates, o.Drafts, o.Messages} {
				kept[i] = append(kept[i], ids...)
			}
		}
		var moved [3]int
		for i, c := range movedColumns {
			ids, err := int64s(ctx, tx, `SELECT id FROM `+c.table+` WHERE `+c.task+` = ? AND `+unitsIn(len(units)), //nolint:gosec // fixed names
				append([]any{string(r.Into)}, unitArgs(units)...)...)
			if err != nil {
				return err
			}
			for _, id := range lists[i] {
				var n int
				if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM `+c.table+` WHERE id = ? AND `+c.task+` = ?`, id, string(r.Into)).Scan(&n); err != nil { //nolint:gosec // fixed names
					return err
				}
				if n == 1 && !slices.Contains(ids, id) {
					ids = append(ids, id)
				}
			}
			ids = slices.DeleteFunc(ids, func(id int64) bool { return slices.Contains(kept[i], id) && !slices.Contains(lists[i], id) })
			if err := pointRows(ctx, tx, c.table, c.task, r.From, ids); err != nil {
				return err
			}
			moved[i] = len(ids)
		}
		for _, h := range sp.Heirs {
			data, err := json.Marshal(h)
			if err != nil {
				return err
			}
			if err := writeEvents(ctx, tx, []port.Event{{Unit: task.UnitOf(h.From), Kind: merge.RecordKind, At: h.At, Data: string(data)}}); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM task_aliases WHERE old_id = ? AND new_id = ?`, string(r.From), string(r.Into)); err != nil {
			return err
		}
		touchMerge(tx, from, into, moved[0], moved[1], r.Wake != nil && from.Open())
		return writeEvents(ctx, tx, evs)
	})
	return err
}

func (s taskStore) MergeOf(ctx context.Context, id task.ID) (merge.Record, bool, error) {
	var r merge.Record
	var ok bool
	err := s.db.Read(ctx, func(tx *sql.Tx) error {
		var err error
		r, ok, err = mergeOf(ctx, tx, id)
		return err
	})
	return r, ok, err
}

// mergeOf is the record of the merge that took task id, the one of the highest event id; false when id is
// stored or was never merged.
func mergeOf(ctx context.Context, q rowQuerier, id task.ID) (merge.Record, bool, error) {
	var stored int
	if err := q.QueryRowContext(ctx, `SELECT count(*) FROM tasks WHERE id = ?`, string(id)).Scan(&stored); err != nil {
		return merge.Record{}, false, err
	}
	if stored > 0 {
		return merge.Record{}, false, nil
	}
	u := task.UnitOf(id)
	var data string
	err := q.QueryRowContext(ctx, `SELECT data FROM events WHERE provider = ? AND unit_key = ? AND kind = ? ORDER BY id DESC LIMIT 1`,
		string(u.Provider), u.Key, merge.RecordKind).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return merge.Record{}, false, nil
	}
	if err != nil {
		return merge.Record{}, false, err
	}
	var r merge.Record
	if err := json.Unmarshal([]byte(data), &r); err != nil {
		return merge.Record{}, false, fmt.Errorf("the merge record of %s: %w", id, err)
	}
	return r, true, nil
}

// newPosition starts a new epoch of the tasks' position and returns its first rev, so a page since an older
// epoch is Full.
func newPosition(ctx context.Context, tx *Tx) (uint64, error) {
	_, rev, err := readPosition(ctx, tx)
	if err != nil {
		return 0, err
	}
	rev++
	_, err = tx.ExecContext(ctx, `INSERT INTO task_position (only, epoch, rev) VALUES (1, ?, ?)
		ON CONFLICT (only) DO UPDATE SET epoch = excluded.epoch, rev = excluded.rev`, newEpoch(), rev)
	return rev, err
}

// pointRows points the rows of table whose id is in ids at task id, in its column col.
func pointRows(ctx context.Context, tx *Tx, table, col string, id task.ID, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	set := col + ` = ?`
	if table == "drafts" {
		set += `, rev = rev + 1`
	}
	args := []any{string(id)}
	for _, x := range ids {
		args = append(args, x)
	}
	_, err := tx.ExecContext(ctx, `UPDATE `+table+` SET `+set+` WHERE id IN (?`+strings.Repeat(`, ?`, len(ids)-1)+`)`, args...) //nolint:gosec // fixed names
	return err
}

// touchMerge notes the tasks of both and their units, and the gates, drafts and wake the move pointed.
func touchMerge(tx *Tx, from, into task.Task, gates, drafts int, woke bool) {
	units := append(append(taskUnits(from), taskUnits(into)...), task.UnitOf(from.ID), task.UnitOf(into.ID))
	topics := port.TopicTasks
	if gates > 0 {
		topics |= port.TopicGates
	}
	if drafts > 0 {
		topics |= port.TopicDrafts
	}
	if woke {
		topics |= port.TopicWakes
	}
	tx.Touch(topics, units...)
}

func int64s(ctx context.Context, q querier, query string, args ...any) ([]int64, error) {
	var out []int64
	err := eachRow(ctx, q, query, args, func(r *sql.Rows) error {
		var id int64
		if err := r.Scan(&id); err != nil {
			return err
		}
		out = append(out, id)
		return nil
	})
	return out, err
}

func unitMarks(ctx context.Context, q querier, u unit.Unit) (map[string]string, error) {
	out := map[string]string{}
	err := eachRow(ctx, q, `SELECT mark, value FROM unit_marks WHERE provider = ? AND unit_key = ?`, []any{string(u.Provider), u.Key},
		func(r *sql.Rows) error {
			var name, value string
			if err := r.Scan(&name, &value); err != nil {
				return err
			}
			out[name] = value
			return nil
		})
	if len(out) == 0 {
		return nil, err
	}
	return out, err
}

func putWake(ctx context.Context, tx *Tx, w wake.Wake) error {
	on, err := json.Marshal(nonNilList(w.On))
	if err != nil {
		return err
	}
	actors, err := json.Marshal(nonNilList(w.Actors))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO wakes (`+wakeColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (task_id) DO UPDATE SET until = excluded.until, on_kinds = excluded.on_kinds, actors = excluded.actors,
		cond = excluded.cond, expires = excluded.expires, return_state = excluded.return_state`,
		string(w.Task), eventTime(w.Until), string(on), string(actors), w.When, eventTime(w.Expires), w.Return)
	return err
}

// TasksRunByHand are, of ids, or of every task when ids is empty, the tasks on whose own unit an event of one of
// kinds was recorded by a person or an agent.
func (db *DB) TasksRunByHand(ctx context.Context, kinds []string, ids ...task.ID) (map[task.ID]bool, error) {
	out := map[task.ID]bool{}
	if len(kinds) == 0 {
		return out, nil
	}
	q := `SELECT DISTINCT unit_key FROM events WHERE provider = ? AND kind IN (?` + strings.Repeat(`, ?`, len(kinds)-1) +
		`) AND (actor LIKE ? OR actor LIKE ?)`
	args := []any{string(task.UnitOf("").Provider)}
	for _, k := range kinds {
		args = append(args, k)
	}
	args = append(args, string(task.ActorPerson)+":%", string(task.ActorAgent)+":%")
	if len(ids) > 0 {
		q += ` AND unit_key IN (?` + strings.Repeat(`, ?`, len(ids)-1) + `)`
		for _, id := range ids {
			args = append(args, string(id))
		}
	}
	err := eachRow(ctx, db.R(), q, args, func(r *sql.Rows) error {
		var id string
		if err := r.Scan(&id); err != nil {
			return err
		}
		out[task.ID(id)] = true
		return nil
	})
	return out, err
}
