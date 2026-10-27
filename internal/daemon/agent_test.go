package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	dmessage "github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/transport/mcp"
)

var agentNow = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

type agentClock struct{}

func (agentClock) Now() time.Time { return agentNow }

// agentServer is a daemon whose turns live in a real store.
func agentServer(t *testing.T) (*Server, *turns.Service) {
	t.Helper()
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "inv.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := sqlite.New(db)
	svc := &turns.Service{Intents: turnstest.Intents(), Store: repo, Registry: repo, Sessions: turnstest.NewSessions(), Clock: agentClock{}, Command: brand.Name, FastJob: 2 * time.Minute}
	inbox := coregtd.New(coregtd.Deps{Store: db.Entries(), Items: itemsNone{}, Facts: enabledUnits{map[unit.ProviderID]bool{}}, Clock: agentClock{}})
	s := New(withStore(Deps{Turns: svc, Activity: db, Cfg: config.Defaults(), Version: "test", Now: func() time.Time { return agentNow },
		Viewer:   func(context.Context) (string, []string, error) { return "me", nil, nil },
		Messages: &messages.Service{Store: inventory.NewMessages(db), Books: repo, Turns: svc, Entries: inbox, Intents: turnstest.Intents(), Clock: agentClock{}},
		ReadText: func(_ context.Context, convs []dmessage.Conversation, id int64) string {
			var texts []string
			for _, c := range convs {
				for _, a := range c.Answers {
					texts = append(texts, fmt.Sprintf("%d:%s", c.Message.ID, a.Text))
				}
			}
			return fmt.Sprintf("message %d: %s", id, strings.Join(texts, ","))
		}}, inventory.New(db)))
	return s, svc
}

// sendTurn stores a turn sent by an automated round, with its instructions.
func sendTurn(t *testing.T, s *Server, svc *turns.Service, ref pr.Ref, intent turn.Intent, round string) {
	t.Helper()
	ctx := context.Background()
	if _, err := svc.Send(ctx, turns.SendRequest{Unit: ref.Unit(), Intent: intent, Turn: round, Trigger: turn.Automated, Head: "h2", Records: true}); err != nil {
		t.Fatal(err)
	}
	stored := dinv.StoredTurn{ID: round, Kind: string(intent), Repo: ref.Owner + "/" + ref.Repo, Text: "Instructions " + round + " for " + string(intent) + "\n"}
	if err := s.store().SaveTurn(ctx, stored, agentNow); err != nil {
		t.Fatal(err)
	}
}

