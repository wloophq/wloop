package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/lock"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/draft"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/pr"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/transport/mcp"
	"github.com/wloophq/wloop/internal/workspace/fake"
	"github.com/wloophq/wloop/plugins/slack"
	dslack "github.com/wloophq/wloop/plugins/slack/domain"
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
	logs    []string
}

func (w *sessionWiring) logf(format string, args ...any) {
	w.mu.Lock()
	w.logs = append(w.logs, fmt.Sprintf(format, args...))
	w.mu.Unlock()
}

// newSessionWiring serves the daemon's MCP tools as serve wires them, with
// the real Slack plugin running over a fake Keychain and fake Slack, whose
// calls it records.
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
	target, err := url.Parse(st.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	target.Path = ""
	proxy := httputil.NewSingleHostReverseProxy(target)
	rec := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.mu.Lock()
		w.methods = append(w.methods, strings.TrimPrefix(r.URL.Path, "/api/"))
		w.mu.Unlock()
		proxy.ServeHTTP(rw, r)
	}))
	t.Cleanup(rec.Close)
	withSlackPlugin(t, te.Env, st.pluginEnv(t, rec.URL+"/api/"))
	for _, id := range []string{draftHandle, "term_other1"} {
		te.registerSession(t, dturn.Session{ID: id, WorkspaceID: "ws-1"})
	}
	ctx, cancel := context.WithCancel(context.Background())
	stop := te.servePlugins(ctx, db, te.cfg, w.logf)
	t.Cleanup(func() {
		cancel()
		stop()
	})
	eventually(t, "the slack plugin running", func() bool {
		_, err := te.pluginKind.Client(string(slackPlugin))
		return err == nil
	})
	w.deps = (mcpEndpoint{tools: te.pluginTools(db), intents: func() *dintent.Registry { return te.loadedIntents().Registry }, texts: te.Env.toolTexts, log: w.logf}).wire(te.serveDeps(ctx, db, te.cfg, t.Logf, &lock.Mutex{}, nil), te.cfg)
	w.srv = w.deps.MCP.(*mcp.Server)
	return w
}

// send calls slack_send_message to target as the draft session and returns
// the Slack methods it called.
func (w *sessionWiring) send(t *testing.T, target string) string {
	t.Helper()
	w.mu.Lock()
	w.methods = nil
	w.mu.Unlock()
	if got, isErr := w.callTool(t, "", draftHandle, "slack_send_message", `{"target":"`+target+`","text":"a session message"}`); isErr {
		t.Fatalf("send to %s: %s", target, got)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.Join(w.methods, " ")
}

// seedSlack saves facts in the running plugin's store, as its ingestion
// would.
func (w *sessionWiring) seedSlack(t *testing.T, facts dslack.Facts) {
	t.Helper()
	pdb := pluginStore(t, w.te.StateDir)
	if err := (&slack.Store{DB: pdb.Slack()}).Save(context.Background(), nil, facts, "", testNow); err != nil {
		t.Fatal(err)
	}
}

// reported has the running plugin report every unit, and waits for us.
func (w *sessionWiring) reported(t *testing.T, us ...unit.Unit) {
	t.Helper()
	c, err := w.te.pluginKind.Client(string(slackPlugin))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Notify(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the slack plugin's report", func() bool {
		got, err := w.te.pluginKind.Snapshots.Reported(context.Background(), us)
		return err == nil && len(got) == len(us)
	})
}

// segments applies spans to conversation's segment map in the running
// plugin's store and returns their units.
func (w *sessionWiring) segments(t *testing.T, team, conversation string, spans []dslack.Span) []unit.Unit {
	t.Helper()
	pdb := pluginStore(t, w.te.StateDir)
	us, err := (slack.Segments{Store: pdb.Segments(), Clock: clockFunc(func() time.Time { return testNow })}).Apply(context.Background(), team, conversation, spans)
	if err != nil {
		t.Fatal(err)
	}
	var out []unit.Unit
	for _, u := range us {
		out = append(out, unit.Unit{Provider: unit.ProviderID(u.Provider), Key: u.Key})
	}
	return out
}

