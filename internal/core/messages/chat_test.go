package messages_test

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/timeline"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// taskLog is an in-memory TaskLog: the event log of the tasks' own units, numbered as stored.
type taskLog struct {
	mu     sync.Mutex
	events []timeline.Event
	marks  map[unit.Unit]map[string]string
	briefs map[task.ID]string
}

func newTaskLog() *taskLog {
	return &taskLog{marks: map[unit.Unit]map[string]string{}, briefs: map[task.ID]string{}}
}

func (l *taskLog) RecordEvents(_ context.Context, evs ...port.Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, ev := range evs {
		l.events = append(l.events, timeline.Event{Seq: int64(len(l.events) + 1), Event: ev})
	}
	return nil
}

func (l *taskLog) Of(_ context.Context, id task.ID, f timeline.Filter) ([]timeline.Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []timeline.Event
	for i := len(l.events) - 1; i >= 0; i-- {
		ev := l.events[i]
		if ev.Unit != task.UnitOf(id) || (len(f.Kinds) > 0 && !slices.Contains(f.Kinds, ev.Kind)) {
			continue
		}
		if out = append(out, ev); f.Limit > 0 && len(out) == f.Limit {
			break
		}
	}
	return out, nil
}

func (l *taskLog) Marks(_ context.Context, u unit.Unit) (map[string]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[string]string{}
	for k, v := range l.marks[u] {
		out[k] = v
	}
	return out, nil
}

func (l *taskLog) SetMarks(_ context.Context, u unit.Unit, marks map[string]string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.marks[u] == nil {
		l.marks[u] = map[string]string{}
	}
	for k, v := range marks {
		l.marks[u][k] = v
	}
	return nil
}

func (l *taskLog) Brief(_ context.Context, e task.Task) (string, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.briefs[e.ID]
	return b, ok, nil
}

func (l *taskLog) chat() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, ev := range l.events {
		if ev.Kind == messages.KindChat {
			out = append(out, ev.Unit.String()+" "+ev.Actor+" "+ev.Data)
		}
	}
	return out
}

// chatRig is a rig whose messages serve the chat, with the profile's chat intent "chat".
func chatRig(t *testing.T) (*rig, *taskLog) {
	t.Helper()
	r := newRig(t)
	log := newTaskLog()
	r.svc.ChatDeps = messages.ChatDeps{Sessions: r.turns, Log: log, Units: r.taskStore, Intent: func() string { return "chat" }}
	return r, log
}

