package mcp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/slack/send"
)

type readCall struct {
	target string
	limit  int
}

type fakeReader struct {
	calls  []readCall
	answer string
	err    error
}

func (f *fakeReader) Read(_ context.Context, target string, limit int) (string, error) {
	f.calls = append(f.calls, readCall{target, limit})
	return f.answer, f.err
}

func readCallBody(args string) string {
	return `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"slack_read","arguments":` + args + `}}`
}

func readSession(t *testing.T, f *fakeSlack, r *fakeReader) (*Server, string) {
	t.Helper()
	s := &Server{Turns: &fakeTurns{}, Port: 47800, Slack: f, SlackRead: r}
	sid := call(t, s, "", unexpanded, initialize).Header().Get("Mcp-Session-Id")
	if sid == "" {
		t.Fatal("no session id")
	}
	return s, sid
}

func TestSlackReadListing(t *testing.T) {
	list := `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	s, sid := slackSession(t, &fakeSlack{loggedIn: true})
	if got := listedTools(t, call(t, s, sid, unexpanded, list)); slices.Contains(got, "slack_read") {
		t.Fatalf("SlackRead nil lists %v", got)
	}
	r := &fakeReader{answer: "rendered"}
	f := &fakeSlack{}
	s, sid = readSession(t, f, r)
	if got := listedTools(t, call(t, s, sid, unexpanded, list)); !slices.Equal(got, []string{"turn_end", "slack_send_message", "slack_read"}) {
		t.Fatalf("tools %v", got)
	}
	in := slackReadTool().InputSchema
	if !slices.Equal(in.Required, []string{"target"}) || len(in.Properties) != 2 || in.AdditionalProperties == nil || *in.AdditionalProperties {
		t.Fatalf("schema %+v", in)
	}

	want := `not logged in to Slack; run "` + brand.Command + ` slack login"`
	res := decodeResult(t, call(t, s, sid, unexpanded, readCallBody(`{"target":"slack:T1/C1/1.2"}`)))
	if !res.Result.IsError || !strings.Contains(res.Result.Content[0].Text, want) {
		t.Fatalf("logged out: %+v", res.Result)
	}
	if len(r.calls) != 0 {
		t.Fatal("Read was called while logged out")
	}

	f.loggedIn = true
	r.err = fmt.Errorf("%w: token_expired", send.ErrNotLoggedIn)
	res = decodeResult(t, call(t, s, sid, unexpanded, readCallBody(`{"target":"slack:T1/C1/1.2"}`)))
	if !res.Result.IsError || !strings.Contains(res.Result.Content[0].Text, want) {
		t.Fatalf("expired: %+v", res.Result)
	}

	r.err = nil
	res = decodeResult(t, call(t, s, sid, unexpanded, readCallBody(`{"target":" slack:T1/C1/1.2 "}`)))
	if res.Result.IsError || res.Result.Content[0].Text != "rendered" {
		t.Fatalf("%+v", res.Result)
	}
	res = decodeResult(t, call(t, s, sid, unexpanded, readCallBody(`{"target":"https://acme.slack.com/archives/C1/p1","limit":200}`)))
	if res.Result.IsError {
		t.Fatalf("%+v", res.Result)
	}
	if want := []readCall{{"slack:T1/C1/1.2", 50}, {"slack:T1/C1/1.2", 50}, {"https://acme.slack.com/archives/C1/p1", 200}}; !slices.Equal(r.calls, want) {
		t.Fatalf("calls %v, want %v", r.calls, want)
	}
}

func TestSlackReadRefusesBadArguments(t *testing.T) {
	r := &fakeReader{answer: "rendered"}
	s, sid := readSession(t, &fakeSlack{loggedIn: true}, r)
	for args, want := range map[string]string{
		`{"target":"slack:T1/C1/1.2","channel":"C1"}`: "channel",
		`{"target":"slack:T1/C1/1.2","limit":201}`:    "limit",
		`{"target":"slack:T1/C1/1.2","limit":0}`:      "limit",
		`{"target":"slack:T1/C1/1.2","limit":2.5}`:    "limit",
		`{"target":"slack:T1/C1/1.2","limit":"9"}`:    "limit",
		`{"target":"  "}`:                             "target",
		`{}`:                                          "target",
		`[]`:                                          "object",
	} {
		res := decodeResult(t, call(t, s, sid, unexpanded, readCallBody(args)))
		if !res.Result.IsError || !strings.Contains(res.Result.Content[0].Text, want) {
			t.Errorf("%s: %+v", args, res.Result)
		}
	}
	if len(r.calls) != 0 {
		t.Fatalf("read %v", r.calls)
	}
}

func TestSlackReadHoldsNoToken(t *testing.T) {
	r := &fakeReader{err: errors.New("slack conversations.replies: channel_not_found token=xoxp-1111-fake-token")}
	s, sid := readSession(t, &fakeSlack{loggedIn: true}, r)
	res := decodeResult(t, call(t, s, sid, unexpanded, readCallBody(`{"target":"slack:T1/C1/1.2"}`)))
	got := res.Result.Content[0].Text
	if !res.Result.IsError || !strings.Contains(got, "channel_not_found") || !strings.HasPrefix(got, "Slack read failed: ") {
		t.Fatalf("%q", got)
	}
	if strings.Contains(got, "xoxp-1111") {
		t.Fatalf("the token reached the result: %q", got)
	}
}