func TestSessionSendsToUntrackedPlacesAreRecorded(t *testing.T) {
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
	var units []unit.Unit
	for _, d := range w.drafts(t) {
		parts := strings.Split(d.Unit.Key, "/")
		if len(parts) < 2 || d.State != draft.Sent || d.Origin != (draft.Origin{Kind: draft.OriginSession}) || d.Evidence.Ref != parts[1]+"/"+sessPosted {
			t.Fatalf("draft %+v", d)
		}
		units = append(units, d.Unit)
	}
	want := []unit.Unit{slackThreadUnit("T000", sessDM, sessPosted), slackThreadUnit("T000", sessThreaded, sessParent), slackThreadUnit("T000", sessChannel, sessPosted)}
	if !slices.Equal(units, want) {
		t.Fatalf("recorded on %v, want %v", units, want)
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
	return w.callTool(t, sid, handle, "slack_draft", args)
}

func (w *sessionWiring) callTool(t *testing.T, sid, handle, name, args string) (string, bool) {
	t.Helper()
	if sid == "" {
		init := rpc(t, w.srv, "", handle, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
		if sid = init.Header().Get("Mcp-Session-Id"); sid == "" {
			t.Fatalf("initialize: %d %s", init.Code, init.Body)
		}
	}
	rec := rpc(t, w.srv, sid, handle, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"`+name+`","arguments":`+args+`}}`)
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
	u := slackThreadUnit("T000", sessChannel, sessParent)
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
	if d.Unit != u || d.State != draft.Proposed || d.Origin != want || d.Kind != draft.Reply || d.Text != "on it, **today**" || d.Approval.Mode != draft.Wait {
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
	w.seedSlack(t, dslack.Facts{Team: "T000", Conversations: []dslack.Conversation{{ID: sessThreaded, Name: "random", Kind: dslack.KindChannel, IsMember: true}},
		Threads: []dslack.Thread{{Conversation: sessThreaded, TS: sessParent, Messages: []dslack.Message{{TS: sessParent, ThreadTS: sessParent, User: "U000", Text: "parent"}}}}})
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
	if d.Unit != slackThreadUnit("T000", sessThreaded, sessParent) || d.State != draft.Sent || d.Origin.Kind != draft.OriginSession ||
		d.Evidence.Ref != sessThreaded+"/"+sessPosted || !strings.Contains(d.Evidence.Permalink, "thread_ts="+sessParent) || d.Text != "a session message" {
		t.Fatalf("draft %+v", d)
	}
	if len(w.st.srv.Calls("chat.postMessage")) != 1 {
		t.Fatal("recording posted again")
	}
}

func TestSessionSendToASegmentIsRecorded(t *testing.T) {
	t.Parallel()
	w := newSessionWiring(t)
	w.seedSlack(t, dslack.Facts{Team: "T000", Conversations: []dslack.Conversation{{ID: sessDM, Kind: dslack.KindIM, DMUser: sessUser}}})
	units := w.segments(t, "T000", sessDM, []dslack.Span{{FromTS: "1700000000.000000"}})
	if len(units) != 1 {
		t.Fatalf("segments %v", units)
	}
	w.send(t, sessUser)
	ds := w.drafts(t)
	if len(ds) != 1 || ds[0].Unit != units[0] || ds[0].State != draft.Sent || ds[0].Evidence.Ref != sessDM+"/"+sessPosted {
		t.Fatalf("drafts %+v", ds)
	}
}

func TestApprovedSlackDraftPublishesThroughTheDaemonOutbox(t *testing.T) {
	t.Parallel()
	w := newSessionWiring(t)
	ctx := context.Background()
	u := slackThreadUnit("T000", sessChannel, sessParent)
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
	if got := w.drafts(t)[0]; got.State != draft.Sent || got.Evidence.Ref != sessChannel+"/"+sessPosted {
		t.Fatalf("draft %+v", got)
	}
}

func TestSlackDraftIsTiedToTheTaskTheTurnWasAskedFrom(t *testing.T) {
	t.Parallel()
	w := newSessionWiring(t)
	ctx := context.Background()
	u := slackThreadUnit("T000", sessChannel, sessParent)
	_, err := w.te.repo.Save(ctx, dturn.Book{Unit: u, Turns: []dturn.Turn{
		{Intent: "act", Trigger: dturn.ByHand, Session: draftHandle, State: dturn.Started, SentAt: testNow, Inputs: map[string]any{"task": "e42"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got, isErr := w.draftTool(t, draftHandle, `{"text":"done"}`); isErr {
		t.Fatalf("slack_draft = %q", got)
	}
	ds := w.drafts(t)
	if len(ds) != 1 || ds[0].Task != "e42" {
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
	if len(ds) != 1 || ds[0].Unit != slackThreadUnit("T000", sessChannel, sessParent) || ds[0].Origin.Kind != draft.OriginUser || ds[0].State != draft.Approved {
		t.Fatalf("drafts %+v", ds)
	}
	if sent, _, err := w.deps.Outbox.Send(ctx); sent != 1 || err != nil {
		t.Fatalf("Send = %d, %v", sent, err)
	}
	if posts := w.st.srv.Calls("chat.postMessage"); len(posts) != 1 || posts[0].Get("thread_ts") != sessParent || posts[0].Get("text") != "on it" {
		t.Fatalf("posts %v", posts)
	}

	w.te.out.Reset()
	if code := w.te.run("slack", "send", sessChannel+"/"+sessReply, "by ref", "--yes"); code != 0 || !strings.Contains(w.te.out.String(), "draft 2 approved") {
		t.Fatalf("a ref: exit %d: %s %s", code, w.te.out, w.te.err)
	}
	if ds := w.drafts(t); len(ds) != 2 || ds[1].Unit != slackThreadUnit("T000", sessChannel, sessParent) {
		t.Fatalf("a ref's drafts %+v", ds)
	}
	if code := w.te.run("slack", "send", sessChannel, "top level", "--yes"); code != 0 || len(w.drafts(t)) != 2 {
		t.Fatalf("a post to a channel: exit %d, drafts %d: %s", code, len(w.drafts(t)), w.te.err)
	}
	if code := w.te.run("slack", "send", link, "to the channel", "--thread=false", "--yes"); code != 0 || len(w.drafts(t)) != 2 {
		t.Fatalf("--thread=false: exit %d, drafts %d: %s", code, len(w.drafts(t)), w.te.err)
	}
	if n := len(w.st.srv.Calls("chat.postMessage")); n != 3 {
		t.Fatalf("posts %d", n)
	}
}

func TestSlackSendAndOutboxNewToADirectMessageRefReplyInItsSegment(t *testing.T) {
	w := newSessionWiring(t)
	ctx := context.Background()
	d := w.deps
	d.Startup = nil
	w.te.Socket = serveSocket(t, daemon.New(d).Handler())
	wait := postWait
	postWait = 10 * time.Millisecond
	t.Cleanup(func() { postWait = wait })
	w.seedSlack(t, dslack.Facts{Team: "T000", Conversations: []dslack.Conversation{{ID: sessDM, Kind: dslack.KindIM, DMUser: sessUser}}})
	segs := w.segments(t, "T000", sessDM, []dslack.Span{{FromTS: "1700000000.000000"}})
	if len(segs) != 1 {
		t.Fatalf("segments %v", segs)
	}
	ref := sessDM + "/" + sessReply
	if code := w.te.run("slack", "send", ref, "on it", "--yes"); code != 0 || !strings.Contains(w.te.out.String(), "draft 1 approved") {
		t.Fatalf("slack send: exit %d: %s %s", code, w.te.out, w.te.err)
	}
	w.te.out.Reset()
	if code := w.te.run("outbox", "new", ref, "later"); code != 0 || !strings.Contains(w.te.out.String(), "draft 2 saved") {
		t.Fatalf("outbox new: exit %d: %s %s", code, w.te.out, w.te.err)
	}
	ds := w.drafts(t)
	if len(ds) != 2 || ds[0].Unit != segs[0] || ds[1].Unit != segs[0] {
		t.Fatalf("drafts %+v", ds)
	}
	if sent, _, err := w.deps.Outbox.Send(ctx); sent != 1 || err != nil {
		t.Fatalf("Send = %d, %v", sent, err)
	}
	if posts := w.st.srv.Calls("chat.postMessage"); len(posts) != 1 || posts[0].Get("channel") != sessDM || posts[0].Get("thread_ts") != sessParent {
		t.Fatalf("posts %v", posts)
	}
}

func TestSlackDraftRefusesAnEffectItsIntentDoesNotList(t *testing.T) {
	t.Parallel()
	w := newSessionWiring(t)
	ctx := context.Background()
	u := slackThreadUnit("T000", sessChannel, sessParent)
	if _, err := w.te.repo.Save(ctx, dturn.Book{Unit: u, Turns: []dturn.Turn{
		{Intent: "reply", Trigger: dturn.ByHand, Session: draftHandle, State: dturn.Started, SentAt: testNow},
	}}); err != nil {
		t.Fatal(err)
	}
	got, isErr := w.draftTool(t, draftHandle, `{"text":"Closing.","kind":"close","effects":["unit.closed"]}`)
	if !isErr || !strings.Contains(got, "the reply intent lists in drafts.allowed_effects") {
		t.Fatalf("slack_draft = %q (error %v)", got, isErr)
	}
	if len(w.drafts(t)) != 0 {
		t.Fatal("a refused effect stored a draft")
	}
	if got, isErr := w.draftTool(t, draftHandle, `{"text":"Closing.","kind":"close"}`); isErr {
		t.Fatalf("a close draft with no effect: %q", got)
	}
	if ds := w.drafts(t); len(ds) != 1 || len(ds[0].Effects) != 0 {
		t.Fatalf("drafts %+v", ds)
	}
}

func TestASlackDraftDeclaringAGitHubEffectIsRefused(t *testing.T) {
	t.Parallel()
	w := newSessionWiring(t)
	files := bundledIntentAs(t, "act", "nudging-act")
	files["intents/nudging-act/intent.toml"] = strings.Replace(files["intents/nudging-act/intent.toml"], "role    = \"headless\"\n", "", 1)
	files["intents/nudging-act/intent.toml"] = strings.Replace(files["intents/nudging-act/intent.toml"], `allowed_effects = ["task.nudged", "unit.closed"]`, `allowed_effects = ["task.nudged", "pr.nudged"]`, 1)
	userProfile(t, w.te, files)
	ctx := context.Background()
	u := slackThreadUnit("T000", sessChannel, sessParent)
	if _, err := w.te.repo.Save(ctx, dturn.Book{Unit: u, Turns: []dturn.Turn{
		{Intent: "nudging-act", Trigger: dturn.ByHand, Session: draftHandle, State: dturn.Started, SentAt: testNow},
	}}); err != nil {
		t.Fatal(err)
	}
	got, isErr := w.draftTool(t, draftHandle, `{"text":"any news?","effects":["pr.nudged"]}`)
	if !isErr || !strings.Contains(got, "pr.nudged") {
		t.Fatalf("slack_draft = %q (error %v); broken %v", got, isErr, w.te.loadedIntents().Broken)
	}
	if len(w.drafts(t)) != 0 {
		t.Fatal("a draft whose effect never applies on Slack was stored")
	}
}

func TestASlackDraftOfAnIntentTakesTheRuleItsUnitMatches(t *testing.T) {
	t.Parallel()
	w := newSessionWiring(t)
	files := bundledIntentAs(t, "act", "quick-act")
	m := strings.Replace(files["intents/quick-act/intent.toml"], "role    = \"headless\"\n", "", 1)
	files["intents/quick-act/intent.toml"] = m + `
[gates.approval]
mode = "wait"
[[gates.approval.rule]]
when = 'draft.delivery == "message" && slack.channel == "` + sessChannel + `"'
mode = "delay"
window = "10m"
`
	userProfile(t, w.te, files)
	ctx := context.Background()
	u := slackThreadUnit("T000", sessChannel, sessParent)
	w.seedSlack(t, dslack.Facts{Team: "T000", Conversations: []dslack.Conversation{{ID: sessChannel, Name: "general", Kind: dslack.KindChannel, IsMember: true}},
		Threads: []dslack.Thread{{Conversation: sessChannel, TS: sessParent, Messages: []dslack.Message{{TS: sessParent, ThreadTS: sessParent, User: "U000", Text: "parent"}}}}})
	w.reported(t, u)
	if _, err := w.te.repo.Save(ctx, dturn.Book{Unit: u, Turns: []dturn.Turn{
		{Intent: "quick-act", Trigger: dturn.ByHand, Session: draftHandle, State: dturn.Started, SentAt: testNow},
	}}); err != nil {
		t.Fatal(err)
	}
	if got, isErr := w.draftTool(t, draftHandle, `{"text":"on it"}`); isErr {
		t.Fatalf("slack_draft = %q; broken %v", got, w.te.loadedIntents().Broken)
	}
	if ds := w.drafts(t); len(ds) != 1 || ds[0].State != draft.Proposed || ds[0].Approval.Mode != draft.Delay {
		t.Fatalf("drafts %+v", ds)
	}
}

func TestSlackDraftTellsTheAgentWhatItsApprovalPolicyDid(t *testing.T) {
	t.Parallel()
	for mode, c := range map[string]struct {
		state draft.State
		says  string
	}{
		"wait":    {draft.Proposed, "once the user approves it"},
		"send":    {draft.Approved, "is approved by its policy"},
		"dry_run": {draft.Discarded, "is a dry run"},
	} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			w := newSessionWiring(t)
			files := bundledIntentAs(t, "act", "policy-act")
			m := strings.Replace(files["intents/policy-act/intent.toml"], "role    = \"headless\"\n", "", 1)
			files["intents/policy-act/intent.toml"] = m + "\n[[gates.approval.rule]]\nwhen = 'draft.delivery == \"message\"'\nmode = \"" + mode + "\"\n"
			userProfile(t, w.te, files)
			u := slackThreadUnit("T000", sessChannel, sessParent)
			if _, err := w.te.repo.Save(context.Background(), dturn.Book{Unit: u, Turns: []dturn.Turn{
				{Intent: "policy-act", Trigger: dturn.ByHand, Session: draftHandle, State: dturn.Started, SentAt: testNow},
			}}); err != nil {
				t.Fatal(err)
			}
			got, isErr := w.draftTool(t, draftHandle, `{"text":"on it"}`)
			if isErr || !strings.Contains(got, c.says) {
				t.Fatalf("slack_draft = %q, want it to say %q; broken %v", got, c.says, w.te.loadedIntents().Broken)
			}
			if ds := w.drafts(t); len(ds) != 1 || ds[0].State != c.state {
				t.Fatalf("drafts %+v, want one %s", ds, c.state)
			}
		})
	}
}

func TestSlackDraftFromAPullRequestTurnDraftsInThePermalinksThread(t *testing.T) {
	t.Parallel()
	w := newSessionWiring(t)
	ctx := context.Background()
	if _, err := w.te.repo.Save(ctx, dturn.Book{Unit: pr.Ref{Owner: "acme", Repo: "widgets", Number: 7}.Unit(), Turns: []dturn.Turn{
		{Intent: "address", Trigger: dturn.ByHand, Session: draftHandle, State: dturn.Started, SentAt: testNow},
	}}); err != nil {
		t.Fatal(err)
	}
	link := "https://acme.slack.com/archives/" + sessThreaded + "/p" + strings.ReplaceAll(sessReply, ".", "") + "?thread_ts=" + sessParent + "&cid=" + sessThreaded
	if got, isErr := w.draftTool(t, draftHandle, `{"text":"release 1.2 is out"}`); !isErr || !strings.Contains(got, "a target it posts to") {
		t.Fatalf("no permalink: %q", got)
	}
	for _, args := range []string{`{"text":"x","target":"` + link + `","items":["i1"]}`, `{"text":"x","target":"` + link + `","effects":["unit.closed"]}`} {
		if got, isErr := w.draftTool(t, draftHandle, args); !isErr || !strings.Contains(got, "outside") {
			t.Fatalf("%s: %q", args, got)
		}
	}
	if len(w.drafts(t)) != 0 {
		t.Fatal("a refused call stored a draft")
	}

	got, isErr := w.draftTool(t, draftHandle, `{"text":"release 1.2 is out","target":"`+link+`"}`)
	if isErr || !strings.Contains(got, "approves it in the") || !strings.Contains(got, "#random") {
		t.Fatalf("slack_draft = %q (error %v)", got, isErr)
	}
	ds := w.drafts(t)
	if len(ds) != 1 {
		t.Fatalf("drafts %d", len(ds))
	}
	d := ds[0]
	wantOrigin := draft.Origin{Kind: draft.OriginTurn, TurnSeq: 1, Intent: "address", Unit: pr.Ref{Owner: "acme", Repo: "widgets", Number: 7}.Unit()}
	if d.Unit != slackThreadUnit("T000", sessThreaded, sessParent) || d.Origin != wantOrigin || d.Target != link ||
		d.State != draft.Proposed || d.Approval.Mode != draft.Wait || d.Would.Policy != draft.WouldWait {
		t.Fatalf("draft %+v", d)
	}
	if _, err := w.deps.Outbox.Approve(ctx, d.ID, nil); err != nil {
		t.Fatal(err)
	}
	if sent, _, err := w.deps.Outbox.Send(ctx); sent != 1 || err != nil {
		t.Fatalf("Send = %d, %v; draft %+v", sent, err, w.drafts(t)[0])
	}
	if posts := w.st.srv.Calls("chat.postMessage"); len(posts) != 1 || posts[0].Get("channel") != sessThreaded || posts[0].Get("thread_ts") != sessParent {
		t.Fatalf("posts %v", posts)
	}
}

func TestFromAPullRequestTurnEditsAndReactionsPostAtOnceAndAreRecorded(t *testing.T) {
	t.Parallel()
	w := newSessionWiring(t)
	ctx := context.Background()
	prUnit := pr.Ref{Owner: "acme", Repo: "widgets", Number: 7}.Unit()
	if _, err := w.te.repo.Save(ctx, dturn.Book{Unit: prUnit, Turns: []dturn.Turn{
		{Intent: "address", Trigger: dturn.ByHand, Session: draftHandle, State: dturn.Started, SentAt: testNow},
	}}); err != nil {
		t.Fatal(err)
	}
	author := "U000"
	w.st.srv.Handle("conversations.replies", func(v url.Values) any {
		return map[string]any{"messages": []map[string]any{{"ts": sessParent, "thread_ts": sessParent, "text": "parent"},
			{"ts": sessReply, "thread_ts": sessParent, "user": author, "text": "release 1.2 :hourglass:"}}}
	})
	w.st.srv.Handle("chat.update", func(v url.Values) any { return map[string]any{"channel": v.Get("channel"), "ts": v.Get("ts")} })
	w.st.srv.Handle("reactions.add", func(url.Values) any { return map[string]any{} })
	w.st.srv.Handle("reactions.remove", func(url.Values) any { return map[string]any{} })
	link := "https://acme.slack.com/archives/" + sessThreaded + "/p" + strings.ReplaceAll(sessReply, ".", "") + "?thread_ts=" + sessParent + "&cid=" + sessThreaded

	if got, isErr := w.callTool(t, "", draftHandle, "slack_edit", `{"message":"`+link+`","text":"release 1.2 **done**"}`); isErr || !strings.Contains(got, "#random") {
		t.Fatalf("slack_edit = %q", got)
	}
	if ups := w.st.srv.Calls("chat.update"); len(ups) != 1 || ups[0].Get("channel") != sessThreaded || ups[0].Get("ts") != sessReply || ups[0].Get("text") != "release 1.2 *done*" {
		t.Fatalf("chat.update %v", ups)
	}
	for _, args := range []string{`{"message":"` + link + `","emoji":"white_check_mark"}`, `{"message":"` + link + `","emoji":"hourglass","remove":true}`} {
		if got, isErr := w.callTool(t, "", draftHandle, "slack_react", args); isErr || !strings.Contains(got, "#random") {
			t.Fatalf("slack_react %s = %q", args, got)
		}
	}
	if adds, removes := w.st.srv.Calls("reactions.add"), w.st.srv.Calls("reactions.remove"); len(adds) != 1 || adds[0].Get("timestamp") != sessReply ||
		len(removes) != 1 || removes[0].Get("name") != "hourglass" {
		t.Fatalf("adds %v, removes %v", adds, removes)
	}
	ds := w.drafts(t)
	slices.SortFunc(ds, func(a, b draft.Draft) int { return int(a.ID - b.ID) })
	origin := draft.Origin{Kind: draft.OriginSession, TurnSeq: 1, Intent: "address", Unit: prUnit}
	thread := slackThreadUnit("T000", sessThreaded, sessParent)
	if len(ds) != 3 {
		t.Fatalf("records %+v", ds)
	}
	for i, want := range []struct{ purpose, reaction string }{{"edit", ""}, {"react", "white_check_mark"}, {"unreact", "hourglass"}} {
		if d := ds[i]; d.State != draft.Sent || d.Purpose != want.purpose || d.Reaction != want.reaction || d.Unit != thread || d.Origin != origin {
			t.Fatalf("record %d %+v", i, d)
		}
	}
	if d := ds[0]; d.Kind != draft.Edit || d.Text != "release 1.2 **done**" || d.Replaced != "release 1.2 :hourglass:" || d.Evidence.Ref != sessThreaded+"/"+sessReply {
		t.Fatalf("edit record %+v", d)
	}

	author = "U0000000077"
	if got, isErr := w.callTool(t, "", draftHandle, "slack_edit", `{"message":"`+link+`","text":"hijacked"}`); !isErr || !strings.Contains(got, "only the user's own") {
		t.Fatalf("another's message: %q", got)
	}
	if n := len(w.st.srv.Calls("chat.update")); n != 1 {
		t.Fatalf("chat.update %d", n)
	}
}

func TestSlackDraftResolvesHandlesAndEmailsIntoMentions(t *testing.T) {
	t.Parallel()
	w := newSessionWiring(t)
	w.st.srv.Handle("users.list", func(url.Values) any {
		return map[string]any{"members": []map[string]any{{"id": sessUser, "name": "dana"}}}
	})
	w.st.srv.Handle("users.lookupByEmail", func(url.Values) any {
		return map[string]any{"user": map[string]any{"id": sessUser, "name": "dana"}}
	})
	ctx := context.Background()
	if _, err := w.te.repo.Save(ctx, dturn.Book{Unit: slackThreadUnit("T000", sessChannel, sessParent), Turns: []dturn.Turn{
		{Intent: "reply", Trigger: dturn.ByHand, Session: draftHandle, State: dturn.Started, SentAt: testNow},
	}}); err != nil {
		t.Fatal(err)
	}
	if got, isErr := w.draftTool(t, draftHandle, `{"text":"@dana and dana@example.com, see `+"`@dana`"+`"}`); isErr {
		t.Fatalf("slack_draft = %q", got)
	}
	if ds := w.drafts(t); len(ds) != 1 || ds[0].Text != "@{slack:"+sessUser+"} and @{slack:"+sessUser+"}, see `@dana`" {
		t.Fatalf("drafts %+v", ds)
	}
}

func TestEveryToolIsDescribedByTheBundledProfile(t *testing.T) {
	t.Parallel()
	w := newSessionWiring(t)
	if w.srv.Inbox == nil || len(w.srv.Plugins) != 6 {
		t.Fatal("the wiring serves only some tools")
	}
	init := rpc(t, w.srv, "", draftHandle, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	rec := rpc(t, w.srv, init.Header().Get("Mcp-Session-Id"), draftHandle, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	var got struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range got.Result.Tools {
		names = append(names, tl.Name)
		if tl.Description == "" || strings.Contains(tl.Description, "{{") {
			t.Errorf("%s: description %q", tl.Name, tl.Description)
		}
	}
	if want := builtin.ValueNames(builtin.Tools); !slices.Equal(slices.Sorted(slices.Values(names)), slices.Sorted(slices.Values(want))) {
		t.Errorf("listed %v, tools.toml describes %v", names, want)
	}
	for _, call := range []struct{ tool, args string }{{"slack_send_message", `{}`}, {"slack_draft", `{"kind":"nope"}`}, {"turn_end", `[]`}} {
		if got, isErr := w.callTool(t, "", draftHandle, call.tool, call.args); !isErr || strings.Contains(got, "map[") {
			t.Errorf("%s answered %q", call.tool, got)
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, l := range w.logs {
		if strings.Contains(l, "tools.toml") {
			t.Errorf("logged %q", l)
		}
	}
}

// TestPostingOutsideATurn posts at once from a session with no turn, whose
// posts are recorded, and from no session, whose posts are not; neither
// drafts.
func TestPostingOutsideATurn(t *testing.T) {
	t.Parallel()
	link := "https://acme.slack.com/archives/" + sessThreaded + "/p" + strings.ReplaceAll(sessReply, ".", "") + "?thread_ts=" + sessParent + "&cid=" + sessThreaded
	thread := slackThreadUnit("T000", sessThreaded, sessParent)
	session := draft.Origin{Kind: draft.OriginSession}
	for name, handle := range map[string]string{"a session with no turn": "term_other1", "no session": "", "a closed session": "term_closed1"} {
		t.Run(name, func(t *testing.T) {
			w := newSessionWiring(t)
			w.te.registerSession(t, dturn.Session{ID: "term_closed1", WorkspaceID: "ws-1", ClosedAt: testNow})
			w.st.srv.Handle("reactions.add", func(url.Values) any { return map[string]any{} })
			if got, isErr := w.callTool(t, "", handle, "slack_send_message", `{"target":"`+link+`","text":"release 1.2 is out"}`); isErr {
				t.Fatalf("slack_send_message = %q", got)
			}
			if got, isErr := w.callTool(t, "", handle, "slack_react", `{"message":"`+link+`","emoji":"white_check_mark"}`); isErr {
				t.Fatalf("slack_react = %q", got)
			}
			for _, d := range w.drafts(t) {
				if d.State != draft.Sent || d.Unit != thread || d.Origin != session {
					t.Fatalf("record %+v", d)
				}
			}
			if n := len(w.drafts(t)); n != 2 {
				t.Fatalf("records %d", n)
			}

			got, isErr := w.callTool(t, "", handle, "slack_draft", `{"text":"any news?","target":"`+link+`"}`)
			if handle != "term_other1" {
				if !isErr || !strings.Contains(got, "slack_draft refused") {
					t.Fatalf("slack_draft with no session = %q", got)
				}
				return
			}
			if isErr {
				t.Fatalf("slack_draft = %q", got)
			}
			if d := w.drafts(t)[0]; d.State != draft.Proposed || d.Unit != thread || d.Origin != session || d.Approval.Mode != draft.Wait {
				t.Fatalf("draft %+v", d)
			}
			if got, isErr := w.callTool(t, "", handle, "slack_draft", `{"text":"any news?"}`); !isErr || !strings.Contains(got, "no turn") {
				t.Fatalf("slack_draft with no permalink = %q", got)
			}
		})
	}
}

func TestAReactionOnAReplyPermalinkIsRecordedOnItsThread(t *testing.T) {
	t.Parallel()
	w := newSessionWiring(t)
	w.st.srv.Handle("reactions.add", func(url.Values) any { return map[string]any{} })
	reply := "https://acme.slack.com/archives/" + sessThreaded + "/p" + strings.ReplaceAll(sessReply, ".", "")
	if got, isErr := w.callTool(t, "", draftHandle, "slack_react", `{"message":"`+reply+`","emoji":"eyes"}`); isErr {
		t.Fatalf("slack_react = %q", got)
	}
	if ds := w.drafts(t); len(ds) != 1 || ds[0].Unit != slackThreadUnit("T000", sessThreaded, sessParent) {
		t.Fatalf("records %+v", ds)
	}
}

func TestSlackDraftFromATaskTurnPostsToItsTargetThroughSlack(t *testing.T) {
	t.Parallel()
	w := newSessionWiring(t)
	ctx := context.Background()
	task := unit.Unit{Provider: unit.Task, Key: "e1"}
	if _, err := w.te.repo.Save(ctx, dturn.Book{Unit: task, Turns: []dturn.Turn{
		{Intent: "act", Trigger: dturn.ByHand, Session: draftHandle, State: dturn.Started, SentAt: testNow},
	}}); err != nil {
		t.Fatal(err)
	}
	if got, isErr := w.draftTool(t, draftHandle, `{"text":"the new search is live"}`); !isErr {
		t.Fatalf("a draft with no target on an entry turn: %q", got)
	}
	got, isErr := w.draftTool(t, draftHandle, `{"text":"the new search is live","target":"`+sessChannel+`"}`)
	if isErr || !strings.Contains(got, "#general") {
		t.Fatalf("slack_draft = %q (error %v)", got, isErr)
	}
	ds := w.drafts(t)
	if len(ds) != 1 {
		t.Fatalf("drafts %+v", ds)
	}
	d := ds[0]
	if d.Unit != task || d.Target != sessChannel || d.Origin != (draft.Origin{Kind: draft.OriginTurn, TurnSeq: 1, Intent: "act"}) ||
		d.State != draft.Proposed || d.Would.Policy != draft.WouldWait {
		t.Fatalf("draft %+v", d)
	}
	if _, err := w.deps.Outbox.Approve(ctx, d.ID, nil); err != nil {
		t.Fatal(err)
	}
	if sent, _, err := w.deps.Outbox.Send(ctx); sent != 1 || err != nil {
		t.Fatalf("Send = %d, %v; draft %+v", sent, err, w.drafts(t)[0])
	}
	if posts := w.st.srv.Calls("chat.postMessage"); len(posts) != 1 || posts[0].Get("channel") != sessChannel || posts[0].Has("thread_ts") {
		t.Fatalf("posts %v", posts)
	}
}

func TestSlackDraftFromAPullRequestTurnStillNeedsAPermalink(t *testing.T) {
	t.Parallel()
	w := newSessionWiring(t)
	if _, err := w.te.repo.Save(context.Background(), dturn.Book{Unit: pr.Ref{Owner: "acme", Repo: "widgets", Number: 7}.Unit(), Turns: []dturn.Turn{
		{Intent: "address", Trigger: dturn.ByHand, Session: draftHandle, State: dturn.Started, SentAt: testNow},
	}}); err != nil {
		t.Fatal(err)
	}
	if got, isErr := w.draftTool(t, draftHandle, `{"text":"x","target":"`+sessChannel+`"}`); !isErr || !strings.Contains(got, "a target it posts to") {
		t.Fatalf("slack_draft = %q", got)
	}
	if len(w.drafts(t)) != 0 {
		t.Fatal("a refused call stored a draft")
	}
}

func TestASendFromATaskTurnIsRecordedWithTheTurnAsOrigin(t *testing.T) {
	t.Parallel()
	w := newSessionWiring(t)
	task := unit.Unit{Provider: unit.Task, Key: "e1"}
	if _, err := w.te.repo.Save(context.Background(), dturn.Book{Unit: task, Turns: []dturn.Turn{
		{Intent: "act", Trigger: dturn.ByHand, Session: draftHandle, State: dturn.Started, SentAt: testNow},
	}}); err != nil {
		t.Fatal(err)
	}
	w.send(t, sessChannel)
	ds := w.drafts(t)
	want := draft.Origin{Kind: draft.OriginSession, TurnSeq: 1, Intent: "act", Unit: task}
	if len(ds) != 1 || ds[0].Unit != slackThreadUnit("T000", sessChannel, sessPosted) || ds[0].State != draft.Sent || ds[0].Origin != want {
		t.Fatalf("drafts %+v", ds)
	}
}
