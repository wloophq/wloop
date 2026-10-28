package cli

import (
	"context"
	"sync"
	"testing"

	"github.com/wloophq/wloop/internal/domain/task"
)

const prioritizeWorkflow = `summary = "Raise a next action's priority"
provider = "task"
when = 'task.state == "next"'

[[step]]
use = "task.set"
prop = "priority"
value = "high"
`

func TestTaskSetWritesAProp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newTaskRig(t, map[string]string{"workflows/my-priority/workflow.toml": prioritizeWorkflow})
	r.put(t, "t-01", task.Next)
	ed := engineDeps{DB: r.db, SessionFacts: noUnits{}, WorkspaceFacts: noUnits{}}
	r.te.wireSubjects(&serveWiring{DB: r.db, Cfg: r.te.cfg, Engine: &ed})
	eng := r.te.newEngine(ed)

	if _, _, err := eng.Sweep(ctx, &sync.Mutex{}); err != nil {
		t.Fatal(err)
	}
	props, err := r.db.Props(r.te.Now).PropsOf(ctx, "t-01")
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := props.Get("priority"); !ok || v.Text != "high" {
		t.Fatalf("props %+v", props)
	}
}
