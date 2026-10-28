package cli

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/core/change"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

const doneWorkflow = `summary = "React to an entry marked done"
provider = "entry"
when = 'trigger.event.kind == "verb_done"'
key = ["trigger.event.id"]
observe = true

[trigger]
on = ["verb_done"]

[[step]]
use = "notify.self"
`

func TestAVerbAPersonRunsPlansItsWorkflow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	te := newTestEnv(t, &fake.Manager{})
	userProfile(t, te, map[string]string{
		"workflows/my-done/workflow.toml": doneWorkflow,
		"workflows/my-done/text.md.tmpl":  "done",
	})
	if l := te.loadedIntents(); len(l.Problems) > 0 {
		t.Fatal(l.Problems.Error())
	}
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "s.db"), change.NewLog(change.DefaultWindow, nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ed := engineDeps{DB: db, SessionFacts: noUnits{}, WorkspaceFacts: noUnits{}}
	te.wireSubjects(&serveWiring{DB: db, Cfg: te.cfg, Engine: &ed})
	eng := te.planEngine(ed)
	if err := db.Events().Advance(ctx, "my-done", 0, time.Time{}); err != nil {
		t.Fatal(err)
	}
	g := coretasks.New(coretasks.Deps{Store: db.Entries(), Facts: coretasks.CardFacts{Cards: db.Cards()}, Clock: clockFunc(func() time.Time { return te.Now() })})
	at := testNow.Add(-time.Hour)
	if err := db.Entries().Apply(ctx, []task.Change{{Op: task.OpCreate, Entry: task.Entry{ID: "e-01", Kind: task.KindCapture, State: task.Next,
		Title: "Ship it", Origin: task.OriginUser, Created: at, Updated: at}}}); err != nil {
		t.Fatal(err)
	}
	for _, v := range []task.Verb{task.VerbDone, task.VerbReference, task.VerbDone} {
		if _, err := g.Do(ctx, "e-01", coretasks.Request{Verb: v, Source: "cockpit"}); err != nil {
			t.Fatalf("%s: %v", v, err)
		}
	}

	p, err := eng.Evaluate(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Errors) > 0 {
		t.Fatalf("plan errors: %v", p.Errors)
	}
	var left []string
	for _, it := range p.Items {
		if it.Workflow == "my-done" && it.Unit == task.UnitOf("e-01") && it.Why.Event != nil {
			left = append(left, it.Why.Event.Data)
		}
	}
	if len(left) != 2 || left[0] != "next" || left[1] != "reference" {
		t.Fatalf("my-done planned from the states %v, want next then reference", left)
	}
}
