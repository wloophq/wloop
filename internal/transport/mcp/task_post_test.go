package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/core/messages/messagestest"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var (
	inboxAt   = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	inboxUnit = unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
)

type fixedClock struct{}

func (fixedClock) Now() time.Time { return inboxAt }

type enabledFacts struct{}

func (enabledFacts) UnitFacts(_ context.Context, u unit.Unit) (task.UnitFacts, bool, error) {
	return task.UnitFacts{Unit: u}, true, nil
}

type noItems struct{}

func (noItems) Items(context.Context, unit.Unit) ([]items.Item, error) { return nil, nil }

type inboxRig struct {
	srv       *Server
	sid       string
	books     *turnstest.Store
	msgs      *messagestest.Store
	taskStore *taskstest.Store
	tasks     *coretasks.Service
}

// inboxSession serves task_post over the messages service, with a started turn on term_1.
func inboxSession(t *testing.T, handle string) *inboxRig {
	t.Helper()
	r := &inboxRig{books: turnstest.NewStore(), msgs: messagestest.New(), taskStore: taskstest.New()}
	r.books.Put(turn.Book{Unit: inboxUnit, Turns: []turn.Turn{{Seq: 1, Intent: turn.Intent(dintent.Review), ID: "abc", SentTo: "term_1",
		Session: "term_1", State: turn.Started, SentAt: inboxAt.Add(-time.Hour), StartedAt: inboxAt.Add(-time.Minute)}}})
	r.tasks = coretasks.New(coretasks.Deps{States: taskstest.BundledStates, Store: r.taskStore, Items: noItems{}, Facts: enabledFacts{}, Clock: fixedClock{}})
	svc := messagestest.NewGates(fixedClock{}.Now, rigTasks{r.taskStore, r.tasks}).Bind(&messages.Service{Store: r.msgs, Books: r.books, Tasks: r.tasks,
		Intents: turnstest.Intents(), Clock: fixedClock{}})
	r.srv = &Server{Texts: bundledTexts, Turns: &fakeTurns{}, Port: 47800, Tasks: svc}
	r.sid = call(t, r.srv, "", handle, initialize).Header().Get("Mcp-Session-Id")
	if r.sid == "" {
		t.Fatal("no session id")
	}
	return r
}

func postCall(args string) string {
	return `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"task_post","arguments":` + args + `}}`
}

func (r *inboxRig) post(t *testing.T, handle, args string) toolResult {
	t.Helper()
	return decodeResult(t, call(t, r.srv, r.sid, handle, postCall(args)))
}

func (r *inboxRig) open(t *testing.T) []message.Message {
	t.Helper()
	open, err := r.msgs.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return open
}