func agentCall(t *testing.T, h http.Handler, method, path, handle, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := apiRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if handle != "" {
		req.Header.Set(api.SessionHeader, handle)
		req.Header.Set(api.ManagerHeader, turnstest.TestManager)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// sessionOf is the session of the test manager's handle, adopting a process
// no session took yet as agentCall's hooks would.
func sessionOf(t *testing.T, svc *turns.Service, handle string) string {
	t.Helper()
	id, err := svc.Resolve(context.Background(), turnstest.TestManager, handle)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func bookOf(t *testing.T, svc *turns.Service, ref pr.Ref) turn.Book {
	t.Helper()
	books, err := svc.Store.Books(context.Background(), ref.Unit())
	if err != nil {
		t.Fatal(err)
	}
	return books[ref.Unit()]
}

var agentRef = pr.Ref{Owner: "acme", Repo: "api", Number: 7}

const doneReview = `{"state":"done","summary":"reviewed","outcome":{"verdict":"COMMENT","url":"https://github.com/acme/api/pull/7#pullrequestreview-1"}}`

func TestTurnLifecycleThroughTheAgentAPI(t *testing.T) {
	t.Parallel()
	s, svc := agentServer(t)
	sendTurn(t, s, svc, agentRef, turn.Intent(dintent.Review), "abc123abc123")
	h := s.Handler()

	rec := agentCall(t, h, "POST", api.AgentTurnsStart, "term_1", `{"turn":"abc123abc123","intent":"review"}`)
	var text api.TurnText
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &text) != nil || !strings.HasPrefix(text.Text, "Instructions abc123abc123") {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	if got := bookOf(t, svc, agentRef).Turns[0]; got.State != turn.Started || got.Session != sessionOf(t, svc, "term_1") {
		t.Fatalf("after start: %+v", got)
	}

	rec = agentCall(t, h, "POST", api.AgentTurnsEnd, "term_1", `{"turn":"abc123abc123","pr":"`+agentRef.URL()+`","record":`+doneReview+`}`)
	var ended api.TurnEnded
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &ended) != nil || ended.State != "done" || !ended.Added || ended.PR != agentRef.URL() {
		t.Fatalf("end: %d %s", rec.Code, rec.Body)
	}
	if got := bookOf(t, svc, agentRef).Turns[0]; got.State != turn.Done {
		t.Fatalf("after end: %+v", got)
	}

	tcp := httptest.NewRecorder()
	req := apiRequest("POST", api.AgentTurnsEnd, strings.NewReader(`{}`))
	req.AddCookie(&http.Cookie{Name: tokenCookie, Value: s.token})
	req.Header.Set(api.SessionHeader, "term_1")
	s.AuthHandler().ServeHTTP(tcp, req)
	if tcp.Code != http.StatusNotFound {
		t.Fatalf("the TCP port answered %d to AgentAPI: %s", tcp.Code, tcp.Body)
	}
}

func TestTurnEndRefusesAMalformedRecordWithEveryProblem(t *testing.T) {
	t.Parallel()
	s, svc := agentServer(t)
	sendTurn(t, s, svc, agentRef, turn.Intent(dintent.Review), "abc123abc123")
	rec := agentCall(t, s.Handler(), "POST", api.AgentTurnsEnd, "", `{"turn":"abc123abc123","record":{"state":"done","summary":"reviewed"}}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "outcome.verdict") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if got := bookOf(t, svc, agentRef); len(got.Records) != 0 {
		t.Fatalf("stored %+v", got.Records)
	}
}

func TestTurnEndWithoutIntentsWiredAnswers503(t *testing.T) {
	t.Parallel()
	s, svc := agentServer(t)
	sendTurn(t, s, svc, agentRef, turn.Intent(dintent.Review), "abc123abc123")
	svc.Intents = nil
	rec := agentCall(t, s.Handler(), "POST", api.AgentTurnsEnd, "", `{"turn":"abc123abc123","record":`+doneReview+`}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestTurnEndOfAnotherSessionIsRefused(t *testing.T) {
	t.Parallel()
	s, svc := agentServer(t)
	sendTurn(t, s, svc, agentRef, turn.Intent(dintent.Review), "abc123abc123")
	h := s.Handler()
	if rec := agentCall(t, h, "POST", api.AgentTurnsStart, "term_1", `{"turn":"abc123abc123","intent":"review"}`); rec.Code != 200 {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	rec := agentCall(t, h, "POST", api.AgentTurnsEnd, "term_2", `{"turn":"abc123abc123","record":`+doneReview+`}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "belongs to another session") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestTurnEndRefusesARecordFileOlderThanItsTurn(t *testing.T) {
	t.Parallel()
	s, svc := agentServer(t)
	sendTurn(t, s, svc, agentRef, turn.Intent(dintent.Review), "abc123abc123")
	old := agentNow.Add(-3 * time.Second).Format(time.RFC3339)
	rec := agentCall(t, s.Handler(), "POST", api.AgentTurnsEnd, "", `{"turn":"abc123abc123","written_at":"`+old+`","record":`+doneReview+`}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "before turn abc123abc123 started") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestTurnEndTakesARecordFileWrittenWithinTheSlack(t *testing.T) {
	t.Parallel()
	s, svc := agentServer(t)
	sendTurn(t, s, svc, agentRef, turn.Intent(dintent.Review), "abc123abc123")
	early := agentNow.Add(-RecordFileSlack + time.Second).Format(time.RFC3339)
	rec := agentCall(t, s.Handler(), "POST", api.AgentTurnsEnd, "", `{"turn":"abc123abc123","written_at":"`+early+`","record":`+doneReview+`}`)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestTurnStartPrintsInstructionsStoredForNoTurn(t *testing.T) {
	t.Parallel()
	s, svc := agentServer(t)
	stored := dinv.StoredTurn{ID: "dry123dry123", Kind: "review", Repo: "acme/api", Text: "Instructions dry123dry123 for review\n"}
	if err := s.store().SaveTurn(context.Background(), stored, agentNow); err != nil {
		t.Fatal(err)
	}
	rec := agentCall(t, s.Handler(), "POST", api.AgentTurnsStart, "term_1", `{"turn":"dry123dry123","intent":"review"}`)
	var text api.TurnText
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &text) != nil || text.Text != stored.Text {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if b := bookOf(t, svc, agentRef); len(b.Turns) != 0 {
		t.Fatalf("a turn appeared: %+v", b.Turns)
	}
}

func TestAgentAPIRefusesAHeaderThatIsNotAHandle(t *testing.T) {
	t.Parallel()
	s, _ := agentServer(t)
	for _, path := range []string{api.AgentTurnsEnd, api.AgentSessionsStop, api.AgentSessionsStart, api.AgentSessionsPrompt} {
		if rec := agentCall(t, s.Handler(), "POST", path, "${ORCA_TERMINAL_HANDLE}", `{}`); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
	if rec := agentCall(t, s.Handler(), "POST", api.AgentSessionsStop, "", `{}`); rec.Code != http.StatusBadRequest {
		t.Errorf("a stop without a handle: %d %s", rec.Code, rec.Body)
	}
}

func TestSessionStartAndStopThroughTheAgentAPI(t *testing.T) {
	t.Parallel()
	s, svc := agentServer(t)
	h := s.Handler()
	rec := agentCall(t, h, "POST", api.AgentSessionsStart, "term_1", `{"pane":"tab:leaf","worktree_id":"wt-1","agent_session":"sess-1","source":"startup","cwd":"/wt/rev","home":"/h"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	sess, ok, err := svc.Registry.Session(context.Background(), sessionOf(t, svc, "term_1"))
	if err != nil || !ok || sess.Pane != "tab:leaf" || sess.WorkspaceID != "wt-1" || sess.AgentSession != "sess-1" || sess.Worktree != "/wt/rev" || sess.Home != "/h" || !sess.StartedAt.Equal(agentNow) {
		t.Fatalf("session %+v %v %v", sess, ok, err)
	}

	sendTurn(t, s, svc, agentRef, turn.Intent(dintent.Review), "abc123abc123")
	if rec := agentCall(t, h, "POST", api.AgentTurnsStart, "term_1", `{"turn":"abc123abc123","intent":"review"}`); rec.Code != 200 {
		t.Fatalf("turn start: %d %s", rec.Code, rec.Body)
	}
	var ans api.StopAnswer
	rec = agentCall(t, h, "POST", api.AgentSessionsStop, "term_1", `{"agent_session":"sess-1","prompt_id":"p1","last_message":"done"}`)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &ans) != nil || !ans.Block || !strings.Contains(ans.Reason, "Turn=abc123abc123") {
		t.Fatalf("first stop: %d %s", rec.Code, rec.Body)
	}
	rec = agentCall(t, h, "POST", api.AgentSessionsStop, "term_1", `{"agent_session":"sess-1","prompt_id":"p1","last_message":"done"}`)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &ans) != nil || ans.Block {
		t.Fatalf("second stop of the same prompt: %d %s", rec.Code, rec.Body)
	}
	if got := bookOf(t, svc, agentRef).Turns[0]; got.State != turn.Unrecorded {
		t.Fatalf("turn %+v", got)
	}
}

type savedAgentSessions struct{ got []turns.AgentSessionInfo }

func (s *savedAgentSessions) SaveAgentSession(_ context.Context, a turns.AgentSessionInfo) error {
	s.got = append(s.got, a)
	return nil
}

func TestHooksPassTheirAgentKindAndTranscript(t *testing.T) {
	t.Parallel()
	s, svc := agentServer(t)
	saved := &savedAgentSessions{}
	svc.AgentSessions = saved
	h := s.Handler()
	if rec := agentCall(t, h, "POST", api.AgentSessionsStart, "term_1", `{"agent_session":"sess-1","cwd":"/wt/rev","home":"/h","agent":"echo","transcript":"/h/projects/-wt-rev/sess-1.jsonl"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	if rec := agentCall(t, h, "POST", api.AgentSessionsStop, "term_1", `{"agent_session":"sess-1","prompt_id":"p1","agent":"echo","transcript":"/t/stop.jsonl"}`); rec.Code != 200 {
		t.Fatalf("stop: %d %s", rec.Code, rec.Body)
	}
	id := sessionOf(t, svc, "term_1")
	want := []turns.AgentSessionInfo{
		{ID: "sess-1", Kind: "echo", Session: id, Home: "/h", Worktree: "/wt/rev", Transcript: "/h/projects/-wt-rev/sess-1.jsonl", At: agentNow},
		{ID: "sess-1", Kind: "echo", Session: id, Home: "/h", Worktree: "/wt/rev", Transcript: "/t/stop.jsonl", At: agentNow},
	}
	if len(saved.got) != len(want) {
		t.Fatalf("saved %+v", saved.got)
	}
	for i := range want {
		if saved.got[i] != want[i] {
			t.Errorf("save %d:\n%+v\nwant\n%+v", i, saved.got[i], want[i])
		}
	}
}

func TestTurnAnswerReadsTheUnreadAnswersOfTheCallersTurns(t *testing.T) {
	t.Parallel()
	s, svc := agentServer(t)
	sendTurn(t, s, svc, agentRef, turn.Intent(dintent.Review), "abc123abc123")
	h := s.Handler()
	if rec := agentCall(t, h, "POST", api.AgentTurnsStart, "term_1", `{"turn":"abc123abc123","intent":"review"}`); rec.Code != 200 {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	path := api.AgentAnswer + "?pr=" + agentRef.URL()
	read := func(handle, path string) (int, string) {
		t.Helper()
		var text api.TurnText
		rec := agentCall(t, h, "GET", path, handle, "")
		_ = json.Unmarshal(rec.Body.Bytes(), &text)
		return rec.Code, text.Text
	}
	if code, text := read("term_1", path); code != 200 || text != "message 0: " {
		t.Fatalf("before an answer: %d %q", code, text)
	}
	b := bookOf(t, svc, agentRef)
	b.Answer(turn.Answer{TurnSeq: b.Turns[0].Seq, Text: "first line\nsecond line", Source: turn.Cockpit, At: agentNow}).Route = turn.Typed
	if _, err := svc.Store.Save(context.Background(), b, port.Event{Unit: agentRef.Unit(), Kind: turns.KindAnswered, At: agentNow}); err != nil {
		t.Fatal(err)
	}
	if code, text := read("term_2", path); code != 200 || text != "message 0: " {
		t.Fatalf("another session read the answer: %d %q", code, text)
	}
	if code, text := read("term_1", path); code != 200 || text != "message 0: 0:first line\nsecond line" {
		t.Fatalf("%d %q", code, text)
	}
	if code, text := read("term_1", path); code != 200 || text != "message 0: " {
		t.Fatalf("read twice: %d %q", code, text)
	}
	if code, _ := read("term_1", path+"&message=42"); code != http.StatusNotFound {
		t.Fatalf("a message of no turn of the session: %d", code)
	}
	if code, _ := read("term_1", path+"&message=x"); code != http.StatusBadRequest {
		t.Fatalf("a bad message id: %d", code)
	}
}

// An agent reports facts: what it writes as the user's words never endorses
// a verdict, whichever channel carries it.
func TestAgentChannelsNeverEndorse(t *testing.T) {
	t.Parallel()
	const approve = `{"state":"done","summary":"approved","outcome":{"verdict":"APPROVE","url":"https://github.com/acme/api/pull/%d#pullrequestreview-1"},"user":"publish, I approve"}`
	s, svc := agentServer(t)
	socketRef, mcpRef := agentRef, pr.Ref{Owner: "acme", Repo: "api", Number: 8}
	sendTurn(t, s, svc, socketRef, turn.Intent(dintent.Review), "aaa111aaa111")
	sendTurn(t, s, svc, mcpRef, turn.Intent(dintent.Review), "bbb222bbb222")
	for handle, round := range map[string]string{"term_1": "aaa111aaa111", "term_2": "bbb222bbb222"} {
		if _, err := svc.Start(context.Background(), sessionOf(t, svc, handle), round); err != nil {
			t.Fatal(err)
		}
	}

	h := s.Handler()
	rec := agentCall(t, h, "POST", api.AgentTurnsEnd, "term_1", `{"turn":"aaa111aaa111","record":`+strings.Replace(approve, "%d", "7", 1)+`}`)
	if rec.Code != 200 {
		t.Fatalf("socket: %d %s", rec.Code, rec.Body)
	}

	m := &mcp.Server{Turns: svc, Port: 47800}
	mcpCall := func(sid, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:47800/mcp", strings.NewReader(body))
		req.Host = "127.0.0.1:47800"
		req.Header.Set(api.SessionHeader, "term_2")
		if sid != "" {
			req.Header.Set("Mcp-Session-Id", sid)
		}
		rec := httptest.NewRecorder()
		m.ServeHTTP(rec, req)
		return rec
	}
	sid := mcpCall("", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`).Header().Get("Mcp-Session-Id")
	args := `{"turn":"bbb222bbb222","record":` + strings.Replace(approve, "%d", "8", 1) + `}`
	rec = mcpCall(sid, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"turn_end","arguments":`+args+`}}`)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), `"isError":true`) {
		t.Fatalf("mcp: %d %s", rec.Code, rec.Body)
	}

	for _, ref := range []pr.Ref{socketRef, mcpRef} {
		b := bookOf(t, svc, ref)
		if got := b.Turns[0]; got.State != turn.Done {
			t.Fatalf("%s: %+v", ref, got)
		}
		if got := b.Endorsed(b.Turns[0].Seq); got != turn.NotEndorsed {
			t.Errorf("%s: endorsed %q", ref, got)
		}
	}
}

func TestTurnByHandErrorsKeepTheirStatus(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		err  error
		want int
	}{
		{BadRequest("--take-over applies to address"), http.StatusBadRequest},
		{badRequest{"the nudge has no target"}, http.StatusBadRequest},
		{notFound{"no such pull request"}, http.StatusNotFound},
		{tooManyRequests{"slow down"}, http.StatusTooManyRequests},
		{errors.New("boom"), http.StatusInternalServerError},
	} {
		s, _ := agentServer(t)
		s.deps.TurnByHand = func(context.Context, api.ByHandRequest, string) (api.ByHandResult, error) {
			return api.ByHandResult{}, c.err
		}
		rec := agentCall(t, s.Handler(), http.MethodPost, api.AgentTurnsByHand, "", `{"intent":"review","cwd":"/wt"}`)
		if rec.Code != c.want || !strings.Contains(rec.Body.String(), c.err.Error()) {
			t.Errorf("%v: %d %s", c.err, rec.Code, rec.Body)
		}
	}
}

func TestThePromptHookReachesTheSession(t *testing.T) {
	t.Parallel()
	s, svc := agentServer(t)
	h := s.Handler()
	if rec := agentCall(t, h, "POST", api.AgentSessionsStart, "term_1", `{"agent_session":"sess-1","source":"startup","cwd":"/wt/rev"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	if rec := agentCall(t, h, "POST", api.AgentSessionsPrompt, "term_1", `{"prompt":"and the tests?"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("prompt: %d %s", rec.Code, rec.Body)
	}
	sess, _, err := svc.Registry.Session(context.Background(), sessionOf(t, svc, "term_1"))
	if err != nil || !sess.PromptSeenAt.Equal(agentNow) || !sess.UserPromptAt.Equal(agentNow) {
		t.Fatalf("session %+v %v", sess, err)
	}
	if rec := agentCall(t, h, "POST", api.AgentSessionsPrompt, "", `{"prompt":"hi"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("a prompt without a handle: %d %s", rec.Code, rec.Body)
	}
}
