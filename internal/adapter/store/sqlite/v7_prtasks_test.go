package sqlite

import (
	"context"
	"slices"
	"testing"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const v6PRs = `INSERT INTO pull_requests (owner, repo, number, title, author, state, created_at, reasons, ws_review) VALUES
	('acme', 'api', 1, 'Fix the parser', 'ana', 'OPEN', '2026-09-20T09:00:00.000000000Z', 'involved', 0),
	('acme', 'api', 2, 'Drop the cache', 'ana', 'MERGED', '2026-09-21T09:00:00.000000000Z', 'involved', 0),
	('acme', 'api', 3, 'Add the export', 'bruno', 'OPEN', '2026-09-22T09:00:00.000000000Z', '', 1),
	('acme', 'api', 4, 'Bump the linter', 'bruno', 'OPEN', '2026-09-23T09:00:00.000000000Z', '', 0),
	('acme', 'api', 5, 'Read once in Explore', 'bruno', 'OPEN', '2026-09-24T09:00:00.000000000Z', '', 0)`

// v6Tracked tracks acme/api#4 by hand.
const v6Tracked = `INSERT INTO overrides (provider, unit_key, tracked) VALUES ('github', 'acme/api#4', 1)`

func TestV7PRTasksMergesSeveralTasksOfOnePR(t *testing.T) {
	t.Parallel()
	review, merge, address, thread := "e-00000000000000c1", "e-00000000000000c2", "e-00000000000000c3", "e-00000000000000c4"
	path := v6Store(t, v6PRs, v6Tracked,
		`INSERT INTO pr_seen (owner, repo, number, at) VALUES ('acme', 'api', 1, '2026-10-20T09:00:00.000000000Z')`,
		`INSERT INTO entries (id, kind, state, title, changed, origin, created, updated, rev) VALUES
			('`+review+`', 'action', 'inbox', 'Review acme/api#1', 0, 'derived', '2026-10-01T09:00:00.000000000Z', '2026-10-01T09:00:00.000000000Z', 1),
			('`+merge+`', 'action', 'done', 'Merge acme/api#1', 0, 'derived', '2026-09-30T09:00:00.000000000Z', '2026-09-30T09:00:00.000000000Z', 2),
			('`+address+`', 'action', 'next', 'Address acme/api#1', 1, 'derived', '2026-10-02T09:00:00.000000000Z', '2026-10-02T09:00:00.000000000Z', 3),
			('`+thread+`', 'capture', 'next', 'Answer Bruno about #1', 0, 'user', '2026-09-29T09:00:00.000000000Z', '2026-09-29T09:00:00.000000000Z', 4)`,
		`INSERT INTO entry_units (entry_id, provider, unit_key, ord) VALUES
			('`+review+`', 'github', 'acme/api#1', 0), ('`+merge+`', 'github', 'acme/api#1', 0), ('`+address+`', 'github', 'acme/api#1', 0),
			('`+thread+`', 'slack', 'T1/C1/1.1', 0), ('`+thread+`', 'github', 'acme/api#1', 1)`,
		`INSERT INTO entry_items (entry_id, provider, unit_key, item) VALUES
			('`+review+`', 'github', 'acme/api#1', 'gh:review'), ('`+merge+`', 'github', 'acme/api#1', 'gh:merge'),
			('`+address+`', 'github', 'acme/api#1', 'gh:address')`,
		`INSERT INTO items (provider, unit_key, id, body, state, provenance, created_at, updated_at) VALUES
			('github', 'acme/api#1', 'gh:review', '{"id":"gh:review","summary":"Review acme/api#1"}', 'open', 'derived', '2026-10-01T09:00:00Z', '2026-10-01T09:00:00Z'),
			('github', 'acme/api#1', 'gh:merge', '{"id":"gh:merge","summary":"Merge acme/api#1"}', 'resolved', 'derived', '2026-09-30T09:00:00Z', '2026-09-30T09:00:00Z'),
			('github', 'acme/api#1', 'gh:address', '{"id":"gh:address","summary":"Address acme/api#1"}', 'open', 'derived', '2026-10-02T09:00:00Z', '2026-10-02T09:00:00Z')`,
	)
	db := migrateV6(t, path)
	raw := rawDB(t, path)
	keeper := "t-00000000000000c1"
	if got := strings1(t, raw, `SELECT DISTINCT task_id FROM task_units WHERE provider = 'github' AND unit_key = 'acme/api#1' AND ord = 0
		UNION SELECT DISTINCT task_id FROM task_items WHERE provider = 'github' AND unit_key = 'acme/api#1'`); !slices.Equal(got, []string{keeper}) {
		t.Fatalf("the tasks of acme/api#1 %q, want the open oldest %s alone", got, keeper)
	}
	if got := strings1(t, raw, `SELECT old_id || ' ' || new_id FROM task_aliases ORDER BY old_id`); !slices.Equal(got,
		[]string{"t-00000000000000c2 " + keeper, "t-00000000000000c3 " + keeper}) {
		t.Errorf("aliases %q", got)
	}
	if got := strings1(t, raw, `SELECT item FROM task_items WHERE task_id = ? ORDER BY item`, keeper); !slices.Equal(got, []string{"gh:address", "gh:merge", "gh:review"}) {
		t.Errorf("the merged task holds %q", got)
	}
	if got := strings1(t, raw, `SELECT provider || ':' || unit_key FROM task_units WHERE task_id = ? ORDER BY ord`, "t-00000000000000c4"); !slices.Equal(got,
		[]string{"slack:T1/C1/1.1", "github:acme/api#1"}) {
		t.Errorf("a thread's task that names the pull request second is not the pull request's: %q", got)
	}
	if got := strings1(t, raw, `SELECT DISTINCT json_extract(body, '$.task_key') FROM items WHERE provider = 'github'`); !slices.Equal(got, []string{"github:acme/api#1"}) {
		t.Errorf("the pull request's items' task key %q", got)
	}
	t.Run("the unseen marker is Changed", func(t *testing.T) {
		e, err := db.Tasks().Task(context.Background(), task.ID(keeper))
		if err != nil {
			t.Fatal(err)
		}
		if !e.Changed || !e.SeenAt.IsZero() || e.Category != task.CategoryReady {
			t.Fatalf("the merged task changed %v, seen at %s, %s: a merged task changed shows changed, and the sighting of a search is no reading", e.Changed, e.SeenAt, e.Category)
		}
	})
	for n, want := range map[string]string{"2": "0", "3": "1", "4": "1", "5": "0"} {
		if got := strings1(t, raw, `SELECT count(*) FROM task_units WHERE provider = 'github' AND unit_key = 'acme/api#`+n+`' AND ord = 0`); !slices.Equal(got, []string{want}) {
			t.Errorf("acme/api#%s has %q tasks, want %s: an open pull request the loop follows gets one, a merged one or one nobody follows none", n, got, want)
		}
	}
	if got := strings1(t, raw, `SELECT t.state || ' ' || t.origin || ' ' || json_extract(p.value, '$.text') FROM task_units u JOIN tasks t ON t.id = u.task_id
		JOIN task_props p ON p.task_id = t.id AND p.name = 'kind' WHERE u.provider = 'github' AND u.unit_key IN ('acme/api#3', 'acme/api#4') ORDER BY u.unit_key`); !slices.Equal(got,
		[]string{"waiting derived pull-request", "waiting derived pull-request"}) {
		t.Errorf("the tasks made for the followed pull requests no task held %q, want waiting: they ask nothing of you yet", got)
	}
}

func TestV7PRTasksClarifiesWithoutASweep(t *testing.T) {
	t.Parallel()
	inbox, waiting := "e-00000000000000d1", "e-00000000000000d2"
	path := v6Store(t, v6PRs, v6Tracked,
		`INSERT INTO entries (id, kind, state, title, origin, created, updated, rev) VALUES
			('`+inbox+`', 'action', 'inbox', 'Review acme/api#1', 'derived', '2026-10-01T09:00:00.000000000Z', '2026-10-01T09:00:00.000000000Z', 1),
			('`+waiting+`', 'action', 'waiting', 'Wait for bruno on acme/api#3', 'derived', '2026-10-01T09:00:00.000000000Z', '2026-10-01T09:00:00.000000000Z', 2)`,
		`INSERT INTO entry_units (entry_id, provider, unit_key, ord) VALUES ('`+inbox+`', 'github', 'acme/api#1', 0), ('`+waiting+`', 'github', 'acme/api#3', 0)`,
	)
	db := migrateV6(t, path)
	raw := rawDB(t, path)
	if got := strings1(t, raw, `SELECT u.unit_key || ' ' || t.state || ' ' || t.category FROM task_units u JOIN tasks t ON t.id = u.task_id
		WHERE u.provider = 'github' AND u.ord = 0 ORDER BY u.unit_key`); !slices.Equal(got, []string{
		"acme/api#1 next ready", "acme/api#3 waiting waiting", "acme/api#4 waiting waiting"}) {
		t.Fatalf("the pull requests' tasks %q", got)
	}
	me := items.Person{Key: "github:me"}
	pr4 := unit.Unit{Provider: unit.GitHub, Key: "acme/api#4"}
	review := items.Item{ID: "gh:review", Unit: pr4, Summary: "Review acme/api#4", Requester: items.Person{Key: "github:bruno"},
		WaitingOn: []items.Person{me}, State: items.Open, Initial: items.InitialInbox, TaskKey: pr4.String()}
	svc := coretasks.New(coretasks.Deps{Store: db.Tasks(), Items: v7Items{pr4: {review}}, Facts: v7Facts{}, Me: task.Me{Keys: []string{me.Key}},
		Clock: v7Clock{}, States: taskstest.BundledStates})
	if err := svc.Sync(context.Background(), []unit.Unit{pr4}); err != nil {
		t.Fatal(err)
	}
	ts, err := db.Tasks().TasksOf(context.Background(), pr4)
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 1 || ts[0].Category != task.CategoryWaiting || !ts[0].Changed || !slices.Contains(ts[0].Items, task.ItemRef{Unit: pr4, Item: "gh:review"}) {
		t.Fatalf("the first sync after the upgrade %+v: the review request goes to the pull request's task, changed", ts)
	}
}
