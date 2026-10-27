package mcp

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
)

type fakeSearcher struct {
	calls  []SearchArgs
	answer string
	err    error
}

func (f *fakeSearcher) Search(_ context.Context, a SearchArgs) (string, error) {
	f.calls = append(f.calls, a)
	return f.answer, f.err
}

func TestSlackSearchPassesTheFilters(t *testing.T) {
	f := &fakeSearcher{answer: "1 of 1 match for release"}
	s := &Server{Texts: bundledTexts, Turns: &fakeTurns{}, Port: 47800, Slack: &fakeSlack{loggedIn: true}, SlackSearch: f}
	sid := call(t, s, "", unexpanded, initialize).Header().Get("Mcp-Session-Id")
	if got := listedTools(t, call(t, s, sid, unexpanded, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)); !slices.Contains(got, slackSearchName) {
		t.Fatalf("tools %v", got)
	}
	body := func(args string) string { return toolCallBody(slackSearchName, args) }
	res := decodeResult(t, call(t, s, sid, unexpanded, body(`{"query":" release ","channel":"#releases","from":"@dana","after":"2026-10-01","before":"2026-10-27","limit":5}`)))
	if res.Result.IsError || res.Result.Content[0].Text != "1 of 1 match for release" {
		t.Fatalf("%+v", res.Result)
	}
	decodeResult(t, call(t, s, sid, unexpanded, body(`{"query":"release"}`)))
	want := []SearchArgs{
		{Query: "release", Channel: "#releases", From: "@dana", After: "2026-10-01", Before: "2026-10-27", Limit: 5},
		{Query: "release", Limit: 20},
	}
	if fmt.Sprint(f.calls) != fmt.Sprint(want) {
		t.Fatalf("calls %+v", f.calls)
	}
	for args, want := range map[string]string{
		`{}`:                                  "query",
		`{"query":"  "}`:                      "query",
		`{"query":"x","after":"yesterday"}`:   "after",
		`{"query":"x","before":"2026-13-01"}`: "before",
		`{"query":"x","limit":0}`:             "limit",
		`{"query":"x","limit":101}`:           "limit",
		`{"query":"x","in":"#releases"}`:      "in",
		`{"query":"x","after":"2026-10-02","before":"2026-10-01"}`: "before",
	} {
		res := decodeResult(t, call(t, s, sid, unexpanded, body(args)))
		if !res.Result.IsError || !strings.Contains(res.Result.Content[0].Text, want) {
			t.Errorf("%s: %+v", args, res.Result)
		}
	}
	if len(f.calls) != 2 {
		t.Fatalf("bad arguments searched: %+v", f.calls[2:])
	}
	f.err = fmt.Errorf("slack search.messages: ratelimited token=xoxp-1111-fake-token")
	res = decodeResult(t, call(t, s, sid, unexpanded, body(`{"query":"x"}`)))
	if got := res.Result.Content[0].Text; !res.Result.IsError || strings.Contains(got, "xoxp-1111") {
		t.Fatalf("%q", got)
	}
}
