package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/domain/gate"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/items"
	dmessage "github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type enabledUnits struct{ off map[unit.ProviderID]bool }

func (e enabledUnits) UnitFacts(_ context.Context, u unit.Unit) (task.UnitFacts, bool, error) {
	return task.UnitFacts{Unit: u}, !e.off[u.Provider], nil
}

type itemsNone struct{}

func (itemsNone) Items(context.Context, unit.Unit) ([]items.Item, error) { return nil, nil }

type messageRig struct{ *turnRig }

var r8u = pr.Ref{Owner: "acme", Repo: "api", Number: 8}

// newMessageRig serves the messages over a turn rig, with the tasks' verbs registered.
func newMessageRig(t *testing.T) *messageRig {
	t.Helper()
	r := &messageRig{turnRig: newTurnRig(t)}
	r.s.registerMessages()
	return r
}

// question asks publishQuestion on r8 and returns its message.
func (r *messageRig) question(t *testing.T) dmessage.Message {
	t.Helper()
	r.asked(r8u, turn.Intent(dintent.Review), turn.Asked, publishQuestion)
	if err := r.s.deps.Messages.Reconcile(context.Background(), r8u.Unit()); err != nil {
		t.Fatal(err)
	}
	open, _ := r.msgs.Open(context.Background())
	if len(open) != 1 {
		t.Fatalf("open %+v", open)
	}
	return open[0]
}

func (r *messageRig) idle() {
	now := r.s.deps.Now()
	r.reg.Put(turn.Session{ID: "term-1", Pane: "tab-1:leaf-1", WorkspaceID: "ws-1", StartedAt: now.Add(-time.Hour), MCPAt: now.Add(-time.Hour)})
	r.sessions.Put("ws-1", turns.Terminal{Session: "term-1", Key: "tab-1", LastOutput: now.Add(-time.Minute)})
}

func (r *messageRig) busy() {
	r.idle()
	r.sessions.SetAgents(turns.AgentObservation{WorkspaceID: "ws-1", Pane: "tab-1:leaf-1", TerminalKey: "tab-1", State: turn.AgentWorking})
}

// outcome is what an answer leaves behind, for comparing the paths that give it.
type outcome struct {
	Code     int
	Lines    []string
	Answers  []turn.Answer
	Events   []port.Event
	Typed    []turnstest.Typed
	Launches []turns.Launch
}

func (r *messageRig) outcome(code int, lines []string) outcome {
	books, _ := r.store.Books(context.Background(), r8u.Unit())
	return outcome{Code: code, Lines: lines, Answers: books[r8u.Unit()].Answers, Events: r.store.Events(), Typed: r.sessions.Typed(), Launches: r.launcher.launches}
}

func actionLines(t *testing.T, body []byte) []string {
	t.Helper()
	var res api.ActionResult
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return res.Output
}

func taskLines(t *testing.T, body []byte) []string {
	t.Helper()
	var res api.TaskResult
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return res.Lines
}

// gatePath is the route that resolves gate id with intent.
func gatePath(id int64, intent string) string {
	return strings.NewReplacer("{id}", strconv.FormatInt(id, 10), "{intent}", intent).Replace(api.GatePath)
}

// gateOf is m's open gate.
func (r *messageRig) gateOf(t *testing.T, m dmessage.Message) gate.Gate {
	t.Helper()
	open, err := r.s.deps.GateReader.Where(context.Background(), gates.Query{Kind: m.Kind.Gate(), Unit: m.Unit})
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(open, func(g gate.Gate) bool { return g.Subject == strconv.FormatInt(m.ID, 10) })
	if i < 0 {
		t.Fatalf("message %d has no open gate in %+v", m.ID, open)
	}
	return open[i]
}

