package brief

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
)

func TestAPRTaskTakesItsPRsBriefPropertiesWhenItStartsHoldingIt(t *testing.T) {
	r := newRig(t, 1)
	ctx := context.Background()
	u := github.Ref{Owner: "acme", Repo: "api", Number: 1}.Unit()
	data := []byte(`{"goal":"Export the ledger","kind":"feature","size":"m","risk":"high","open_questions":[],"tags":[]}`)
	if err := r.db.PutRecord(ctx, u, "brief", task.WriterAgent, data, t0); err != nil {
		t.Fatal(err)
	}
	fresh, edited := task.ID("t-00000000000000f1"), task.ID("t-00000000000000f2")
	for _, id := range []task.ID{fresh, edited} {
		if err := r.db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: task.Task{ID: id, State: "next", Category: task.CategoryReady,
			Title: "Review acme/api#1", Origin: task.OriginDerived, Units: []unit.Unit{u}, Created: t0, Updated: t0}}}); err != nil {
			t.Fatal(err)
		}
	}
	props := r.db.Props(func() time.Time { return t0 })
	if err := props.SetProp(ctx, edited, "risk", task.Value{Text: "low"}, task.WriterPerson); err != nil {
		t.Fatal(err)
	}
	recs := &Records{Holders: r.db, Props: props, Projection: bundledProjection}
	for range 2 {
		if err := recs.ProjectUnheld(ctx, u, data, task.WriterAgent); err != nil {
			t.Fatal(err)
		}
	}
	got, err := props.PropsOf(ctx, fresh)
	if err != nil || got["risk"].Text != "high" || got["effort"].Text != "m" {
		t.Fatalf("the task that started holding the pull request %+v %v", got, err)
	}
	if got, err := props.PropsOf(ctx, edited); err != nil || got["risk"].Text != "low" || got["effort"].Text != "" {
		t.Fatalf("a task holding a property already %+v %v", got, err)
	}
}
