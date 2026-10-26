package mcp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/slack/send"
)

type draftCall struct {
	handle string
	args   DraftArgs
}

type fakeDrafts struct {
	calls  []draftCall
	answer DraftResult
	err    error
}

func (f *fakeDrafts) Draft(_ context.Context, handle string, a DraftArgs) (DraftResult, error) {
	f.calls = append(f.calls, draftCall{handle, a})
	return f.answer, f.err
}

func draftCallBody(args string) string {
	return `{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"slack_draft","arguments":` + args + `}}`
}

func draftSession(t *testing.T, f *fakeSlack, d *fakeDrafts, handle string) (*Server, string) {
	t.Helper()
	s := &Server{Turns: &fakeTurns{}, Port: 47800, Slack: f}
	if d != nil {
		s.SlackDraft = d
	}
	sid := call(t, s, "", handle, initialize).Header().Get("Mcp-Session-Id")
	if sid == "" {
		t.Fatal("no session id")
	}
	return s, sid
}

func TestSlackDraftListing(t *testing.T) {
	list := `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	s, sid := draftSession(t, &fakeSlack{loggedIn: true}, nil, "term_1")
	if got := listedTools(t, call(t, s, sid, "term_1", list)); slices.Contains(got, slackDraftName) {
		t.Fatalf("SlackDraft nil lists %v", got)
	}
	f, d := &fakeSlack{}, &fakeDrafts{answer: DraftResult{ID: 3}}
	s, sid = draftSession(t, f, d, "term_1")
	if got := listedTools(t, call(t, s, sid, "term_1", list)); !slices.Equal(got, []string{"turn_end", "slack_send_message", slackDraftName}) {
		t.Fatalf("tools %v", got)
	}
	in := slackDraftTool().InputSchema
	if len(in.Required) != 0 || len(in.Properties) != 6 || in.AdditionalProperties == nil || *in.AdditionalProperties {
		t.Fatalf("schema %+v", in)
	}

	want := `not logged in to Slack; run "` + brand.Command + ` slack login"`
	res := decodeResult(t, call(t, s, sid, "term_1", draftCallBody(`{"text":"hi"}`)))
	if !res.Result.IsError || !strings.Contains(res.Result.Content[0].Text, want) || len(d.calls) != 0 {
		t.Fatalf("logged out: %+v, %d calls", res.Result, len(d.calls))
	}
	f.loggedIn = true
	d.err = fmt.Errorf("invalid draft: %w: token_expired", send.ErrNotLoggedIn)
	res = decodeResult(t, call(t, s, sid, "term_1", draftCallBody(`{"text":"hi"}`)))
	if !res.Result.IsError || !strings.Contains(res.Result.Content[0].Text, want) {
		t.Fatalf("expired: %+v", res.Result)
	}
}

func TestSlackDraftNeedsAHandle(t *testing.T) {
	d := &fakeDrafts{}
	s, sid := draftSession(t, &fakeSlack{loggedIn: true}, d, unexpanded)
	res := decodeResult(t, call(t, s, sid, unexpanded, draftCallBody(`{"text":"hi"}`)))
	want := slackDraftName + " refused: " + api.ErrSessionHandle.Error()
	if !res.Result.IsError || !strings.HasPrefix(res.Result.Content[0].Text, want) || !strings.Contains(res.Result.Content[0].Text, "X-Loop-Session") {
		t.Fatalf("%+v", res.Result)
	}
	if len(d.calls) != 0 {
		t.Fatalf("drafted %v", d.calls)
	}
}

func TestSlackDraftPassesTheArguments(t *testing.T) {
	d := &fakeDrafts{answer: DraftResult{ID: 42, Unit: "slack T0/C1/1.2", Conversation: "#general", State: "proposed"}}
	s, sid := draftSession(t, &fakeSlack{loggedIn: true}, d, "term_1")
	res := decodeResult(t, call(t, s, sid, "term_1", draftCallBody(`{"text":"on it","target":" C1 ","items":["i1"]}`)))
	got := res.Result.Content[0].Text
	if res.Result.IsError || !strings.Contains(got, "draft 42") || !strings.Contains(got, "#general") || !strings.Contains(got, "nothing was posted") {
		t.Fatalf("%q", got)
	}
	res = decodeResult(t, call(t, s, sid, "term_1", draftCallBody(`{"kind":"react","reaction":"eyes"}`)))
	if res.Result.IsError {
		t.Fatalf("%+v", res.Result)
	}
	want := []draftCall{
		{"term_1", DraftArgs{Text: "on it", Kind: "reply", Target: "C1", Items: []string{"i1"}}},
		{"term_1", DraftArgs{Kind: "react", Reaction: "eyes"}},
	}
	if len(d.calls) != 2 || fmt.Sprint(d.calls) != fmt.Sprint(want) {
		t.Fatalf("calls %+v", d.calls)
	}

	for args, want := range map[string]string{
		`{"text":"hi","origin":"user"}`:   "origin",
		`{"text":"hi","approval":"none"}`: "approval",
		`{"text":"hi","turn_seq":3}`:      "turn_seq",
		`{"text":"  "}`:                   "text",
		`{"kind":"react"}`:                "reaction",
		`{"text":"hi","kind":"comment"}`:  "kind",
		`{"text":"hi","items":"i1"}`:      "items",
		`[]`:                              "object",
	} {
		res := decodeResult(t, call(t, s, sid, "term_1", draftCallBody(args)))
		if !res.Result.IsError || !strings.Contains(res.Result.Content[0].Text, want) {
			t.Errorf("%s: %+v", args, res.Result)
		}
	}
	if len(d.calls) != 2 {
		t.Fatalf("bad arguments drafted: %+v", d.calls[2:])
	}

	d.err = errors.New("slack conversations.info: channel_not_found token=xoxp-1111-fake-token")
	res = decodeResult(t, call(t, s, sid, "term_1", draftCallBody(`{"text":"hi"}`)))
	if got := res.Result.Content[0].Text; !res.Result.IsError || !strings.HasPrefix(got, "Nothing was drafted: ") || strings.Contains(got, "xoxp-1111") {
		t.Fatalf("%q", got)
	}
}