func TestMessageAnswerDifferential(t *testing.T) {
	t.Parallel()
	sessions := map[string]func(*messageRig){"live idle": (*messageRig).idle, "busy": (*messageRig).busy, "gone": func(*messageRig) {}}
	routes := map[string]turn.Route{"live idle": turn.Typed, "busy": turn.Queued, "gone": turn.Resumed}
	for name, arrange := range sessions {
		for _, source := range []string{"cockpit", "notification", "tui"} {
			t.Run(name+"/"+source, func(t *testing.T) {
				t.Parallel()
				body := `{"option_id":"publish","guidance":"say thanks","source":"` + source + `"}`
				byUnit := func(t *testing.T) outcome {
					r := newMessageRig(t)
					r.question(t)
					arrange(r)
					rec := post(t, r.s, "/api/units/github/acme%2Fapi%238/answer", body)
					return r.outcome(rec.Code, actionLines(t, rec.Body.Bytes()))
				}
				byTask := func(t *testing.T) outcome {
					r := newMessageRig(t)
					m := r.question(t)
					arrange(r)
					rec := post(t, r.s, api.TaskPath(m.TaskID, "answer"), `{"args":{"option_id":"publish","guidance":"say thanks"},"source":"`+source+`"}`)
					return r.outcome(rec.Code, taskLines(t, rec.Body.Bytes()))
				}
				want := byUnit(t)
				if want.Code != 200 || len(want.Answers) != 1 || want.Answers[0].Source != turn.Source(source) || want.Answers[0].Route != routes[name] ||
					want.Answers[0].Gate == 0 {
					t.Fatalf("unit path %+v", want)
				}
				if got := byTask(t); !reflect.DeepEqual(got, want) {
					t.Errorf("task path\n got %+v\nwant %+v", got, want)
				}
				if source != "cockpit" {
					return
				}
				r := newMessageRig(t)
				m := r.question(t)
				arrange(r)
				rec := post(t, r.s, gatePath(r.gateOf(t, m).ID, "answer"), `{"args":{"answer":"publish\nsay thanks"}}`)
				if got := r.outcome(rec.Code, want.Lines); !reflect.DeepEqual(got, want) {
					t.Errorf("gate path %s\n got %+v\nwant %+v", rec.Body, got, want)
				}
			})
		}
	}
}

func TestMessageAnswerRefusesAnUnknownOption(t *testing.T) {
	t.Parallel()
	ref := newMessageRig(t)
	ref.question(t)
	want := post(t, ref.s, "/api/units/github/acme%2Fapi%238/answer", `{"option_id":"nope"}`)
	r := newMessageRig(t)
	m := r.question(t)
	got := post(t, r.s, api.TaskPath(m.TaskID, "answer"), `{"args":{"option_id":"nope"}}`)
	if got.Code != 400 || want.Code != 400 || got.Body.String() != want.Body.String() {
		t.Fatalf("task %d %s; unit %d %s", got.Code, got.Body, want.Code, want.Body)
	}
	if open, _ := r.msgs.Open(context.Background()); len(open) != 1 || r.gateOf(t, open[0]).Status != gate.Open {
		t.Fatalf("open %+v", open)
	}
}

func TestDismissingAQuestionGateThroughTheDaemon(t *testing.T) {
	t.Parallel()
	for _, via := range []string{"unit", "gate"} {
		t.Run(via, func(t *testing.T) {
			t.Parallel()
			r := newMessageRig(t)
			m := r.question(t)
			path := "/api/units/github/acme%2Fapi%238/dismiss"
			if via == "gate" {
				path = gatePath(r.gateOf(t, m).ID, "dismiss")
			}
			if rec := post(t, r.s, path, ``); rec.Code != 200 {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
			books, _ := r.store.Books(context.Background(), r8u.Unit())
			if st := books[r8u.Unit()].Turns[0].State; st != turn.Dismissed {
				t.Fatalf("turn %s", st)
			}
			if got, _, _ := r.msgs.Message(context.Background(), m.ID); got.Open() {
				t.Fatalf("message %+v", got)
			}
		})
	}
}

func TestATaskServesItsQuestionGateWithItsMessage(t *testing.T) {
	t.Parallel()
	r := newMessageRig(t)
	m := r.question(t)
	var v api.TaskView
	if code := get(t, r.s, api.TaskPath(m.TaskID), &v); code != 200 {
		t.Fatalf("%d", code)
	}
	got, g, ok := v.MessageOf()
	if !ok || got.ID != m.ID || got.Text != "Publish?" || len(got.Options) != 2 || g.Kind != "question" || g.Text != "The agent asks" {
		t.Fatalf("%+v %+v %v", got, g, ok)
	}
}

func TestMessageAnswerKeepsItsSource(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"tui", "notification"} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			r := newMessageRig(t)
			m := r.question(t)
			if rec := post(t, r.s, api.TaskPath(m.TaskID, "answer"), `{"args":{"option_id":"hold"},"source":"`+source+`"}`); rec.Code != 200 {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
			books, _ := r.store.Books(context.Background(), r8u.Unit())
			if a := books[r8u.Unit()].Answers; len(a) != 1 || a[0].Source != turn.Source(source) {
				t.Fatalf("answers %+v", a)
			}
		})
	}
	r := newMessageRig(t)
	m := r.question(t)
	if rec := post(t, r.s, api.TaskPath(m.TaskID, "answer"), `{"args":{"option_id":"hold"},"source":"bogus"}`); rec.Code != 400 {
		t.Fatalf("a bogus source: %d %s", rec.Code, rec.Body)
	}
}

