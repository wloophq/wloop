package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// v7PullRequest is a pull request version 6 followed.
type v7PullRequest struct {
	key, title, state, created string
	followed                   bool
}

// v7PRTasks makes every pull request version 6 followed one task, as reconcile keeps it from version 7 on:
//   - the pull request's items carry its unit as their task key;
//   - the tasks whose first unit is the pull request, or that hold one of its items, merge into one, an open
//     one first, then one the loop derived, then the oldest; the others go, aliased to it;
//   - an open pull request the loop follows (scope reasons, a review workspace, tracked or adopted) that no
//     task holds gets one, waiting; a merged or closed one, or one nobody follows, gets none;
//   - the pull request is the task's first unit, so its brief is the task's;
//   - an open pull request's task in the inbox moves into the first ready state, as pr-task-clarify would;
//   - an open plan gate on the pull request with no task, or on a closed task while the pull request's task is
//     open, is the pull request task's.
func v7PRTasks(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `UPDATE items SET body = json_set(body, '$.task_key', provider || ':' || unit_key)
		WHERE provider = 'github' AND json_extract(body, '$.task_key') IS NULL`); err != nil {
		return err
	}
	var prs []v7PullRequest
	err := eachRow(ctx, tx, `SELECT p.owner || '/' || p.repo || '#' || p.number, p.title, p.state,
			coalesce(nullif(p.created_at, ''), nullif(p.updated_at, ''), (SELECT min(created) FROM tasks), '1970-01-01T00:00:00.000000000Z'),
			p.reasons != '' OR p.ws_review != 0 OR EXISTS (SELECT 1 FROM overrides o WHERE o.provider = 'github'
				AND o.unit_key = p.owner || '/' || p.repo || '#' || p.number AND (o.tracked != 0 OR o.taken_over != 0))
		FROM pull_requests p ORDER BY p.owner, p.repo, p.number`, nil, func(r *sql.Rows) error {
		var p v7PullRequest
		if err := r.Scan(&p.key, &p.title, &p.state, &p.created, &p.followed); err != nil {
			return err
		}
		prs = append(prs, p)
		return nil
	})
	if err != nil {
		return err
	}
	for _, p := range prs {
		if err := v7PRTask(ctx, tx, p); err != nil {
			return fmt.Errorf("the task of github:%s: %w", p.key, err)
		}
	}
	return nil
}

func v7PRTask(ctx context.Context, tx *sql.Tx, p v7PullRequest) error {
	var holders []string
	err := eachRow(ctx, tx, `SELECT id FROM tasks WHERE id IN (
			SELECT task_id FROM task_units u WHERE provider = 'github' AND unit_key = ?1
				AND ord = (SELECT min(ord) FROM task_units WHERE task_id = u.task_id)
			UNION SELECT task_id FROM task_items WHERE provider = 'github' AND unit_key = ?1)
		ORDER BY category IN ('done', 'dropped'), origin = 'gate', created, id`, []any{p.key}, func(r *sql.Rows) error {
		var id string
		if err := r.Scan(&id); err != nil {
			return err
		}
		holders = append(holders, id)
		return nil
	})
	if err != nil {
		return err
	}
	if len(holders) == 0 {
		if p.state != "OPEN" || !p.followed {
			return nil
		}
		id, err := v7NewPRTask(ctx, tx, p)
		if err != nil {
			return err
		}
		holders = []string{id}
	}
	keeper := holders[0]
	ids, err := json.Marshal(holders)
	if err != nil {
		return err
	}
	var changed bool
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(changed), 0) FROM tasks WHERE id IN (SELECT value FROM json_each(?))`, string(ids)).Scan(&changed); err != nil {
		return err
	}
	for _, loser := range holders[1:] {
		if err := v7Merge(ctx, tx, loser, keeper); err != nil {
			return err
		}
	}
	for _, q := range []string{
		`UPDATE tasks SET changed = ?2, origin = iif(origin = 'gate', 'derived', origin) WHERE id = ?1`,
		`UPDATE task_units SET ord = ord + 1 WHERE task_id = ?1 AND NOT (provider = 'github' AND unit_key = ?3)`,
		`INSERT INTO task_units (task_id, provider, unit_key, ord) VALUES (?1, 'github', ?3, 0)
			ON CONFLICT (task_id, provider, unit_key) DO UPDATE SET ord = 0`,
	} {
		if _, err := tx.ExecContext(ctx, q, keeper, changed, p.key); err != nil {
			return err
		}
	}
	if p.state == "OPEN" {
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET state = ?2, category = 'ready' WHERE id = ?1 AND category = 'inbox'`, keeper, v7FirstReady); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE gates SET task_id = ?1 WHERE status = 'open' AND kind = 'plan' AND provider = 'github' AND unit_key = ?2
		AND (task_id IS NULL OR task_id IN (SELECT id FROM tasks WHERE category IN ('done', 'dropped')))
		AND (SELECT category FROM tasks WHERE id = ?1) NOT IN ('done', 'dropped')`, keeper, p.key)
	return err
}

// v7FirstReady is the first state of the ready category version 6 knew, the one reconcile moves a task into.
var v7FirstReady = v7FirstOf("ready")

// v7FirstOf is the first state of category c version 6 knew.
func v7FirstOf(c string) string {
	for _, s := range v6States {
		if s.category == c {
			return s.state
		}
	}
	return ""
}

// v7NewPRTask inserts the task of p, an open pull request the loop follows that no task holds, under the id
// reconcile derives for its unit's task key. It waits: version 6 made a task of everything that waited on you,
// so what is left asks nothing of you, and the first sync gives it its asks.
func v7NewPRTask(ctx context.Context, tx *sql.Tx, p v7PullRequest) (string, error) {
	sum := sha256.Sum256([]byte("action|github|" + p.key + "|github:" + p.key))
	id := v7IDPrefix + hex.EncodeToString(sum[:])[:16]
	title := "Follow " + p.key
	if p.title != "" {
		title += ": " + p.title
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO tasks (id, state, category, title, origin, created, updated, rev)
		VALUES (?1, ?2, 'waiting', ?3, 'derived', ?4, ?4, (SELECT coalesce(max(rev), 0) + 1 FROM tasks))`, id, v7FirstOf("waiting"), title, p.created); err != nil {
		return "", err
	}
	return id, putV7Props(ctx, tx, []v7Prop{{id, "kind", map[string]string{"text": "pull-request"}, "plugin", p.created}})
}
