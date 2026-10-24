package cli

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	slacksource "github.com/wloophq/wloop/internal/adapter/source/slack"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/daemon"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/slack/send"
	"github.com/wloophq/wloop/internal/transport/mcp"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func sendRequest(target, text string) send.Request {
	return send.Request{Target: target, Text: text, Source: "session"}
}

// wiredSlack is the daemon's Slack adapter for te's config.
func wiredSlack(t *testing.T, te *slackTestEnv, logf func(string, ...any)) *slackSender {
	t.Helper()
	d := (mcpEndpoint{log: logf, slack: te.slack}).wire(daemon.Deps{}, te.cfg)
	sender, ok := d.MCP.(*mcp.Server).Slack.(*slackSender)
	if !ok {
		t.Fatalf("Slack = %T, want the adapter", d.MCP.(*mcp.Server).Slack)
	}
	return sender
}

func TestServeWiresSlack(t *testing.T) {
	te := newSlackTestEnv(t)
	te.fakeChannel()
	sender := wiredSlack(t, te, nil)
	if sender.kit.Store.Service != "loop-test-slack" || sender.kit.Store.Keychain != te.kc || sender.kit.Send.Client != sender.kit.Client || sender.kit.Client.Tokens != sender.kit.Store {
		t.Fatal("the adapter is not over the configured store and service")
	}

	te.kc.gets = 0
	for range 100 {
		if sender.LoggedIn() {
			t.Fatal("logged in with an empty keychain")
		}
	}
	if te.kc.gets != 1 {
		t.Fatalf("keychain reads = %d for 100 checks, want 1", te.kc.gets)
	}
	if _, err := sender.Send(context.Background(), sendRequest("C0000000001", "hi")); !errors.Is(err, send.ErrNotLoggedIn) {
		t.Fatalf("Send logged out = %v", err)
	}
	if n := len(te.srv.Calls("chat.postMessage")) + len(te.srv.Calls("conversations.info")); n != 0 {
		t.Fatalf("logged out reached Slack %d times", n)
	}

	te.saveToken(t, fakeToken())
	sender = wiredSlack(t, te, nil)
	if !sender.LoggedIn() {
		t.Fatal("not logged in with a stored token")
	}
	sent, err := sender.Send(context.Background(), sendRequest("C0000000001", "hi"))
	if err != nil || !strings.Contains(sent.Permalink, "p1700000000000100") {
		t.Fatalf("Send = %+v, %v", sent, err)
	}
	if auths := te.srv.Authorization("chat.postMessage"); len(auths) != 1 || auths[0] != "xoxe.xoxp-1-fakeaccess" {
		t.Fatal("the post did not carry the stored token")
	}
}

func TestServeLeavesSlackNilWhenDisabled(t *testing.T) {
	off := false
	for name, tc := range map[string]struct {
		cfg     func(*config.Config)
		enabled func(unit.ProviderID) bool
	}{
		"no client id":    {cfg: func(c *config.Config) { c.Slack.ClientID = "" }},
		"plugin disabled": {cfg: func(c *config.Config) { c.Plugins = map[string]config.Plugin{"slack": {Enabled: &off}} }},
		"daemon switch":   {cfg: func(*config.Config) {}, enabled: func(p unit.ProviderID) bool { return p != "slack" }},
	} {
		t.Run(name, func(t *testing.T) {
			te := newSlackTestEnv(t)
			tc.cfg(&te.cfg)
			d := (mcpEndpoint{slack: te.slack}).wire(daemon.Deps{PluginEnabled: tc.enabled}, te.cfg)
			srv := d.MCP.(*mcp.Server)
			if srv.Slack != nil {
				t.Fatalf("Slack = %T, want nil", srv.Slack)
			}
			if listed := toolNames(t, srv); strings.Contains(listed, "slack_send_message") {
				t.Fatalf("tools %s", listed)
			}
			if len(te.kc.services) != 0 {
				t.Fatalf("keychain touched: %v", te.kc.services)
			}
		})
	}
}

// toolNames is the tools/list answer of srv over HTTP.
func toolNames(t *testing.T, srv *mcp.Server) string {
	t.Helper()
	host := "127.0.0.1:" + strconv.Itoa(srv.Port)
	post := func(sid, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "http://"+host+"/mcp", strings.NewReader(body))
		req.Host = host
		if sid != "" {
			req.Header.Set("Mcp-Session-Id", sid)
		}
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return rec
	}
	init := post("", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
	sid := init.Header().Get("Mcp-Session-Id")
	if sid == "" {
		t.Fatalf("initialize: %d %s", init.Code, init.Body)
	}
	list := post(sid, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "turn_end") {
		t.Fatalf("tools/list: %d %s", list.Code, list.Body)
	}
	return list.Body.String()
}

func TestServeWiresSlackRead(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	st := newSlackTestEnv(t)
	te.cfg.Slack = st.cfg.Slack
	te.Env.slack = st.slack
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	const conv, ts = "C0000000001", "1700000000.000100"
	facts := dslack.Facts{Team: "T000", Threads: []dslack.Thread{{Conversation: conv, TS: ts, Messages: []dslack.Message{
		{TS: ts, ThreadTS: ts, User: "U000", Text: "stored thread text"}}}}}
	if err := (&slacksource.Store{DB: db.Slack()}).Save(ctx, nil, facts, "", testNow); err != nil {
		t.Fatal(err)
	}

	srv := (mcpEndpoint{slack: te.Env.slack}).wire(serveDeps(ctx, te.Env, db, te.cfg, t.Logf, &sync.Mutex{}, nil), te.cfg).MCP.(*mcp.Server)
	if srv.SlackRead == nil || !strings.Contains(toolNames(t, srv), "slack_read") {
		t.Fatal("slack_read is not wired")
	}
	target := "slack:" + dslack.ThreadUnit("T000", conv, ts).Key
	if _, err := srv.SlackRead.Read(ctx, target, 50); !errors.Is(err, send.ErrNotLoggedIn) {
		t.Fatalf("logged out: %v", err)
	}

	st.saveToken(t, fakeToken())
	srv = (mcpEndpoint{slack: te.Env.slack}).wire(serveDeps(ctx, te.Env, db, te.cfg, t.Logf, &sync.Mutex{}, nil), te.cfg).MCP.(*mcp.Server)
	got, err := srv.SlackRead.Read(ctx, target, 50)
	if err != nil || !strings.Contains(got, "stored thread text") {
		t.Fatalf("Read = %q, %v", got, err)
	}
	if n := len(st.srv.Calls("conversations.replies")); n != 0 {
		t.Fatalf("a stored read called Slack %d times", n)
	}

	off := false
	te.cfg.Plugins = map[string]config.Plugin{"slack": {Enabled: &off}}
	srv = (mcpEndpoint{slack: te.Env.slack}).wire(serveDeps(ctx, te.Env, db, te.cfg, t.Logf, &sync.Mutex{}, nil), te.cfg).MCP.(*mcp.Server)
	if srv.SlackRead != nil {
		t.Fatal("slack_read wired with the plugin disabled")
	}
}
