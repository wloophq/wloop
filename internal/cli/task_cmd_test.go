package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/task"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

const attachTask = task.ID("t-00000000000000a1")

func attachEnv(t *testing.T) *testEnv {
	t.Helper()
	te := newTestEnv(t, &fake.Manager{WorkspaceList: []wsdomain.Workspace{{ID: "ws-caller", Path: "/worktrees/caller", DisplayName: "caller", HostID: wsdomain.LocalHost}}})
	te.Harness.Cwd = func() string { return "/worktrees/caller" }
	te.Rec = storeRecorder{te.Store}
	te.serveWithTasks(t)
	err := te.db.Tasks().Apply(context.Background(), []task.Change{{Op: task.OpCreate, Task: task.Task{ID: attachTask, Kind: task.KindCapture, State: "next",
		Category: task.CategoryReady, Title: "Retries for the webhook sender", Origin: task.OriginUser, Created: testNow, Updated: testNow}}})
	if err != nil {
		t.Fatal(err)
	}
	return te
}

func TestAttachBindsTheCallingSessionToTheTask(t *testing.T) {
	te := attachEnv(t)
	ctx := context.Background()
	inSession(te, "term_1")
	id, err := te.Turns.Resolve(ctx, "orca", "term_1")
	if err != nil {
		t.Fatal(err)
	}
	te.Turns.Placed(ctx, id, "ws-caller", "/worktrees/caller", r7.Unit())
	if code := te.run("task", "attach", "task:"+string(attachTask)); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	u := task.UnitOf(attachTask)
	books, err := te.Turns.Store.Books(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	b := books[u]
	if len(b.Turns) != 1 || b.Turns[0].Intent != "attach" || b.Turns[0].State != dturn.Started || b.Turns[0].Session != id || b.Turns[0].Trigger != dturn.ByHand {
		t.Fatalf("turns %+v", b.Turns)
	}
	if s, ok, _ := te.reg.Registry.Session(ctx, id); !ok || s.Unit != u || s.AgentSession != "sess-1" {
		t.Fatalf("session %+v %v", s, ok)
	}
	if out := te.out.String(); !strings.Contains(out, "This session is now a run of the task") {
		t.Fatalf("out:\n%s", out)
	}
	stop, err := te.Turns.Stop(ctx, turns.StopRequest{Session: id, AgentSession: "sess-1", PromptID: "p1", LastMessage: "done"})
	if err != nil || stop.Block {
		t.Fatalf("the attached session's stop: %+v %v", stop, err)
	}
	runs, err := te.Turns.RunsOf(ctx, u)
	if err != nil || len(runs) != 1 || runs[0].Session != id || runs[0].Workspace != "ws-caller" {
		t.Fatalf("runs %+v %v", runs, err)
	}
}

func TestAttachWithoutAHandleRefusesAndChangesNothing(t *testing.T) {
	te := attachEnv(t)
	ctx := context.Background()
	if code := te.run("task", "attach", "task:"+string(attachTask)); code == 0 {
		t.Fatalf("attached with no handle: %s", te.out)
	}
	if msg := te.err.String(); !strings.Contains(msg, "task attach task:"+string(attachTask)) || !strings.Contains(msg, "workspace manager") {
		t.Fatalf("refusal:\n%s", msg)
	}
	if _, err := client.New(te.Socket).Attach(ctx, "", string(attachTask), api.AttachRequest{Cwd: "/worktrees/caller"}); err == nil {
		t.Fatal("the daemon attached a request with no session")
	}
	u := task.UnitOf(attachTask)
	books, err := te.Turns.Store.Books(ctx, u)
	if err != nil || len(books[u].Turns) != 0 {
		t.Fatalf("turns %+v %v", books[u].Turns, err)
	}
	all, err := te.reg.Registry.Sessions(ctx)
	if err != nil || len(all) != 0 {
		t.Fatalf("sessions %+v %v", all, err)
	}
}
