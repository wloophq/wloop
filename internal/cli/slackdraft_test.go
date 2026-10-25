package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	slacksource "github.com/wloophq/wloop/internal/adapter/source/slack"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/draft"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/transport/mcp"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

const (
	sessChannel  = "C0000000001"
	sessThreaded = "C0000000002"
	sessDM       = "D0000000009"
	sessUser     = "U0000000009"
	sessParent   = "1700000000.000100"
	sessReply    = "1700000000.000200"
	sessPosted   = "1700000100.000300"
)

// sessionSlack answers a session's sends: a channel, a channel with a thread, and a DM.
func sessionSlack(st *slackTestEnv) {
	st.srv.Handle("conversations.info", func(v url.Values) any {
		switch v.Get("channel") {
		case sessDM:
			return map[string]any{"channel": map[string]any{"id": sessDM, "is_im": true, "user": sessUser}}
		case sessThreaded:
			return map[string]any{"channel": map[string]any{"id": sessThreaded, "name": "random", "is_channel": true, "is_member": true}}
		}
		return map[string]any{"channel": map[string]any{"id": sessChannel, "name": "general", "is_channel": true, "is_member": true}}
	})
	st.srv.Handle("conversations.replies", func(v url.Values) any {
		return map[string]any{"messages": []map[string]any{{"ts": v.Get("ts"), "thread_ts": sessParent, "text": "parent"}}}
	})
	st.srv.Handle("users.info", func(url.Values) any {
		return map[string]any{"user": map[string]any{"id": sessUser, "name": "dana"}}
	})
	st.srv.Handle("conversations.open", func(url.Values) any { return map[string]any{"channel": map[string]any{"id": sessDM}} })
	st.srv.Handle("chat.postMessage", func(v url.Values) any {
		return map[string]any{"channel": v.Get("channel"), "ts": sessPosted}
	})
	st.srv.Handle("chat.getPermalink", func(v url.Values) any {
		link := "https://acme.example/archives/" + v.Get("channel") + "/p" + strings.ReplaceAll(v.Get("message_ts"), ".", "")
		if v.Get("channel") == sessThreaded {
			link += "?thread_ts=" + sessParent + "&cid=" + sessThreaded
		}
		return map[string]any{"permalink": link}
	})
}

type sessionWiring struct {
	te   *testEnv
	st   *slackTestEnv
	db   *sqlite.DB
	srv  *mcp.Server
	deps daemon.Deps

	mu      sync.Mutex
	methods []string
}

func newSessionWiring(t *testing.T) *sessionWiring {
	t.Helper()
	te := newTestEnv(t, &fake.Manager{})
	st := newSlackTestEnv(t)
	te.cfg.Slack = st.cfg.Slack
	te.Env.slack = st.slack
	db := te.db
	sessionSlack(st)
	st.saveToken(t, fakeToken())
	w := &sessionWiring{te: te, st: st, db: db}
	for _, id := range []string{draftHandle, "term_other1"} {
		te.registerSession(t, dturn.Session{ID: id, WorkspaceID: "ws-1"})
	}
	w.deps = (mcpEndpoint{slack: te.Env.slack}).wire(serveDeps(context.Background(), te.Env, db, te.cfg, serveLog(t), &sync.Mutex{}, nil), te.cfg)
	w.srv = w.deps.MCP.(*mcp.Server)
	sender, ok := w.srv.Slack.(*slackSender)
	if !ok {
		t.Fatalf("Slack = %T", w.srv.Slack)
	}
	sender.kit.Client.RequestLog = func(method string, _ int) {
		w.mu.Lock()
		w.methods = append(w.methods, method)
		w.mu.Unlock()
	}
	return w
}

