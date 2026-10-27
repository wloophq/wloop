package cli

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// closingEntries are entries a step closes, recording each close.
type closingEntries struct {
	memEntries
	closed []gtd.ID
}

func (c *closingEntries) Close(_ context.Context, id gtd.ID, to gtd.State) error {
	c.closed = append(c.closed, id)
	e := c.m[id]
	e.State = to
	c.m[id] = e
	return nil
}

func TestEntryCloseClosesACapturedEntryOnly(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	es := &closingEntries{memEntries: memEntries{m: map[gtd.ID]gtd.Entry{
		"e-1": {ID: "e-1", Kind: gtd.KindCapture, State: gtd.Next, Origin: gtd.OriginUser},
		"e-2": {ID: "e-2", Kind: gtd.KindWaiting, State: gtd.Waiting, Origin: gtd.OriginDerived},
		"e-3": {ID: "e-3", Kind: gtd.KindCapture, State: gtd.Done, Origin: gtd.OriginUser},
	}}}
	s := newStepSet(te.Env, stepDeps{Entries: es})
	st := wfStep(builtin.StepEntryClose)
	why := workflow.Why{Workflow: "entry-results-ended"}
	check := func(id gtd.ID) string {
		t.Helper()
		refusal, err := s.Check(context.Background(), st, gtd.UnitOf(id), why, automation.CheckOpts{})
		if err != nil {
			t.Fatal(err)
		}
		return refusal
	}
	if r := check("e-1"); r != "" {
		t.Fatalf("a captured entry is refused: %s", r)
	}
	for _, id := range []gtd.ID{"e-2", "e-3", "e-9"} {
		if check(id) == "" {
			t.Errorf("%s is not refused", id)
		}
	}
	if _, err := s.Run(context.Background(), st, gtd.UnitOf("e-1"), why, automation.RunOpts{}); err != nil {
		t.Fatal(err)
	}
	if len(es.closed) != 1 || es.closed[0] != "e-1" || es.m["e-1"].State != gtd.Done {
		t.Fatalf("closed %v, e-1 %s; want e-1 done", es.closed, es.m["e-1"].State)
	}
}
