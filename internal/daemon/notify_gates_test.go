package daemon

import (
	"context"
	"strconv"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/gate"
	dmessage "github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/task"
)

func TestAQuestionGateOnATaskOutsideTheInboxNotifies(t *testing.T) {
	t.Parallel()
	r := newNotifyRig(t)
	ctx := context.Background()
	put := func(id, state string, cat task.Category) task.Task {
		e := task.Task{ID: task.ID(id), State: task.State(state), Category: cat, Title: id, Origin: task.OriginUser, Created: catalogNow, Updated: catalogNow}
		if err := r.db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: e}}); err != nil {
			t.Fatal(err)
		}
		return e
	}
	ask := func(e task.Task, kind dmessage.Kind, seq int64) {
		m, err := r.s.deps.Messages.Store.Upsert(ctx, dmessage.Message{TaskID: string(e.ID), Unit: task.UnitOf(e.ID), Kind: kind, Text: "which window, " + string(e.ID) + "?",
			Origin: dmessage.Origin{Kind: dmessage.OriginTurn, TurnSeq: seq}, CreatedAt: catalogNow})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.s.deps.Gates.Open(ctx, gate.Gate{Kind: kind.Gate(), Task: e.ID, Unit: task.UnitOf(e.ID), Subject: strconv.FormatInt(m.ID, 10)}, nil); err != nil {
			t.Fatal(err)
		}
	}
	ask(put("e-next", "next", task.CategoryReady), dmessage.Question, 1)
	ask(put("e-wait", "waiting", task.CategoryWaiting), dmessage.AgentPost, 2)
	ask(put("e-prop", "next", task.CategoryReady), dmessage.Proposal, 3)
	put("e-quiet", "next", task.CategoryReady)

	got := r.notified(t)
	for _, id := range []string{"e-next", "e-wait", "e-prop"} {
		e, ok := got[id]
		if !ok || e.Attention.Body != "which window, "+id+"?" {
			t.Errorf("%s: %+v, want its open gate notified", id, e.Attention)
		}
	}
	if _, ok := got["e-quiet"]; ok || len(got) != 3 {
		t.Fatalf("%d notifications: %+v", len(got), got)
	}
	if a := got["e-prop"].Attention; len(a.Actions) == 0 || a.Actions[0].ID != api.TaskActionPrefix+"approve" || a.Actions[0].Title != "Approve" {
		t.Fatalf("a proposal offers %+v, want the verb that approves it, worded by the profile", a.Actions)
	}
}
