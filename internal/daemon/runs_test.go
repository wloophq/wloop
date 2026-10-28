package daemon

import (
	"context"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// fakeHost records what the run actions asked of the workspace managers.
type fakeHost struct {
	mu    sync.Mutex
	calls []string
}

func (h *fakeHost) Focus(_ context.Context, manager, workspaceID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, "focus "+manager+" "+workspaceID)
	return nil
}

func (h *fakeHost) Wake(_ context.Context, u unit.Unit, intent string, sess turn.Session) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, "wake "+u.String()+" "+intent+" "+sess.ID+" "+sess.AgentSession)
	return nil
}

func (h *fakeHost) seen() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.calls)
}

type resumeWith struct{}

func (resumeWith) Resume(home, agentSession string) string {
	return "claude --resume " + agentSession + " (" + home + ")"
}

type runsRig struct {
	*tasksRig
	svc  *turns.Service
	host *fakeHost
	u    unit.Unit
}

const runTask = task.ID("t-00000000000000a1")

func newRunsRig(t *testing.T) *runsRig {
	t.Helper()
	r := &runsRig{tasksRig: newTasksRig(t, nil), host: &fakeHost{}, u: task.UnitOf(runTask)}
	repo := sqlite.New(r.db)
	r.svc = &turns.Service{Intents: turnstest.Intents(), Store: repo, Registry: repo, Sessions: turnstest.NewSessions(), Clock: fixedClock(tasksNow),
		AgentFor: func(unit.Unit) turns.Resumer { return resumeWith{} }, FastJob: 2 * time.Minute}
	r.s.deps.Turns, r.s.deps.RunHost = r.svc, r.host
	r.s.deps.Transcripts = &turnstest.Transcripts{Lines: map[string][]turns.TranscriptLine{
		"ag-1": {{Role: "user", Text: "add retries"}, {Role: "assistant", Text: "on it"}},
	}}
	r.put(t, task.Task{ID: runTask, State: "next", Category: task.CategoryReady, Title: "Retries"})
	return r
}

// session stores a session of the task in ws-1 of orca, live on handle when it is not "".
func (r *runsRig) session(t *testing.T, id, handle, agentSession string) {
	t.Helper()
	r.sessionAt(t, id, handle, agentSession, tasksNow.Add(-time.Hour))
}

func (r *runsRig) sessionAt(t *testing.T, id, handle, agentSession string, started time.Time) {
	t.Helper()
	ctx := context.Background()
	repo := sqlite.New(r.db)
	if err := repo.SaveSession(ctx, turn.Session{ID: id, Manager: "orca", WorkspaceID: "ws-1", Worktree: "/src/api-retries", Home: "/home/ana/.claude",
		AgentSession: agentSession, StartedAt: started, Unit: r.u}); err != nil {
		t.Fatal(err)
	}
	if handle != "" {
		if err := r.svc.Bind(ctx, id, "orca", handle, ""); err != nil {
			t.Fatal(err)
		}
	}
}

func (r *runsRig) start(t *testing.T, id, session string) turn.Turn {
	t.Helper()
	got, err := r.svc.StartByHand(context.Background(), turns.SendRequest{Unit: r.u, Intent: "start", Turn: id, Trigger: turn.ByHand, Records: true}, session)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestRunsShowManagerWorkspaceAndSession(t *testing.T) {
	r := newRunsRig(t)
	r.session(t, "s1", "term_1", "ag-1")
	first := r.start(t, "turn-1", "s1")
	var runs []api.RunView
	if code := get(t, r.s, api.TaskPath(string(runTask), "runs"), &runs); code != 200 {
		t.Fatalf("GET runs: %d", code)
	}
	want := api.RunView{Seq: first.Seq, Intent: "start", State: "started", Manager: "orca", Workspace: "ws-1", Worktree: "/src/api-retries",
		Session: "s1", SessionState: "active", Resume: "claude --resume ag-1 (/home/ana/.claude)", Actions: []string{"open", "enter", "transcript"}}
	if len(runs) != 1 || !runEqual(runs[0], want) {
		t.Fatalf("runs %+v, want %+v", runs, want)
	}
	var v api.TaskView
	if code := get(t, r.s, api.TaskPath(string(runTask)), &v); code != 200 || len(v.Runs) != 1 || !runEqual(v.Runs[0], want) {
		t.Fatalf("task detail %d runs %+v", code, v.Runs)
	}
	var page api.TranscriptPage
	path := "/api/runs/" + itoa(first.Seq) + "/transcript?from=1&limit=5"
	if code := get(t, r.s, path, &page); code != 200 || page.Total != 2 || len(page.Lines) != 1 || page.Lines[0] != (api.TranscriptLine{Role: "assistant", Text: "on it"}) {
		t.Fatalf("transcript %d %+v", code, page)
	}
	if code := get(t, r.s, "/api/runs/999/transcript", nil); code != 404 {
		t.Fatalf("an unknown run's transcript: %d", code)
	}
}

func runEqual(a, b api.RunView) bool { return reflect.DeepEqual(a, b) }

func TestOpenFocusesTheRunsWorkspace(t *testing.T) {
	r := newRunsRig(t)
	r.session(t, "s1", "term_1", "ag-1")
	run := r.start(t, "turn-1", "s1")
	if rec := post(t, r.s, "/api/runs/"+itoa(run.Seq)+"/open", `{"source":"cockpit"}`); rec.Code != 200 {
		t.Fatalf("open: %d %s", rec.Code, rec.Body)
	}
	if got := r.host.seen(); !slices.Equal(got, []string{"focus orca ws-1"}) {
		t.Fatalf("host calls %v", got)
	}
	headless, err := r.svc.Send(context.Background(), turns.SendRequest{Unit: r.u, Intent: "act", Turn: "turn-2", Trigger: turn.ByHand, Records: true})
	if err != nil {
		t.Fatal(err)
	}
	if rec := post(t, r.s, "/api/runs/"+itoa(headless.Seq)+"/open", `{}`); rec.Code != 409 {
		t.Fatalf("open a run with no workspace: %d %s", rec.Code, rec.Body)
	}
	if got := r.host.seen(); len(got) != 1 {
		t.Fatalf("host calls %v", got)
	}
}

func TestEnterWakesADormantSession(t *testing.T) {
	r := newRunsRig(t)
	r.session(t, "s1", "term_1", "ag-1")
	live := r.start(t, "turn-1", "s1")
	if rec := post(t, r.s, "/api/runs/"+itoa(live.Seq)+"/enter", `{}`); rec.Code != 200 {
		t.Fatalf("enter a live session: %d %s", rec.Code, rec.Body)
	}
	if got := r.host.seen(); !slices.Equal(got, []string{"focus orca ws-1"}) {
		t.Fatalf("a live session: host calls %v", got)
	}
	r.session(t, "s2", "", "ag-2")
	dormant := r.start(t, "turn-2", "s2")
	r.sessionAt(t, "s3", "", "ag-3", tasksNow.Add(-time.Minute))
	if rec := post(t, r.s, "/api/runs/"+itoa(dormant.Seq)+"/enter", `{}`); rec.Code != 200 {
		t.Fatalf("enter a dormant session: %d %s", rec.Code, rec.Body)
	}
	want := []string{"focus orca ws-1", "wake " + r.u.String() + " start s2 ag-2", "focus orca ws-1"}
	if got := r.host.seen(); !slices.Equal(got, want) {
		t.Fatalf("a dormant session: host calls %v, want %v", got, want)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
