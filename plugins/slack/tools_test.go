package slack

import (
	"context"
	"encoding/json"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/slack/auth"
	"github.com/wloophq/wloop/plugins/slack/config"
	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/host/hosttest"
	slackapi "github.com/wloophq/wloop/plugins/slack/slackapi"
	"github.com/wloophq/wloop/plugins/slack/slacktest"
	"github.com/wloophq/wloop/plugins/slack/store"
)

const (
	toolSession = "session-1"
	toolTeam    = "T000"
	toolThread  = "1699999999.000100"
	toolReply   = "1700000100.000200"
	toolOther   = "1700000500.000100"
)

var toolNow = time.Date(2026, 10, 9, 15, 0, 0, 0, time.UTC)

type toolFixture struct {
	s   *Plugin
	h   *hosttest.Host
	srv *slacktest.Server
	kc  *cliKeychain
}

func newToolFixture(t *testing.T, loggedIn bool) *toolFixture {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f := &toolFixture{h: &hosttest.Host{Now: func() time.Time { return toolNow }}, srv: slacktest.New(t), kc: newCLIKeychain()}
	f.handleWorkspace()
	cfg := config.Config{Slack: config.Slack{ClientID: cliClientID, KeychainService: "loop-test-slack"}, Brand: "loop"}
	client := &slackapi.Client{BaseURL: f.srv.URL}
	tokens := &auth.Store{Keychain: f.kc, Service: cfg.Service(), LockPath: filepath.Join(t.TempDir(), "slack.lock"), Client: client,
		ClientID: cliClientID, Now: func() time.Time { return toolNow }}
	client.Tokens = tokens
	if loggedIn {
		tok := auth.Token{Team: toolTeam, User: "U000", Access: "xoxp-1-faketool", Expires: toolNow.Add(12 * time.Hour)}
		raw, _ := json.Marshal(tok)
		f.kc.Set(cfg.Service(), "current", raw)
	}
	f.s = &Plugin{}
	f.s.p = newPlugin(cfg, f.h.Broker(), db, Slack{Tokens: tokens, Client: client, Now: func() time.Time { return toolNow }}, nil)
	f.h.SetSession(toolSession, src.Session{Unit: dslack.ThreadUnit(toolTeam, "C100", toolThread).Wire(), Turn: "turn-1", TurnSeq: 1,
		Intent: "reply"})
	return f
}

func (f *toolFixture) handleWorkspace() {
	messages := []map[string]any{
		{"type": "message", "ts": toolThread, "thread_ts": toolThread, "user": "U001", "text": "Can someone review the launch plan?"},
		{"type": "message", "ts": toolReply, "thread_ts": toolThread, "user": "U000", "text": "On it"},
	}
	other := []map[string]any{{"type": "message", "ts": toolOther, "thread_ts": toolOther, "user": "U001", "text": "Another thread"}}
	f.srv.Handle("conversations.info", func(v url.Values) any {
		return map[string]any{"channel": map[string]any{"id": v.Get("channel"), "name": "general", "is_channel": true, "is_member": true}}
	})
	f.srv.Handle("conversations.replies", func(v url.Values) any {
		if v.Get("ts") == toolOther {
			return map[string]any{"messages": other}
		}
		return map[string]any{"messages": messages}
	})
	f.srv.Handle("conversations.history", func(url.Values) any { return map[string]any{"messages": messages[1:]} })
	f.srv.Handle("chat.postMessage", func(v url.Values) any { return map[string]any{"channel": v.Get("channel"), "ts": "1700000900.000100"} })
	f.srv.Handle("chat.update", func(url.Values) any { return map[string]any{} })
	f.srv.Handle("reactions.add", func(url.Values) any { return map[string]any{} })
	f.srv.Handle("chat.getPermalink", func(v url.Values) any {
		return map[string]any{"permalink": "https://acme.slack.com/archives/" + v.Get("channel") + "/p" + strings.ReplaceAll(v.Get("message_ts"), ".", "")}
	})
	f.srv.Handle("users.info", func(v url.Values) any {
		return map[string]any{"user": map[string]any{"id": v.Get("user"), "name": "dana"}}
	})
	f.srv.Handle("search.messages", func(url.Values) any {
		return map[string]any{"messages": map[string]any{"total": 1, "matches": []map[string]any{{"ts": toolThread, "user": "U001",
			"text": "Can someone review the launch plan?", "permalink": "https://acme.slack.com/archives/C100/p1699999999000100",
			"channel": map[string]any{"id": "C100"}}}}}
	})
}

func (f *toolFixture) call(t *testing.T, tool, args string) src.ToolResult {
	t.Helper()
	res, err := f.s.CallTool(context.Background(), src.ToolCall{Tool: tool, Args: json.RawMessage(args), Session: toolSession, Turn: "turn-1"})
	if err != nil {
		t.Fatalf("%s %s: %v", tool, args, err)
	}
	return res
}