func TestMessageOfADisabledPluginAnswers409(t *testing.T) {
	t.Parallel()
	r := newMessageRig(t)
	m := r.question(t)
	r.s.deps.PluginEnabled = func(p unit.ProviderID) bool { return p != unit.GitHub }
	if rec := post(t, r.s, api.TaskPath(m.TaskID, "answer"), `{"args":{"option_id":"hold"}}`); rec.Code != 409 || rec.Body.String() != `{"error":"github is disabled","plugin":"github"}`+"\n" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if books, _ := r.store.Books(context.Background(), r8u.Unit()); len(books[r8u.Unit()].Answers) != 0 {
		t.Fatal("an answer was stored")
	}
	if r.gateOf(t, m).Status != gate.Open {
		t.Fatal("the gate closed")
	}
}

func TestApproveAnswersAConfirmWithYesAndApprovesAProposal(t *testing.T) {
	t.Parallel()
	r := newMessageRig(t)
	ctx := context.Background()
	r.asked(r8u, turn.Intent(dintent.Review), turn.Asked, &record.Question{Kind: "confirm", Text: "Publish?"})
	if err := r.s.deps.Messages.Reconcile(ctx, r8u.Unit()); err != nil {
		t.Fatal(err)
	}
	open, _ := r.msgs.Open(ctx)
	if rec := post(t, r.s, api.TaskPath(open[0].TaskID, "approve"), `{"source":"tui"}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	books, _ := r.store.Books(ctx, r8u.Unit())
	if a := books[r8u.Unit()].Answers; len(a) != 1 || !a[0].Yes || a[0].Source != turn.TUI {
		t.Fatalf("answers %+v", a)
	}
	m, err := r.s.deps.Messages.Propose(ctx, messages.ProposeRequest{Unit: r8u.Unit(), Text: "Close it?", Origin: dmessage.Origin{Kind: dmessage.OriginCore, Workflow: "close"}})
	if err != nil {
		t.Fatal(err)
	}
	g := r.gateOf(t, m)
	if rec := post(t, r.s, api.TaskPath(m.TaskID, "approve"), ``); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if got, _ := r.s.deps.GateReader.Get(ctx, g.ID); got.Status != gate.Approved || got.By != task.ActorPerson {
		t.Fatalf("gate %+v", got)
	}
	if got, _, _ := r.msgs.Message(ctx, m.ID); got.Open() {
		t.Fatalf("message %+v", got)
	}
}

func TestDismissingAMessageTaskOfAnEarlierVersionDismissesItsQuestion(t *testing.T) {
	t.Parallel()
	r := newMessageRig(t)
	ctx := context.Background()
	r.asked(r8u, turn.Intent(dintent.Review), turn.Asked, publishQuestion)
	e, err := r.s.deps.Tasks.Open(ctx, coretasks.OpenRequest{Kind: task.KindMessage, Title: "Publish?", Unit: ptr(r8u.Unit()), Origin: task.OriginMessage})
	if err != nil {
		t.Fatal(err)
	}
	m, err := r.msgs.Upsert(ctx, dmessage.Message{TaskID: string(e.ID), Unit: r8u.Unit(), Kind: dmessage.Question, Text: "Publish?", Options: publishQuestion.Options,
		Recommend: "publish", Origin: dmessage.Origin{Kind: dmessage.OriginTurn, TurnSeq: 1, Session: "term-1"}, CreatedAt: r.s.deps.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.s.deps.Messages.Reconcile(ctx, r8u.Unit()); err != nil {
		t.Fatal(err)
	}
	if g := r.gateOf(t, m); g.Task != e.ID {
		t.Fatalf("gate %+v", g)
	}
	if rec := post(t, r.s, api.TaskPath(string(e.ID), "dismiss"), ``); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	books, _ := r.store.Books(ctx, r8u.Unit())
	if st := books[r8u.Unit()].Turns[0].State; st != turn.Dismissed {
		t.Fatalf("turn %s", st)
	}
	if got, _ := r.s.deps.Tasks.Task(ctx, e.ID); got.Category != task.CategoryDropped {
		t.Fatalf("task %+v", got)
	}
}

func ptr[T any](v T) *T { return &v }

func TestAClosedTurnAnswers409(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writeTasksError(rec, fmt.Errorf("%w: turn 1", turn.ErrClosed))
	if rec.Code != 409 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestAProposalTheLedgerRefusedAnswers409(t *testing.T) {
	t.Parallel()
	refused := fmt.Errorf("gate 3 resolved approved, its consequence failed: %w: item: unknown \"i9\"", messages.ErrNotApplied)
	for name, write := range map[string]func(http.ResponseWriter, error){"gate": writeGateError, "task": writeTasksError} {
		rec := httptest.NewRecorder()
		if write(rec, refused); rec.Code != 409 {
			t.Errorf("%s route: %d %s", name, rec.Code, rec.Body)
		}
	}
}

func getRaw(t *testing.T, s *Server, path string) string {
	t.Helper()
	var raw json.RawMessage
	if code := get(t, s, path, &raw); code != 200 {
		t.Fatalf("%s: %d", path, code)
	}
	return string(raw) + "\n"
}
