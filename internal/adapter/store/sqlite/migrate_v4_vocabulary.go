package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/intent/builtin"
)

// v4Vocabulary sets plan_items.observe from the mode v4 drops and rewrites
// each stored step's arguments to the profile language's names: turn.start's
// round and mode to stage and runner; draft.propose's hold and
// hold_after_sent to repeat_after and repeat_after_sent, its comma-separated
// on_posted to the list effects, per = "subject" to "unit", and a duration
// read from config.proposals.renudge_after to config.drafts.repeat_after;
// classify's inline prompt and schema and notify.self's inline text, which
// name files from v4, are dropped for the files' default names.
func v4Vocabulary(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `UPDATE plan_items SET observe = (mode = 'observe')`); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, steps FROM plan_items`)
	if err != nil {
		return err
	}
	rewritten := map[string]string{}
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return err
		}
		var steps []storedStep
		if err := json.Unmarshal([]byte(raw), &steps); err != nil {
			rows.Close()
			return fmt.Errorf("the steps of plan item %s: %w", id, err)
		}
		changed := false
		for i := range steps {
			if args := v4StepArgs(steps[i].Use, steps[i].Args); !reflect.DeepEqual(args, steps[i].Args) {
				steps[i].Args, changed = args, true
			}
		}
		if !changed {
			continue
		}
		b, err := json.Marshal(steps)
		if err != nil {
			rows.Close()
			return err
		}
		rewritten[id] = string(b)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for id, steps := range rewritten {
		if _, err := tx.ExecContext(ctx, `UPDATE plan_items SET steps = ? WHERE id = ?`, steps, id); err != nil {
			return err
		}
	}
	return nil
}

var v4ArgNames = map[string]map[string]string{
	builtin.StepTurnStart:    {"round": "stage", "mode": "runner"},
	builtin.StepDraftPropose: {"hold": "repeat_after", "hold_after_sent": "repeat_after_sent", "on_posted": "effects"},
}

// v4InlineArgs are the step arguments that held their text inline before v4
// and name a file from v4.
var v4InlineArgs = map[string][]string{
	builtin.StepClassify:   {"prompt", "schema"},
	builtin.StepNotifySelf: {"text"},
}

func v4StepArgs(use string, in map[string]any) map[string]any {
	names, inline := v4ArgNames[use], v4InlineArgs[use]
	if in == nil || names == nil && inline == nil {
		return in
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		if slices.Contains(inline, k) {
			continue
		}
		if n, ok := names[k]; ok {
			k = n
		}
		out[k] = v
	}
	if use != builtin.StepDraftPropose {
		return out
	}
	if s, ok := out["effects"].(string); ok {
		effects := []any{}
		for e := range strings.SplitSeq(s, ",") {
			if e = strings.TrimSpace(e); e != "" {
				effects = append(effects, e)
			}
		}
		out["effects"] = effects
	}
	if out["per"] == "subject" {
		out["per"] = "unit"
	}
	for _, k := range []string{"repeat_after", "repeat_after_sent"} {
		if out[k] == "config.proposals.renudge_after" {
			out[k] = "config.drafts.repeat_after"
		}
	}
	return out
}