func toolFields(t *testing.T, r src.ToolResult) map[string]any {
	t.Helper()
	out := map[string]any{}
	if len(r.Fields) > 0 {
		if err := json.Unmarshal(r.Fields, &out); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func toolProblems(t *testing.T, r src.ToolResult) []string {
	t.Helper()
	var f struct{ Problems []Problem }
	if err := json.Unmarshal(r.Fields, &f); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range f.Problems {
		out = append(out, p.Answer)
	}
	return out
}

func TestCallToolNamesEachArgumentProblem(t *testing.T) {
	t.Parallel()
	f := newToolFixture(t, true)
	for _, c := range []struct {
		tool, args string
		want       []string
	}{
		{ToolSend, `{}`, []string{"problem_target_required", "problem_text_required"}},
		{ToolSend, `{"target":" ","text":"hi"}`, []string{"problem_target_required"}},
		{ToolRead, `{"limit":0}`, []string{"problem_target_required", "problem_limit"}},
		{ToolRead, `{"target":"C100","limit":201}`, []string{"problem_limit"}},
		{ToolSearch, `{"after":"soon","before":"later"}`, []string{"problem_query_required", "problem_after", "problem_before"}},
		{ToolSearch, `{"query":"plan","after":"2026-01-02","before":"2026-01-01"}`, []string{"problem_before_after"}},
		{ToolSearch, `{"query":"plan","limit":101}`, []string{"problem_limit"}},
		{ToolReact, `{}`, []string{"problem_message", "problem_emoji_required"}},
		{ToolReact, `{"message":"C100/` + toolReply + `"}`, []string{"problem_emoji_required"}},
		{ToolEdit, `{"message":"#general","text":"hi"}`, []string{"problem_message"}},
		{ToolEdit, `{"message":"C100/` + toolReply + `"}`, []string{"problem_text_required"}},
		{ToolDraft, `{"kind":"shout","text":"hi"}`, []string{"problem_kind"}},
		{ToolDraft, `{}`, []string{"problem_text_required"}},
		{ToolDraft, `{"kind":"react"}`, []string{"problem_reaction_required"}},
	} {
		r := f.call(t, c.tool, c.args)
		if r.Answer != "fix_these" || !r.IsError {
			t.Errorf("%s %s: answer %q error %t", c.tool, c.args, r.Answer, r.IsError)
			continue
		}
		if got := toolProblems(t, r); !slices.Equal(got, c.want) {
			t.Errorf("%s %s: problems %q, want %q", c.tool, c.args, got, c.want)
		}
	}
	if r := f.call(t, ToolRead, `{"target":"C100","limit":0}`); toolFields(t, r)["Problems"].([]any)[0].(map[string]any)["fields"].(map[string]any)["Max"] != float64(readLimitMax) {
		t.Errorf("problem_limit fields %s", r.Fields)
	}
	for _, tool := range []string{ToolSend, ToolRead, ToolSearch, ToolDraft, ToolReact, ToolEdit} {
		if r := f.call(t, tool, `{"unknown":1}`); r.Answer != "bad_arguments" || !r.IsError {
			t.Errorf("%s with an unknown argument: %q", tool, r.Answer)
		}
	}
	if len(f.srv.Calls("chat.postMessage"))+len(f.srv.Calls("reactions.add"))+len(f.srv.Calls("chat.update")) != 0 || len(f.h.Gates()) != 0 {
		t.Fatal("a refused call posted or proposed")
	}
}

func TestCallToolAnswersNotLoggedIn(t *testing.T) {
	t.Parallel()
	f := newToolFixture(t, false)
	for _, c := range []struct{ tool, args string }{
		{ToolSend, `{"target":"C100","text":"hi"}`},
		{ToolRead, `{"target":"C100/` + toolThread + `"}`},
		{ToolSearch, `{"query":"plan"}`},
		{ToolDraft, `{"text":"hi"}`},
		{ToolReact, `{"message":"C100/` + toolReply + `","emoji":"eyes"}`},
		{ToolEdit, `{"message":"C100/` + toolReply + `","text":"hi"}`},
	} {
		if r := f.call(t, c.tool, c.args); r.Answer != "not_logged_in" || !r.IsError {
			t.Errorf("%s: answer %q error %t", c.tool, r.Answer, r.IsError)
		}
	}
	if len(f.srv.Calls("chat.postMessage")) != 0 || len(f.h.Gates()) != 0 || len(f.h.SentDrafts()) != 0 {
		t.Fatal("a logged-out call posted or recorded")
	}
}

func TestCallToolRecordsWhatItPostsAtOnce(t *testing.T) {
	t.Parallel()
	f := newToolFixture(t, true)
	if r := f.call(t, ToolSend, `{"target":"C100","text":"Shipping today"}`); r.Answer != "posted" || r.IsError ||
		toolFields(t, r)["TS"] != "1700000900.000100" {
		t.Fatalf("send: %q %s", r.Answer, r.Fields)
	}
	if r := f.call(t, ToolReact, `{"message":"C100/`+toolReply+`","emoji":":eyes:"}`); r.Answer != "reacted" || toolFields(t, r)["Emoji"] != "eyes" {
		t.Fatalf("react: %q %s", r.Answer, r.Fields)
	}
	if r := f.call(t, ToolEdit, `{"message":"C100/`+toolReply+`","text":"On it, done by noon"}`); r.Answer != "edited" ||
		toolFields(t, r)["Replaced"] != "On it" {
		t.Fatalf("edit: %q %s", r.Answer, r.Fields)
	}
	sent := f.h.SentDrafts()
	if len(sent) != 3 {
		t.Fatalf("recorded %d posts: %+v", len(sent), sent)
	}
	for i, want := range []struct{ tool, purpose, ref string }{
		{ToolSend, sentPurpose, "C100/1700000900.000100"},
		{ToolReact, reactedPurpose, "C100/" + toolReply},
		{ToolEdit, editedPurpose, "C100/" + toolReply},
	} {
		s := sent[i]
		if s.Tool != want.tool || s.Session != toolSession || s.Draft.Purpose != want.purpose || s.Draft.Evidence.Ref != want.ref ||
			s.Draft.State != "sent" || s.Draft.Unit.Provider != string(dslack.Provider) {
			t.Errorf("post %d: %+v", i, s)
		}
	}
}

func TestCallToolDraftOpensTheSessionsGate(t *testing.T) {
	t.Parallel()
	f := newToolFixture(t, true)
	turnUnit := dslack.ThreadUnit(toolTeam, "C100", toolThread)
	r := f.call(t, ToolDraft, `{"text":"Reviewing it now","items":["i1"],"effects":["unit.closed"]}`)
	if r.Answer != "proposed" || r.IsError || toolFields(t, r)["Unit"] != turnUnit.String() {
		t.Fatalf("draft: %q %s", r.Answer, r.Fields)
	}
	gates := f.h.Gates()
	if len(gates) != 1 || gates[0].Session != toolSession || gates[0].Draft.Unit != turnUnit.Wire() || gates[0].Draft.Text != "Reviewing it now" ||
		!slices.Equal(gates[0].Draft.Items, []string{"i1"}) || !slices.Equal(gates[0].Draft.Effects, []string{"unit.closed"}) {
		t.Fatalf("gates %+v", gates)
	}
	if want := int64(1); toolFields(t, r)["ID"] != float64(want) {
		t.Fatalf("draft id %s", r.Fields)
	}

	elsewhere := "C100/" + toolOther
	for args, why := range map[string]string{
		`{"text":"Wrong place","target":"` + elsewhere + `","items":["i1"]}`:            "items:",
		`{"text":"Wrong place","target":"` + elsewhere + `","effects":["unit.closed"]}`: "effects:",
	} {
		r := f.call(t, ToolDraft, args)
		if msg, _ := toolFields(t, r)["Error"].(string); r.Answer != "failed" || !r.IsError || !strings.HasPrefix(msg, why) {
			t.Errorf("%s: %q %s", args, r.Answer, r.Fields)
		}
	}
	if n := len(f.h.Gates()); n != 1 {
		t.Fatalf("a refused draft opened a gate: %d", n)
	}
	r = f.call(t, ToolDraft, `{"text":"Over here","target":"`+elsewhere+`"}`)
	otherUnit := dslack.ThreadUnit(toolTeam, "C100", toolOther)
	if gates := f.h.Gates(); r.Answer != "proposed" || len(gates) != 2 || gates[1].Draft.Unit != otherUnit.Wire() || gates[1].Session != toolSession {
		t.Fatalf("draft elsewhere: %q %s, gates %+v", r.Answer, r.Fields, gates)
	}
}

func TestCallToolReadsAndSearchesAsText(t *testing.T) {
	t.Parallel()
	f := newToolFixture(t, true)
	r := f.call(t, ToolRead, `{"target":"C100/`+toolThread+`"}`)
	if text, _ := toolFields(t, r)["Text"].(string); r.Answer != "read" || r.IsError || !strings.Contains(text, "Can someone review the launch plan?") ||
		!strings.Contains(text, "On it") {
		t.Fatalf("read: %q %s", r.Answer, r.Fields)
	}
	r = f.call(t, ToolSearch, `{"query":"launch plan"}`)
	if text, _ := toolFields(t, r)["Text"].(string); r.Answer != "found" || r.IsError || !strings.Contains(text, "Can someone review the launch plan?") {
		t.Fatalf("search: %q %s", r.Answer, r.Fields)
	}
	if q := f.srv.Calls("search.messages"); len(q) != 1 || !strings.Contains(q[0].Get("query"), "launch plan") {
		t.Fatalf("search calls %v", q)
	}
}