func (r *rig) capture(t *testing.T, title string, u *unit.Unit) task.Task {
	t.Helper()
	e, err := r.tasks.Capture(context.Background(), title, "", u)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestChatToALiveSessionTypesANotice(t *testing.T) {
	r, log := chatRig(t)
	e := r.capture(t, "Retry the webhook", nil)
	own := task.UnitOf(e.ID)
	r.reg.Put(turn.Session{ID: "term_1", WorkspaceID: "ws", Unit: own, StartedAt: t0.Add(-time.Hour)})
	r.reg.Put(turn.Session{ID: "term_0", WorkspaceID: "ws", Unit: own, StartedAt: t0.Add(-2 * time.Hour)})

	got, err := r.svc.Chat(context.Background(), messages.ChatRequest{Task: e.ID, Text: "  Take the timeout case first.  "})
	if err != nil {
		t.Fatal(err)
	}
	if got.Route != messages.ChatTyped || got.Session != "term_1" || got.Seq == 0 {
		t.Fatalf("chat went %+v", got)
	}
	typed := r.sess.Typed()
	if len(typed) != 1 || typed[0].Handle != "term_1" || !strings.Contains(typed[0].Line, "chat") || !strings.Contains(typed[0].Line, string(e.ID)) {
		t.Fatalf("typed %+v", typed)
	}
	if strings.Contains(typed[0].Line, "timeout") {
		t.Errorf("the notice carries the message: %q", typed[0].Line)
	}
	if want := []string{own.String() + ` person {"text":"Take the timeout case first."}`}; !slices.Equal(log.chat(), want) {
		t.Errorf("chat events %v", log.chat())
	}
	if len(r.launch.launches) != 0 {
		t.Errorf("a live session started a turn: %+v", r.launch.launches)
	}
	if _, err := r.svc.Chat(context.Background(), messages.ChatRequest{Task: e.ID, Text: " "}); err == nil {
		t.Error("an empty message was taken")
	}
}

func TestChatWithNoLiveSessionStartsAChatTurnResumingTheLast(t *testing.T) {
	r, log := chatRig(t)
	e := r.capture(t, "Retry the webhook", nil)
	own := task.UnitOf(e.ID)
	got, err := r.svc.Chat(context.Background(), messages.ChatRequest{Task: e.ID, Text: "Look at the retries."})
	if err != nil {
		t.Fatal(err)
	}
	want := turns.Launch{Unit: own, Intent: "chat", Guidance: "Look at the retries.", Trigger: turn.ByHand}
	if got.Route != messages.ChatStarted || len(r.launch.launches) != 1 || !launchIs(r.launch.launches[0], want) {
		t.Fatalf("chat went %+v, launched %+v", got, r.launch.launches)
	}

	r.books.Put(turn.Book{Unit: own, Turns: []turn.Turn{
		{Seq: 1, Intent: "start", ID: "a1", Session: "term_1", SentTo: "term_1", State: turn.Done, SentAt: t0.Add(-3 * time.Hour)},
		{Seq: 2, Intent: "act", ID: "a2", State: turn.Done, SentAt: t0.Add(-2 * time.Hour)},
	}})
	r.reg.Put(turn.Session{ID: "term_1", WorkspaceID: "ws", Unit: own, StartedAt: t0.Add(-3 * time.Hour), ClosedAt: t0.Add(-time.Hour)})
	got, err = r.svc.Chat(context.Background(), messages.ChatRequest{Task: e.ID, Text: "Now the backoff.", Intent: "pair"})
	if err != nil {
		t.Fatal(err)
	}
	want = turns.Launch{Unit: own, Intent: "pair", Guidance: "Now the backoff.", Trigger: turn.ByHand, ResumeOf: 1}
	if got.Route != messages.ChatResumed || got.ResumeOf != 1 || len(r.launch.launches) != 2 || !launchIs(r.launch.launches[1], want) {
		t.Fatalf("chat went %+v, launched %+v", got, r.launch.launches)
	}
	if len(r.sess.Typed()) != 0 {
		t.Errorf("a closed session was typed into: %+v", r.sess.Typed())
	}
	if len(log.chat()) != 2 {
		t.Errorf("chat events %v", log.chat())
	}

	r.svc.ChatDeps.Intent = func() string { return "" }
	if _, err := r.svc.Chat(context.Background(), messages.ChatRequest{Task: e.ID, Text: "And then?"}); err == nil {
		t.Error("a chat started a turn with no chat intent")
	}
}

func launchIs(got, want turns.Launch) bool {
	return got.Unit == want.Unit && got.Intent == want.Intent && got.Guidance == want.Guidance && got.Trigger == want.Trigger && got.ResumeOf == want.ResumeOf
}

func TestChatWorksOnASlackTaskTheUserDidNotCapture(t *testing.T) {
	r, log := chatRig(t)
	thread := unit.Unit{Provider: "slack", Key: "T1/C1/1700000000.000100"}
	e, err := r.tasks.Open(context.Background(), coretasks.OpenRequest{Kind: task.KindAction, Title: "Does the flag need a warning?", Unit: &thread, Origin: "slack"})
	if err != nil {
		t.Fatal(err)
	}
	own := task.UnitOf(e.ID)

	got, err := r.svc.Chat(context.Background(), messages.ChatRequest{Task: e.ID, Text: "Read the flag's code first."})
	if err != nil {
		t.Fatal(err)
	}
	want := turns.Launch{Unit: own, Intent: "chat", Guidance: "Read the flag's code first.", Trigger: turn.ByHand}
	if got.Route != messages.ChatStarted || len(r.launch.launches) != 1 || !launchIs(r.launch.launches[0], want) {
		t.Fatalf("chat went %+v, launched %+v", got, r.launch.launches)
	}
	if c := log.chat(); len(c) != 1 || !strings.HasPrefix(c[0], own.String()+" ") {
		t.Errorf("chat events %v", c)
	}

	r.reg.Put(turn.Session{ID: "term_9", WorkspaceID: "ws", Unit: thread, StartedAt: t0})
	if got, err = r.svc.Chat(context.Background(), messages.ChatRequest{Task: e.ID, Text: "And the rollout?"}); err != nil {
		t.Fatal(err)
	}
	if got.Route != messages.ChatTyped || got.Session != "term_9" {
		t.Errorf("a live session on the thread was not typed into: %+v", got)
	}
}

func TestReadTaskReadsTheChatOnce(t *testing.T) {
	r, log := chatRig(t)
	e := r.capture(t, "Retry the webhook", nil)
	own := task.UnitOf(e.ID)
	log.briefs[e.ID] = `{"text":"Retry three times."}`
	r.books.Put(turn.Book{Unit: own, Turns: []turn.Turn{{Seq: 1, Intent: "chat", ID: "c1", Session: "term_1", SentTo: "term_1", State: turn.Started, SentAt: t0}}})
	r.reg.Put(turn.Session{ID: "term_1", WorkspaceID: "ws", Unit: own, StartedAt: t0})
	for _, text := range []string{"First.", "Second."} {
		if _, err := r.svc.Chat(context.Background(), messages.ChatRequest{Task: e.ID, Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := r.svc.ReadTask(context.Background(), messages.ReadRequest{Session: "term_1"})
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, ev := range got.Chat {
		texts = append(texts, ev.Data)
	}
	if got.Task.ID != e.ID || got.Brief != `{"text":"Retry three times."}` || !slices.Equal(texts, []string{`{"text":"First."}`, `{"text":"Second."}`}) {
		t.Fatalf("read %+v", got)
	}
	again, err := r.svc.ReadTask(context.Background(), messages.ReadRequest{Session: "term_1"})
	if err != nil || len(again.Chat) != 0 {
		t.Fatalf("read again %+v %v", again.Chat, err)
	}
}
