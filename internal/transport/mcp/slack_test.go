package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/slack/send"
)

type fakeSlack struct {
	loggedIn bool
	sent     []send.Request
	answer   send.Sent
	err      error
	sessions []string
	reacted  []send.Reaction
	reaction send.Reacted
	edits    []send.Edit
	edited   send.Edited
}

func (f *fakeSlack) LoggedIn() bool { return f.loggedIn }

func (f *fakeSlack) Send(_ context.Context, session string, r send.Request) (send.Sent, error) {
	f.sent, f.sessions = append(f.sent, r), append(f.sessions, session)
	return f.answer, f.err
}

func (f *fakeSlack) Edit(_ context.Context, session string, e send.Edit) (send.Edited, error) {
	f.edits, f.sessions = append(f.edits, e), append(f.sessions, session)
	return f.edited, f.err
}

func (f *fakeSlack) React(_ context.Context, session string, r send.Reaction) (send.Reacted, error) {
	f.reacted, f.sessions = append(f.reacted, r), append(f.sessions, session)
	return f.reaction, f.err
}

const unexpanded = "${ORCA_TERMINAL_HANDLE}"

func listedTools(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	var got struct {
		Result struct {
			Tools []struct {
				Name        string          `json:"name"`
				InputSchema json.RawMessage `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("%v %s", err, rec.Body)
	}
	var names []string
	for _, tl := range got.Result.Tools {
		if len(tl.InputSchema) == 0 {
			t.Errorf("%s has no input schema", tl.Name)
		}
		names = append(names, tl.Name)
	}
	return names
}

func sendCall(args string) string {
	return `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"slack_send_message","arguments":` + args + `}}`
}

func slackSession(t *testing.T, f *fakeSlack) (*Server, string) {
	t.Helper()
	s := &Server{Texts: bundledTexts, Turns: &fakeTurns{}, Port: 47800, Slack: f}
	sid := call(t, s, "", unexpanded, initialize).Header().Get("Mcp-Session-Id")
	if sid == "" {
		t.Fatal("no session id")
	}
	return s, sid
}

func TestSlackToolListedWhenConfigured(t *testing.T) {
	list := `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	bare := &Server{Texts: bundledTexts, Turns: &fakeTurns{}, Port: 47800}
	sid := call(t, bare, "", "term_1", initialize).Header().Get("Mcp-Session-Id")
	if got := listedTools(t, call(t, bare, sid, "term_1", list)); !slices.Equal(got, []string{"turn_end"}) {
		t.Fatalf("without Slack: %v", got)
	}
	for _, in := range []bool{false, true} {
		s, sid := slackSession(t, &fakeSlack{loggedIn: in})
		if got := listedTools(t, call(t, s, sid, unexpanded, list)); !slices.Equal(got, []string{"turn_end", "slack_send_message", "slack_react", "slack_edit"}) {
			t.Fatalf("logged in %v: %v", in, got)
		}
	}
	in := slackSendTool().InputSchema
	if !slices.Equal(in.Required, []string{"target", "text"}) || len(in.Properties) != 4 || in.AdditionalProperties == nil || *in.AdditionalProperties {
		t.Fatalf("schema %+v", in)
	}
}

func TestSlackToolLoggedOutNamesLogin(t *testing.T) {
	f := &fakeSlack{}
	s, sid := slackSession(t, f)
	r := decodeResult(t, call(t, s, sid, unexpanded, sendCall(`{"target":"#general","text":"hi"}`)))
	want := `not logged in to Slack; run "` + brand.Command + ` slack login"`
	if !r.Result.IsError || !strings.Contains(r.Result.Content[0].Text, want) {
		t.Fatalf("%+v", r.Result)
	}
	if len(f.sent) != 0 {
		t.Fatal("Send was called while logged out")
	}

	f.loggedIn = true
	f.err = fmt.Errorf("%w: invalid_auth", send.ErrNotLoggedIn)
	r = decodeResult(t, call(t, s, sid, unexpanded, sendCall(`{"target":"#general","text":"hi"}`)))
	if !r.Result.IsError || !strings.Contains(r.Result.Content[0].Text, want) {
		t.Fatalf("expired token: %+v", r.Result)
	}
}

func TestSlackSendMessageCall(t *testing.T) {
	f := &fakeSlack{loggedIn: true, answer: send.Sent{
		Conversation: "#general", Parent: "the release plan", Text: "*hi*",
		Permalink: "https://acme.slack.com/archives/C000/p1", Channel: "C000", TS: "1.000",
	}}
	s, sid := slackSession(t, f)
	r := decodeResult(t, call(t, s, sid, unexpanded, sendCall(`{"target":"https://acme.slack.com/archives/C000/p1","text":"**hi**","thread":false,"broadcast":true}`)))
	if r.Result.IsError {
		t.Fatalf("%+v", r.Result)
	}
	no := false
	want := send.Request{Target: "https://acme.slack.com/archives/C000/p1", Text: "**hi**", Thread: &no, Broadcast: true, Source: "session"}
	if len(f.sent) != 1 || f.sent[0].Target != want.Target || f.sent[0].Text != want.Text || f.sent[0].Thread == nil || *f.sent[0].Thread || !f.sent[0].Broadcast || f.sent[0].Source != "session" {
		t.Fatalf("sent %+v", f.sent)
	}
	if got := r.Result.Content[0].Text; got != "Posted to #general in the thread \"the release plan\": https://acme.slack.com/archives/C000/p1\n\n*hi*" {
		t.Fatalf("%q", got)
	}

	f.answer = send.Sent{Conversation: "@ana", Text: "hi", Channel: "D000", TS: "2.000"}
	r = decodeResult(t, call(t, s, sid, unexpanded, sendCall(`{"target":"@ana","text":"hi"}`)))
	if f.sent[1].Thread != nil {
		t.Fatalf("an omitted thread was set: %+v", f.sent[1])
	}
	if got := r.Result.Content[0].Text; got != "Posted to @ana: no permalink (D000/2.000)\n\nhi" {
		t.Fatalf("empty permalink: %q", got)
	}
}

func TestSlackSendMessageRefusesBadArguments(t *testing.T) {
	f := &fakeSlack{loggedIn: true}
	s, sid := slackSession(t, f)
	for args, want := range map[string]string{
		`{"target":"#general","text":"hi","channel":"C1"}`: "channel",
		`{"target":"#general","text":"  "}`:                "text",
		`{"target":"#general"}`:                            "text",
		`{"target":"","text":"hi"}`:                        "target",
		`{"target":"#general","text":7}`:                   "text",
		`[]`:                                               "object",
	} {
		r := decodeResult(t, call(t, s, sid, unexpanded, sendCall(args)))
		if !r.Result.IsError || !strings.Contains(r.Result.Content[0].Text, want) {
			t.Errorf("%s: %+v", args, r.Result)
		}
	}
	if len(f.sent) != 0 {
		t.Fatalf("sent %+v", f.sent)
	}
}

func TestSlackSendMessageReportsFailure(t *testing.T) {
	f := &fakeSlack{loggedIn: true, err: fmt.Errorf("%w: channel_not_found token=xoxp-1111-fake-token", send.ErrCannotPost)}
	s, sid := slackSession(t, f)
	r := decodeResult(t, call(t, s, sid, unexpanded, sendCall(`{"target":"#general","text":"hi"}`)))
	got := r.Result.Content[0].Text
	if !r.Result.IsError || !strings.Contains(got, "channel_not_found") {
		t.Fatalf("%q", got)
	}
	if strings.Contains(got, "xoxp-1111") {
		t.Fatalf("the token reached the result: %q", got)
	}
}

func TestRedactTokensCoversEveryKind(t *testing.T) {
	for _, tok := range []string{"xoxp-1111-fake", "xoxb-2222-fake", "xapp-1-A000-3333-fake", "xoxe-1-fake", "xoxr-fake", "xoxe.xoxp-1-fake"} {
		if got := redactTokens("token=" + tok + " end"); got != "token=[redacted] end" {
			t.Errorf("%s: %q", tok, got)
		}
	}
}

func TestSlackSendMessageRefusesBroadcasts(t *testing.T) {
	f := &fakeSlack{loggedIn: true}
	s, sid := slackSession(t, f)
	for _, msg := range []string{"<!channel> deploy", "hi <!here>", "<!everyone|everyone>", "cc <!subteam^S000|@core>", "<!HERE>"} {
		args, _ := json.Marshal(map[string]string{"target": "#general", "text": msg})
		r := decodeResult(t, call(t, s, sid, unexpanded, sendCall(string(args))))
		if !r.Result.IsError || !strings.Contains(r.Result.Content[0].Text, "post it themselves") {
			t.Errorf("%q: %+v", msg, r.Result)
		}
	}
	if len(f.sent) != 0 {
		t.Fatalf("sent %+v", f.sent)
	}
}
