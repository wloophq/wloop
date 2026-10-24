package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/core/gtd/gtdtest"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/core/messages/messagestest"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/domain/gtd"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/items"
	dmessage "github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type enabledUnits struct{ off map[unit.ProviderID]bool }

func (e enabledUnits) UnitFacts(_ context.Context, u unit.Unit) (gtd.UnitFacts, bool, error) {
	return gtd.UnitFacts{Unit: u}, !e.off[u.Provider], nil
}

type itemsNone struct{}

func (itemsNone) Items(context.Context, unit.Unit) ([]items.Item, error) { return nil, nil }

type messageRig struct {
	*turnRig
	msgs    *messagestest.Store
	entries *gtdtest.Store
	facts   enabledUnits
}

var r8u = pr.Ref{Owner: "acme", Repo: "api", Number: 8}

// newMessageRig serves the messages over a turn rig, with the entries' verbs registered.
func newMessageRig(t *testing.T) *messageRig {
	t.Helper()
	r := &messageRig{turnRig: newTurnRig(t), msgs: messagestest.New(), entries: gtdtest.New(), facts: enabledUnits{map[unit.ProviderID]bool{}}}
	clock := fixedClock(r.s.deps.Now())
	r.s.deps.GTD = coregtd.New(coregtd.Deps{Store: r.entries, Items: itemsNone{}, Facts: r.facts, Clock: clock})
	r.s.deps.Messages = &messages.Service{Store: r.msgs, Books: r.store, Turns: r.s.deps.Turns, Entries: r.s.deps.GTD,
		Intents: turnstest.Intents(), Clock: clock}
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

func entryLines(t *testing.T, body []byte) []string {
	t.Helper()
	var res api.EntryResult
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return res.Lines
}

func TestMessageAnswerDifferential(t *testing.T) {
	sessions := map[string]func(*messageRig){"live idle": (*messageRig).idle, "busy": (*messageRig).busy, "gone": func(*messageRig) {}}
	routes := map[string]turn.Route{"live idle": turn.Typed, "busy": turn.Queued, "gone": turn.Resumed}
	for name, arrange := range sessions {
		for _, source := range []string{"cockpit", "notification", "tui"} {
			t.Run(name+"/"+source, func(t *testing.T) {
				body := `{"option_id":"publish","guidance":"say thanks","source":"` + source + `"}`
				byUnit := func(t *testing.T) outcome {
					r := newMessageRig(t)
					r.question(t)
					arrange(r)
					rec := post(t, r.s, "/api/units/github/acme%2Fapi%238/answer", body)
					return r.outcome(rec.Code, actionLines(t, rec.Body.Bytes()))
				}
				byRoute := func(t *testing.T) outcome {
					r := newMessageRig(t)
					m := r.question(t)
					arrange(r)
					rec := post(t, r.s, api.EntryPath(m.EntryID, "answer"), `{"args":{"option_id":"publish","guidance":"say thanks"},"source":"`+source+`"}`)
					return r.outcome(rec.Code, entryLines(t, rec.Body.Bytes()))
				}
				byEntry := func(t *testing.T) outcome {
					r := newMessageRig(t)
					m := r.question(t)
					arrange(r)
					res, err := r.s.deps.GTD.Do(context.Background(), gtd.ID(m.EntryID), coregtd.Request{Verb: gtd.VerbAnswer,
						Args: map[string]string{"option_id": "publish", "guidance": "say thanks"}, Source: source})
					if err != nil {
						t.Fatal(err)
					}
					if res.Entry.State != gtd.Done {
						t.Fatalf("the entry is %s after its answer", res.Entry.State)
					}
					return r.outcome(200, res.Lines)
				}
				want := byUnit(t)
				if want.Code != 200 || len(want.Answers) != 1 || want.Answers[0].Source != turn.Source(source) || want.Answers[0].Route != routes[name] {
					t.Fatalf("unit path %+v", want)
				}
				for path, got := range map[string]outcome{"entry route": byRoute(t), "entry": byEntry(t)} {
					if !reflect.DeepEqual(got, want) {
						t.Errorf("%s path\n got %+v\nwant %+v", path, got, want)
					}
				}
			})
		}
	}
}

func TestMessageAnswerRefusesAnUnknownOption(t *testing.T) {
	ref := newMessageRig(t)
	ref.question(t)
	want := post(t, ref.s, "/api/units/github/acme%2Fapi%238/answer", `{"option_id":"nope"}`)
	r := newMessageRig(t)
	m := r.question(t)
	got := post(t, r.s, api.EntryPath(m.EntryID, "answer"), `{"args":{"option_id":"nope"}}`)
	if got.Code != 400 || want.Code != 400 || got.Body.String() != want.Body.String() {
		t.Fatalf("entry %d %s; unit %d %s", got.Code, got.Body, want.Code, want.Body)
	}
	if open, _ := r.msgs.Open(context.Background()); len(open) != 1 {
		t.Fatalf("open %+v", open)
	}
}

func TestDismissingAQuestionMessageThroughTheDaemon(t *testing.T) {
	for _, via := range []string{"route", "entry"} {
		t.Run(via, func(t *testing.T) {
			r := newMessageRig(t)
			m := r.question(t)
			if via == "route" {
				if rec := post(t, r.s, api.EntryPath(m.EntryID, "dismiss"), ``); rec.Code != 200 {
					t.Fatalf("%d %s", rec.Code, rec.Body)
				}
			} else if _, err := r.s.deps.GTD.Do(context.Background(), gtd.ID(m.EntryID), coregtd.Request{Verb: gtd.VerbDismiss, Source: "tui"}); err != nil {
				t.Fatal(err)
			}
			books, _ := r.store.Books(context.Background(), r8u.Unit())
			if st := books[r8u.Unit()].Turns[0].State; st != turn.Dismissed {
				t.Fatalf("turn %s", st)
			}
			if got, _, _ := r.msgs.Message(context.Background(), m.ID); got.State != dmessage.Dismissed {
				t.Fatalf("message %+v", got)
			}
			if e, _ := r.entries.Entry(context.Background(), gtd.ID(m.EntryID)); e.State != gtd.Trash {
				t.Fatalf("entry %s", e.State)
			}
		})
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

func TestEntryCarriesItsMessage(t *testing.T) {
	r := newMessageRig(t)
	m := r.question(t)
	got, err := r.s.entryMessage(context.Background(), m.EntryID)
	if err != nil || got == nil || !reflect.DeepEqual(*got, messageView(m)) || got.EntryID != m.EntryID {
		t.Fatalf("%+v %v", got, err)
	}
	capture, err := r.s.deps.GTD.Capture(context.Background(), "call Ana", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := r.s.entryMessage(context.Background(), string(capture.ID)); got != nil || err != nil {
		t.Fatalf("a capture carries %+v %v", got, err)
	}
}

func TestMessageAnswerKeepsItsSource(t *testing.T) {
	for _, c := range []struct {
		via, source string
	}{{"entry", "tui"}, {"route", "notification"}} {
		t.Run(c.via, func(t *testing.T) {
			r := newMessageRig(t)
			m := r.question(t)
			if c.via == "route" {
				if rec := post(t, r.s, api.EntryPath(m.EntryID, "answer"), `{"args":{"option_id":"hold"},"source":"`+c.source+`"}`); rec.Code != 200 {
					t.Fatalf("%d %s", rec.Code, rec.Body)
				}
			} else if _, err := r.s.deps.GTD.Do(context.Background(), gtd.ID(m.EntryID), coregtd.Request{Verb: gtd.VerbAnswer,
				Args: map[string]string{"option_id": "hold"}, Source: c.source}); err != nil {
				t.Fatal(err)
			}
			books, _ := r.store.Books(context.Background(), r8u.Unit())
			if a := books[r8u.Unit()].Answers; len(a) != 1 || a[0].Source != turn.Source(c.source) {
				t.Fatalf("answers %+v", a)
			}
		})
	}
	r := newMessageRig(t)
	m := r.question(t)
	if rec := post(t, r.s, api.EntryPath(m.EntryID, "answer"), `{"args":{"option_id":"hold"},"source":"bogus"}`); rec.Code != 400 {
		t.Fatalf("a bogus source: %d %s", rec.Code, rec.Body)
	}
}

func TestMessageOfADisabledPluginAnswers409(t *testing.T) {
	r := newMessageRig(t)
	m := r.question(t)
	r.s.deps.PluginEnabled = func(p unit.ProviderID) bool { return p != unit.GitHub }
	for _, verb := range []string{"answer", "dismiss"} {
		if rec := post(t, r.s, api.EntryPath(m.EntryID, verb), `{"args":{"option_id":"hold"}}`); rec.Code != 409 || !slices.Contains([]string{`{"error":"github is disabled","plugin":"github"}` + "\n"}, rec.Body.String()) {
			t.Fatalf("%s: %d %s", verb, rec.Code, rec.Body)
		}
	}
	r.facts.off[unit.GitHub] = true
	if _, err := r.s.deps.GTD.Do(context.Background(), gtd.ID(m.EntryID), coregtd.Request{Verb: gtd.VerbAnswer, Args: map[string]string{"option_id": "hold"}}); err == nil {
		t.Fatal("the entry verb answered a disabled plugin's message")
	}
	if books, _ := r.store.Books(context.Background(), r8u.Unit()); len(books[r8u.Unit()].Answers) != 0 {
		t.Fatal("an answer was stored")
	}
}

func TestApproveAnswersAConfirmWithYes(t *testing.T) {
	r := newMessageRig(t)
	ctx := context.Background()
	r.asked(r8u, turn.Intent(dintent.Review), turn.Asked, &record.Question{Kind: "confirm", Text: "Publish?"})
	if err := r.s.deps.Messages.Reconcile(ctx, r8u.Unit()); err != nil {
		t.Fatal(err)
	}
	open, _ := r.msgs.Open(ctx)
	if _, err := r.s.deps.GTD.Do(ctx, gtd.ID(open[0].EntryID), coregtd.Request{Verb: gtd.VerbApprove, Source: "tui"}); err != nil {
		t.Fatal(err)
	}
	books, _ := r.store.Books(ctx, r8u.Unit())
	if a := books[r8u.Unit()].Answers; len(a) != 1 || !a[0].Yes || a[0].Source != turn.TUI {
		t.Fatalf("answers %+v", a)
	}
	m, err := r.s.deps.Messages.Propose(ctx, messages.ProposeRequest{Unit: r8u.Unit(), Text: "Close it?", Origin: dmessage.Origin{Kind: dmessage.OriginCore, Workflow: "close"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.deps.GTD.Do(ctx, gtd.ID(m.EntryID), coregtd.Request{Verb: gtd.VerbApprove}); !errors.Is(err, coregtd.ErrVerb) {
		t.Fatalf("approve on a proposal with no accept option: %v", err)
	}
}

func TestAClosedTurnAnswers409(t *testing.T) {
	rec := httptest.NewRecorder()
	writeGTDError(rec, fmt.Errorf("%w: turn 1", turn.ErrClosed))
	if rec.Code != 409 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}