func (w *sessionWiring) send(t *testing.T, target string) string {
	t.Helper()
	w.mu.Lock()
	w.methods = nil
	w.mu.Unlock()
	if _, err := w.srv.Slack.Send(context.Background(), sendRequest(target, "a session message")); err != nil {
		t.Fatalf("send to %s: %v", target, err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.Join(w.methods, " ")
}

func TestSessionSendUntrackedMatchesPhase0(t *testing.T) {
	t.Parallel()
	w := newSessionWiring(t)
	var b strings.Builder
	for _, target := range []string{
		sessChannel,
		"https://acme.slack.com/archives/" + sessThreaded + "/p" + strings.ReplaceAll(sessReply, ".", ""),
		sessUser,
	} {
		fmt.Fprintf(&b, "%s\n  %s\n", target, w.send(t, target))
	}
	golden(t, "session_send_calls.golden", b.String())
	drafts, err := w.db.Drafts().List(context.Background(), outbox.Filter{})
	if err != nil || len(drafts) != 0 {
		t.Fatalf("drafts after untracked sends = %d, %v", len(drafts), err)
	}
}

// rpc posts body to srv's MCP endpoint as the session handle and returns the answer.
func rpc(t *testing.T, srv *mcp.Server, sid, handle, body string) *httptest.ResponseRecorder {
	t.Helper()
	host := "127.0.0.1:" + strconv.Itoa(srv.Port)
	req := httptest.NewRequest(http.MethodPost, "http://"+host+"/mcp", strings.NewReader(body))
	req.Host = host
	req.Header.Set(api.SessionHeader, handle)
	if sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func (w *sessionWiring) draftTool(t *testing.T, handle, args string) (string, bool) {
	t.Helper()
	init := rpc(t, w.srv, "", handle, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
	sid := init.Header().Get("Mcp-Session-Id")
	if sid == "" {
		t.Fatalf("initialize: %d %s", init.Code, init.Body)
	}
	rec := rpc(t, w.srv, sid, handle, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"slack_draft","arguments":`+args+`}}`)
	var res struct {
		Result struct {
			Content []struct{ Text string } `json:"content"`
			IsError bool                    `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil || len(res.Result.Content) == 0 {
		t.Fatalf("tools/call: %v %s", err, rec.Body)
	}
	return res.Result.Content[0].Text, res.Result.IsError
}

func (w *sessionWiring) drafts(t *testing.T) []draft.Draft {
	t.Helper()
	ds, err := w.db.Drafts().List(context.Background(), outbox.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	return ds
}

const draftHandle = "term_draft1"

func TestSlackDraftProposes(t *testing.T) {
	t.Parallel()
	w := newSessionWiring(t)
	ctx := context.Background()
	u := dslack.ThreadUnit("T000", sessChannel, sessParent)
	_, err := w.te.repo.Save(ctx, dturn.Book{Unit: u, Turns: []dturn.Turn{
		{Intent: "triage", Trigger: dturn.ByHand, Session: draftHandle, State: dturn.Done, SentAt: testNow, Workflow: "old"},
		{Intent: "reply", Trigger: dturn.ByHand, Session: draftHandle, State: dturn.Started, SentAt: testNow, Workflow: "slack-replies"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	stored, _, err := w.te.repo.BookOf(ctx, turns.Key{Session: draftHandle})
	if err != nil {
		t.Fatal(err)
	}
	newest := stored.Turns[len(stored.Turns)-1]

	got, isErr := w.draftTool(t, draftHandle, `{"text":"on it, **today**","items":[]}`)
	if isErr || !strings.Contains(got, "draft 1") || !strings.Contains(got, "#general") {
		t.Fatalf("slack_draft = %q (error %v)", got, isErr)
	}
	ds := w.drafts(t)
	if len(ds) != 1 {
		t.Fatalf("drafts %d", len(ds))
	}
	d := ds[0]
	want := draft.Origin{Kind: draft.OriginTurn, TurnSeq: newest.Seq, Intent: "reply", Workflow: newest.Workflow}
	if d.Unit != u || d.State != draft.Proposed || d.Origin != want || d.Kind != draft.Reply || d.Text != "on it, **today**" || d.Approval.Mode != draft.Always {
		t.Fatalf("draft %+v", d)
	}
	if n := len(w.st.srv.Calls("chat.postMessage")) + len(w.st.srv.Calls("reactions.add")); n != 0 {
		t.Fatalf("slack_draft posted %d times", n)
	}

	if got, isErr := w.draftTool(t, "term_other1", `{"text":"hi"}`); !isErr || !strings.Contains(got, "no turn") {
		t.Fatalf("a session with no turn: %q", got)
	}
	if got, isErr := w.draftTool(t, draftHandle, `{"text":"hi","items":["nope"]}`); !isErr || !strings.Contains(got, "nope") {
		t.Fatalf("a foreign item: %q", got)
	}
	if len(w.drafts(t)) != 1 {
		t.Fatal("a refused call stored a draft")
	}
}

func TestSessionSendToATrackedThreadIsRecorded(t *testing.T) {
	t.Parallel()
	w := newSessionWiring(t)
	ctx := context.Background()
	facts := dslack.Facts{Team: "T000", Conversations: []dslack.Conversation{{ID: sessThreaded, Name: "random", Kind: dslack.KindChannel, IsMember: true}},
		Threads: []dslack.Thread{{Conversation: sessThreaded, TS: sessParent, Messages: []dslack.Message{{TS: sessParent, ThreadTS: sessParent, User: "U000", Text: "parent"}}}}}
	if err := (&slacksource.Store{DB: w.db.Slack()}).Save(ctx, nil, facts, "", testNow); err != nil {
		t.Fatal(err)
	}
	reply := "https://acme.slack.com/archives/" + sessThreaded + "/p" + strings.ReplaceAll(sessReply, ".", "")
	calls := w.send(t, reply)
	if want := "conversations.info conversations.replies conversations.replies chat.postMessage chat.getPermalink"; calls != want {
		t.Fatalf("calls %s, want phase 0's %s", calls, want)
	}
	ds := w.drafts(t)
	if len(ds) != 1 {
		t.Fatalf("drafts %d", len(ds))
	}
	d := ds[0]
	if d.Unit != dslack.ThreadUnit("T000", sessThreaded, sessParent) || d.State != draft.Sent || d.Origin.Kind != draft.OriginSession ||
		d.Evidence.Ref != sessPosted || !strings.Contains(d.Evidence.Permalink, "thread_ts="+sessParent) || d.Text != "a session message" {
		t.Fatalf("draft %+v", d)
	}
	if len(w.st.srv.Calls("chat.postMessage")) != 1 {
		t.Fatal("recording posted again")
	}
}

func TestSessionSendToASegmentIsRecorded(t *testing.T) {
	t.Parallel()
	w := newSessionWiring(t)
	ctx := context.Background()
	facts := dslack.Facts{Team: "T000", Conversations: []dslack.Conversation{{ID: sessDM, Kind: dslack.KindIM, DMUser: sessUser}}}
	if err := (&slacksource.Store{DB: w.db.Slack()}).Save(ctx, nil, facts, "", testNow); err != nil {
		t.Fatal(err)
	}
	units, err := (slacksource.Segments{Store: w.db.Segments(), Clock: clockFunc(func() time.Time { return testNow })}).Apply(ctx, "T000", sessDM,
		[]dslack.Span{{FromTS: "1700000000.000000"}})
	if err != nil || len(units) != 1 {
		t.Fatalf("segments %v %v", units, err)
	}
	w.send(t, sessUser)
	ds := w.drafts(t)
	if len(ds) != 1 || ds[0].Unit != units[0] || ds[0].State != draft.Sent || ds[0].Evidence.Ref != sessPosted {
		t.Fatalf("drafts %+v", ds)
	}
}

func TestApprovedSlackDraftPublishesThroughTheDaemonOutbox(t *testing.T) {
	t.Parallel()
	w := newSessionWiring(t)
	ctx := context.Background()
	u := dslack.ThreadUnit("T000", sessChannel, sessParent)
	if _, err := w.te.repo.Save(ctx, dturn.Book{Unit: u, Turns: []dturn.Turn{
		{Intent: "reply", Trigger: dturn.ByHand, Session: draftHandle, State: dturn.Started, SentAt: testNow},
	}}); err != nil {
		t.Fatal(err)
	}
	if got, isErr := w.draftTool(t, draftHandle, `{"text":"on it"}`); isErr {
		t.Fatalf("slack_draft = %q", got)
	}
	d := w.drafts(t)[0]
	if _, err := w.deps.Outbox.Approve(ctx, d.ID, nil); err != nil {
		t.Fatal(err)
	}
	if sent, failed, err := w.deps.Outbox.Send(ctx); sent != 1 || failed != 0 || err != nil {
		t.Fatalf("Send = %d sent, %d failed, %v; draft %+v", sent, failed, err, w.drafts(t)[0])
	}
	posts := w.st.srv.Calls("chat.postMessage")
	if len(posts) != 1 || posts[0].Get("channel") != sessChannel || posts[0].Get("thread_ts") != sessParent || posts[0].Get("text") != "on it" {
		t.Fatalf("posts %v", posts)
	}
	if got := w.drafts(t)[0]; got.State != draft.Sent || got.Evidence.Ref != sessPosted {
		t.Fatalf("draft %+v", got)
	}
}

func TestSlackDraftIsTiedToTheEntryTheTurnWasAskedFrom(t *testing.T) {
	t.Parallel()
	w := newSessionWiring(t)
	ctx := context.Background()
	u := dslack.ThreadUnit("T000", sessChannel, sessParent)
	_, err := w.te.repo.Save(ctx, dturn.Book{Unit: u, Turns: []dturn.Turn{
		{Intent: "act", Trigger: dturn.ByHand, Session: draftHandle, State: dturn.Started, SentAt: testNow, Inputs: map[string]any{"entry": "e42"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got, isErr := w.draftTool(t, draftHandle, `{"text":"done"}`); isErr {
		t.Fatalf("slack_draft = %q", got)
	}
	ds := w.drafts(t)
	if len(ds) != 1 || ds[0].Entry != "e42" {
		t.Fatalf("drafts %+v", ds)
	}
}

func TestSlackSendToAThreadGoesThroughTheOutbox(t *testing.T) {
	w := newSessionWiring(t)
	ctx := context.Background()
	d := w.deps
	d.Startup = nil
	w.te.Socket = serveSocket(t, daemon.New(d).Handler())
	wait := postWait
	postWait = 10 * time.Millisecond
	t.Cleanup(func() { postWait = wait })
	link := "https://acme.slack.com/archives/" + sessChannel + "/p" + strings.ReplaceAll(sessReply, ".", "") + "?thread_ts=" + sessParent + "&cid=" + sessChannel
	if code := w.te.run("slack", "send", link, "on it", "--yes"); code != 0 || !strings.Contains(w.te.out.String(), "draft 1 approved") {
		t.Fatalf("exit %d: %s %s", code, w.te.out, w.te.err)
	}
	ds := w.drafts(t)
	if len(ds) != 1 || ds[0].Unit != dslack.ThreadUnit("T000", sessChannel, sessParent) || ds[0].Origin.Kind != draft.OriginUser || ds[0].State != draft.Approved {
		t.Fatalf("drafts %+v", ds)
	}
	if sent, _, err := w.deps.Outbox.Send(ctx); sent != 1 || err != nil {
		t.Fatalf("Send = %d, %v", sent, err)
	}
	if posts := w.st.srv.Calls("chat.postMessage"); len(posts) != 1 || posts[0].Get("thread_ts") != sessParent || posts[0].Get("text") != "on it" {
		t.Fatalf("posts %v", posts)
	}

	w.te.out.Reset()
	if code := w.te.run("slack", "send", sessChannel, "top level", "--yes"); code != 0 || len(w.drafts(t)) != 1 {
		t.Fatalf("a post to a channel: exit %d, drafts %d: %s", code, len(w.drafts(t)), w.te.err)
	}
	if code := w.te.run("slack", "send", link, "to the channel", "--thread=false", "--yes"); code != 0 || len(w.drafts(t)) != 1 {
		t.Fatalf("--thread=false: exit %d, drafts %d: %s", code, len(w.drafts(t)), w.te.err)
	}
	if n := len(w.st.srv.Calls("chat.postMessage")); n != 3 {
		t.Fatalf("posts %d", n)
	}
}
