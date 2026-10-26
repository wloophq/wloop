package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

func TestV4WritesTheLanguagesStoredValues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	frozen, err := os.ReadFile(filepath.Join("testdata", "schema-v3.sql"))
	if err != nil {
		t.Fatal(err)
	}
	path := storeAt(t, schemaSet{3, string(frozen), nil})
	stmts := []string{
		`INSERT INTO turns (provider, unit_key, intent, turn_id, state, sent_at, mode) VALUES
			('github', 'o/r#1', 'review', 't1', 'ended', 'x', ''), ('github', 'o/r#1', 'act', 't2', 'ended', 'x', 'headless')`,
		`INSERT INTO plan_items (id, workflow, provider, unit_key, key_value, decision, steps, mode, at) VALUES
			('a', 'review-first', 'github', 'o/r#1', 'k', 'pending',
				'[{"use":"turn.start","reach":"visible","args":{"intent":"review","round":"follow-up","mode":"headless"}}]', 'observe', '2026-10-01T10:00:00.000000000Z'),
			('b', 'slack-closure', 'slack', 'C1/1', 'k', 'done',
				'[{"use":"draft.propose","args":{"purpose":"close","on_posted":"unit.closed, entry.nudged","hold":"config.proposals.renudge_after","hold_after_sent":"forever","per":"subject"}}]', 'apply', '2026-10-01T10:00:00.000000000Z'),
			('c', 'slack-reply', 'slack', 'C1/2', 'k', 'blocked',
				'[{"use":"draft.propose","args":{"purpose":"reply","on_posted":"","per":"person","hold":"2h"}},{"use":"session.close"}]', 'apply', '2026-10-01T10:00:00.000000000Z'),
			('d', 'triage-mail', 'slack', 'C1/3', 'k', 'pending',
				'[{"use":"classify","args":{"prompt":"Is it urgent?","schema":"{}","into":"x"}},{"use":"notify.self","args":{"text":"Look at {{.unit.ref}}"}}]', 'apply', '2026-10-01T10:00:00.000000000Z')`,
	}
	rawExec(t, path, stmts...)
	if _, err := current.migrate(ctx, path, migrateOpts(path)); err != nil {
		t.Fatal(err)
	}
	db, err := current.open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	rows, err := db.r.Query(`SELECT intent, runner FROM turns ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var i, r string
		if err := rows.Scan(&i, &r); err != nil {
			t.Fatal(err)
		}
		got = append(got, i+"|"+r)
	}
	rows.Close()
	rows, err = db.r.Query(`SELECT id, observe, steps FROM plan_items ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id, steps string
		var observe int
		if err := rows.Scan(&id, &observe, &steps); err != nil {
			t.Fatal(err)
		}
		got = append(got, strings.Join([]string{id, []string{"apply", "observe"}[observe], steps}, "|"))
	}
	rows.Close()
	want := []string{
		"review|", "act|headless",
		`a|observe|[{"use":"turn.start","reach":"visible","args":{"intent":"review","runner":"headless","stage":"follow-up"}}]`,
		`b|apply|[{"use":"draft.propose","args":{"effects":["unit.closed","entry.nudged"],"per":"unit","purpose":"close","repeat_after":"config.drafts.repeat_after","repeat_after_sent":"forever"}}]`,
		`c|apply|[{"use":"draft.propose","args":{"effects":[],"per":"person","purpose":"reply","repeat_after":"2h"}},{"use":"session.close"}]`,
		`d|apply|[{"use":"classify","args":{"into":"x"}},{"use":"notify.self"}]`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("migrated\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	items, err := db.Ledger().Items(ctx, automation.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 4 || !items[0].Observe || items[1].Observe {
		t.Fatalf("the ledger reads %+v", items)
	}
	if got := items[1].Steps[0].Args["effects"]; !reflect.DeepEqual(got, []any{"unit.closed", "entry.nudged"}) {
		t.Errorf("effects read back as %#v", got)
	}
	if err := db.Ledger().Save(ctx, automation.PlanWrite{Workflows: []string{items[1].Workflow}, Items: []workflow.Item{items[1]}}); err != nil {
		t.Errorf("a list argument does not save: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	p, err := current.migrate(ctx, path, migrateOpts(path))
	if err != nil || len(p.Steps) != 0 {
		t.Errorf("a second open migrates %+v: %v", p, err)
	}
}
