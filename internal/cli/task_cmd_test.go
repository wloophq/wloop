package cli

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/core/timeline"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
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

func TestTwoSessionsAttachedToATaskAreTwoRuns(t *testing.T) {
	te := attachEnv(t)
	ctx := context.Background()
	for _, h := range []string{"term_1", "term_2", "term_1"} {
		inSession(te, h)
		if code := te.run("task", "attach", "task:"+string(attachTask)); code != 0 {
			t.Fatalf("%s: exit %d: %s", h, code, te.err)
		}
	}
	runs, err := te.Turns.RunsOf(ctx, task.UnitOf(attachTask))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range runs {
		got = append(got, string(r.Turn.State)+" "+r.Session)
	}
	want := []string{"started " + te.sessionOf(t, "orca", "term_1"), "started " + te.sessionOf(t, "orca", "term_2")}
	if !slices.Equal(got, want) {
		t.Fatalf("runs %v, want %v", got, want)
	}
}

func TestAChatReachesTheTasksLiveSessionAndItsTaskRead(t *testing.T) {
	te := attachEnv(t)
	ctx := context.Background()
	inSession(te, "term_1")
	if code := te.run("task", "attach", "task:"+string(attachTask)); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	id := te.sessionOf(t, "orca", "term_1")
	sessions := te.Turns.Sessions.(*turnstest.Sessions)
	sent := len(sessions.Typed())
	res, err := client.New(te.Socket).Chat(ctx, string(attachTask), api.ChatRequest{Text: "Take the timeout case first."})
	if err != nil || len(res.Lines) != 1 || !strings.Contains(res.Lines[0], "live session") {
		t.Fatalf("chat %+v %v", res, err)
	}
	typed := sessions.Typed()[sent:]
	if len(typed) != 1 || typed[0].Handle != id || !strings.Contains(typed[0].Line, "turn answer task:"+string(attachTask)) || !strings.Contains(typed[0].Line, string(attachTask)) ||
		strings.Contains(typed[0].Line, "timeout") {
		t.Fatalf("typed %+v", typed)
	}
	got, err := te.messages.ReadTask(ctx, messages.ReadRequest{Session: id})
	if err != nil || got.Task.ID != attachTask || len(got.Chat) != 1 || !strings.Contains(got.Chat[0].Data, "Take the timeout case first.") ||
		got.Chat[0].Actor != string(task.ActorPerson) {
		t.Fatalf("read %+v %v", got, err)
	}
	if again, err := te.messages.ReadTask(ctx, messages.ReadRequest{Session: id}); err != nil || len(again.Chat) != 0 {
		t.Fatalf("read again %+v %v", again.Chat, err)
	}
	if _, err := client.New(te.Socket).Chat(ctx, string(attachTask), api.ChatRequest{Text: "Then the backoff."}); err != nil {
		t.Fatal(err)
	}
	te.out.Reset()
	if code := te.run("turn", "answer", "task:"+string(attachTask)); code != 0 || !strings.Contains(te.out.String(), "Then the backoff.") ||
		strings.Contains(te.out.String(), "timeout") {
		t.Fatalf("turn answer: exit %d\n%s%s", code, te.out, te.err)
	}
	evs, err := timeline.Merged{Log: te.db, Holdings: te.db}.Of(ctx, attachTask, timeline.Filter{Kinds: []string{messages.KindChat}})
	if err != nil || len(evs) != 2 {
		t.Fatalf("timeline %+v %v", evs, err)
	}
	sess, _, err := te.Turns.Registry.Session(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	sessions.SetAgents(turns.AgentObservation{WorkspaceID: sess.WorkspaceID, Pane: sess.Pane, State: dturn.AgentWorking})
	sent = len(sessions.Typed())
	res, err = client.New(te.Socket).Chat(ctx, string(attachTask), api.ChatRequest{Text: "And the jitter."})
	if err != nil || len(res.Lines) != 1 || !strings.Contains(res.Lines[0], "busy") || len(sessions.Typed()) != sent {
		t.Fatalf("chat to a busy agent %+v %v, typed %+v", res, err, sessions.Typed()[sent:])
	}
	if _, err := client.New(te.Socket).Chat(ctx, string(attachTask), api.ChatRequest{Text: " "}); err == nil {
		t.Fatal("an empty message was taken")
	}
}

func TestAChatWithNoLiveSessionStartsAChatTurnOnTheTask(t *testing.T) {
	te := attachEnv(t)
	ctx := context.Background()
	res, err := client.New(te.Socket).Chat(ctx, string(attachTask), api.ChatRequest{Text: "What is left before we ship?"})
	if err != nil || len(res.Lines) != 1 || !strings.Contains(res.Lines[0], "chat turn starts") {
		t.Fatalf("chat %+v %v", res, err)
	}
	u := task.UnitOf(attachTask)
	books, err := te.Turns.Store.Books(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	b := books[u]
	if len(b.Turns) != 1 || b.Turns[0].Intent != "chat" || b.Turns[0].Guidance != "What is left before we ship?" || b.Turns[0].Trigger != dturn.ByHand {
		t.Fatalf("turns %+v", b.Turns)
	}
}
