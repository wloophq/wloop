package catalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/internal/turn"
)

func TestOwedByIsTheProfilesExpression(t *testing.T) {
	ctx := context.Background()
	l := bundled(t)
	owed := OwedByOf(func() *intentload.Loaded { return l })
	for name, c := range map[string]struct {
		f    workflow.Facts
		want string
	}{
		"an open gate":             {workflow.Facts{"task.category": "waiting", "task.gates_open": 1, "task.link_owes": []string{"slack:U0ANA"}}, "me"},
		"an item that waits on me": {workflow.Facts{"task.category": "inbox", "task.link_owes": []string{"slack:U0ANA", "me"}}, "me"},
		"the items' owers":         {workflow.Facts{"task.category": "waiting", "task.link_owes": []string{"slack:U0ANA", "slack:U0BRUNO"}}, "slack:U0ANA"},
		"the owner":                {workflow.Facts{"task.category": "waiting", "task.link_owes": []string{}, "task.prop.owner": "slack:U0BRUNO"}, "slack:U0BRUNO"},
		"the owner a person handed it to, over an item that waits on me": {workflow.Facts{"task.category": "waiting", "task.link_owes": []string{"me"},
			"task.prop.owner": "slack:U0BRUNO", "task.prop_by.owner": "person"}, "slack:U0BRUNO"},
		"an owner no person set, under an item that waits on me": {workflow.Facts{"task.category": "waiting", "task.link_owes": []string{"me"},
			"task.prop.owner": "slack:U0BRUNO", "task.prop_by.owner": "plugin"}, "me"},
		"nobody named":            {workflow.Facts{"task.category": "ready", "task.link_owes": []string{}}, "me"},
		"a done task":             {workflow.Facts{"task.category": "done", "task.link_owes": []string{"me"}}, ""},
		"a done task's open gate": {workflow.Facts{"task.category": "done", "task.gates_open": 1}, "me"},
	} {
		if got, err := owed(ctx, c.f); err != nil || got != c.want {
			t.Errorf("%s: owed by %q, %v; want %q", name, got, err, c.want)
		}
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, intentload.TasksFile), []byte("[owed_by]\nexpr = 'task.prop.owner'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := turn.Base()
	if err != nil {
		t.Fatal(err)
	}
	mine, problems := intentload.Load(append(profile.BundledChain(), profile.Layer{Name: "user", Source: dir, Dir: dir}), turn.BundledFS(), base, turn.CheckTemplate)
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	byOwner := OwedByOf(func() *intentload.Loaded { return mine })
	if got, err := byOwner(ctx, workflow.Facts{"task.gates_open": 3, "task.prop.owner": "slack:U0CAIO"}); err != nil || got != "slack:U0CAIO" {
		t.Errorf("a profile's own owed_by: %q, %v", got, err)
	}
	if _, err := expr.CompileString(`task.owed_by`, intentload.OwedByEnv(l.Tasks)); err == nil {
		t.Error("owed_by.expr may read the fact it makes")
	}
	if _, err := expr.CompileString(`task.gates_open > 0`, intentload.OwedByEnv(l.Tasks)); err == nil {
		t.Error("an owed_by.expr that is no string loads")
	}
}

func TestOfferFactsReadTheKindProperty(t *testing.T) {
	e := task.Task{ID: "t-1", State: "inbox"}
	e.SetProp(task.PropKind, task.Value{Text: "capture"}, task.WriterWorkflow, e.Created)
	f := OfferFacts(e)
	if f["task.prop.kind"] != "capture" {
		t.Fatalf("offer facts %v, want task.prop.kind capture", f)
	}
	if _, ok := f["task.kind"]; ok {
		t.Error("the offer facts still hold task.kind")
	}
	offer, err := expr.CompileWorkflow(`task.prop.kind != "capture"`, builtin.OfferEnv())
	if err != nil {
		t.Fatal(err)
	}
	for kind, want := range map[string]bool{"capture": false, "thread": true, "": true} {
		e.SetProp(task.PropKind, task.Value{Text: kind}, task.WriterWorkflow, e.Created)
		if got, err := offer.Match(workflow.Vars{Facts: OfferFacts(e)}); err != nil || got != want {
			t.Errorf("kind %q: offered %v, %v; want %v", kind, got, err, want)
		}
	}
}