func TestTaskPostListedOnlyWhenWired(t *testing.T) {
	list := `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	r := inboxSession(t, "term_1")
	if names := listedTools(t, call(t, r.srv, r.sid, "term_1", list)); !slices.Contains(names, "task_post") {
		t.Fatalf("tools %v", names)
	}
	bare := &Server{Texts: bundledTexts, Turns: &fakeTurns{}, Port: 47800}
	sid := call(t, bare, "", "term_1", initialize).Header().Get("Mcp-Session-Id")
	if names := listedTools(t, call(t, bare, sid, "term_1", list)); slices.Contains(names, "task_post") {
		t.Fatalf("listed without the tasks: %v", names)
	}
}

func TestTaskPostCreatesAMessage(t *testing.T) {
	r := inboxSession(t, "term_1")
	got := r.post(t, "term_1", `{"text":"Keep the old column?","options":[{"id":"keep","label":"Keep","consequence":"no migration"},{"id":"drop","label":"Drop","consequence":"migrates"}],"recommend":"keep"}`)
	open := r.open(t)
	if got.Result.IsError || len(open) != 1 {
		t.Fatalf("%+v; open %+v", got.Result, open)
	}
	m := open[0]
	g, _, err := r.srv.Tasks.GateOf(context.Background(), m)
	if err != nil || g.ID == 0 {
		t.Fatalf("message %d has no gate: %v", m.ID, err)
	}
	want := fmt.Sprintf("Posted on the task, waiting on gate %[1]d. When the user answers it, %[2]s tells this session that gate %[1]d was answered; read the answer with task_read.", g.ID, brand.Name)
	if got.Result.Content[0].Text != want {
		t.Fatalf("result %q", got.Result.Content[0].Text)
	}
	if m.Kind != message.AgentPost || m.Unit != inboxUnit || len(m.Options) != 2 || m.Recommend != "keep" ||
		m.Origin != (message.Origin{Kind: message.OriginAgent, TurnSeq: 1, Session: "term_1"}) {
		t.Fatalf("message %+v", m)
	}
	e, err := r.taskStore.Task(context.Background(), task.ID(m.TaskID))
	if err != nil || e.Origin == task.OriginMessage || e.State != task.State("inbox") {
		t.Fatalf("entry %+v %v", e, err)
	}
	books, _ := r.books.Books(context.Background(), inboxUnit)
	if st := books[inboxUnit].Turns[0].State; st != turn.Started {
		t.Fatalf("the turn is %s", st)
	}
}

func TestTaskPostNeedsAHandle(t *testing.T) {
	for _, h := range []string{"", unexpanded} {
		r := inboxSession(t, h)
		got := r.post(t, h, `{"text":"hello"}`)
		if !got.Result.IsError || !strings.Contains(got.Result.Content[0].Text, api.SessionHeader) {
			t.Fatalf("%q: %+v", h, got.Result)
		}
		if open := r.open(t); len(open) != 0 {
			t.Fatalf("%q: stored %+v", h, open)
		}
	}
	r := inboxSession(t, "term_9")
	if got := r.post(t, "term_9", `{"text":"hello"}`); !got.Result.IsError || !strings.Contains(got.Result.Content[0].Text, "no started turn") {
		t.Fatalf("a session with no turn: %+v", got.Result)
	}
}

func TestTaskPostCannotMoveTasks(t *testing.T) {
	r := inboxSession(t, "term_1")
	r.post(t, "term_1", `{"text":"first"}`)
	ctx := context.Background()
	capture, err := r.tasks.Capture(ctx, "call Ana", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := func() []task.Task {
		all, err := r.taskStore.Tasks(ctx, task.Query{All: true})
		if err != nil {
			t.Fatal(err)
		}
		return all
	}
	before := snapshot()
	for _, key := range []string{"state", "entry", "move", "list"} {
		args := fmt.Sprintf(`{"text":"move it","%s":%q}`, key, string(capture.ID))
		got := r.post(t, "term_1", args)
		if !got.Result.IsError || !strings.Contains(got.Result.Content[0].Text, "cannot move") {
			t.Fatalf("%s: %+v", key, got.Result)
		}
	}
	if after := snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatalf("entries changed:\n%+v\n%+v", before, after)
	}
	if n := len(r.open(t)); n != 1 {
		t.Fatalf("open %d", n)
	}
	var schema struct {
		AdditionalProperties *bool `json:"additionalProperties"`
	}
	raw, _ := json.Marshal(taskPostTool().InputSchema)
	if err := json.Unmarshal(raw, &schema); err != nil || schema.AdditionalProperties == nil || *schema.AdditionalProperties {
		t.Fatalf("the schema is not closed: %s", raw)
	}
}

func TestTaskPostIsCapped(t *testing.T) {
	r := inboxSession(t, "term_1")
	for i := range 3 {
		if got := r.post(t, "term_1", fmt.Sprintf(`{"text":"note %d"}`, i)); got.Result.IsError {
			t.Fatalf("post %d: %+v", i, got.Result)
		}
	}
	got := r.post(t, "term_1", `{"text":"one more"}`)
	if !got.Result.IsError || !strings.Contains(got.Result.Content[0].Text, "3 open posts") {
		t.Fatalf("%+v", got.Result)
	}
	if n := len(r.open(t)); n != 3 {
		t.Fatalf("open %d", n)
	}
}

func TestTaskPostRefusesABadMessage(t *testing.T) {
	r := inboxSession(t, "term_1")
	for _, args := range []string{`{}`, `{"text":"  "}`, `{"text":"pick","options":[{"id":"A b","label":"x","consequence":"y"},{"id":"b","label":"x","consequence":"y"}]}`, `[1]`} {
		if got := r.post(t, "term_1", args); !got.Result.IsError || !strings.HasPrefix(got.Result.Content[0].Text, "Nothing was posted") {
			t.Fatalf("%s: %+v", args, got.Result)
		}
	}
	if n := len(r.open(t)); n != 0 {
		t.Fatalf("open %d", n)
	}
}

// rigTasks give a gate the task of its unit from the store, and open one through the tasks service.
type rigTasks struct {
	store *taskstest.Store
	svc   *coretasks.Service
}

func (r rigTasks) TasksOf(ctx context.Context, u unit.Unit) ([]task.Task, error) {
	return r.store.TasksOf(ctx, u)
}

func (r rigTasks) Open(ctx context.Context, req coretasks.OpenRequest) (task.Task, error) {
	return r.svc.Open(ctx, req)
}
