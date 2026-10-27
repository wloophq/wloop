package mcp

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/slack/send"
)

func describedTools(t *testing.T, s *Server) map[string]tool {
	t.Helper()
	sid := call(t, s, "", "term_1", initialize).Header().Get("Mcp-Session-Id")
	rec := call(t, s, sid, "term_1", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	var got struct {
		Result struct {
			Tools []tool `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("%v %s", err, rec.Body)
	}
	out := map[string]tool{}
	for _, tl := range got.Result.Tools {
		out[tl.Name] = tl
	}
	return out
}

func TestToolsTakeTheirTextsFromTheProfile(t *testing.T) {
	var logged []string
	s := &Server{Turns: &fakeTurns{}, Port: 47800, Slack: &fakeSlack{loggedIn: true}, SlackRead: &fakeReader{},
		Log: func(f string, a ...any) {
			if l := fmt.Sprintf(f, a...); strings.HasPrefix(l, "mcp: tools.toml") {
				logged = append(logged, l)
			}
		},
		Texts: func() map[string]ToolText {
			return map[string]ToolText{
				"turn_end":   {Description: "End a {{.Name}} turn.", Params: map[string]string{"record.state": "how it ended", "record.ledger[].anchor.ref": "the message"}},
				"slack_read": {Description: "Read Slack.", Params: map[string]string{"target": "a permalink", "nope": "x"}},
			}
		}}
	tools := describedTools(t, s)
	end := tools["turn_end"]
	rec := end.InputSchema.Properties["record"]
	if end.Description != "End a "+brand.Name+" turn." || rec.Properties["state"].Description != "how it ended" ||
		rec.Properties["ledger"].Items.Properties["anchor"].Properties["ref"].Description != "the message" {
		t.Fatalf("turn_end %+v", end)
	}
	if r := tools["slack_read"]; r.Description != "Read Slack." || r.InputSchema.Properties["target"].Description != "a permalink" {
		t.Fatalf("slack_read %+v", r)
	}
	if tools["slack_send_message"].Description != "" {
		t.Fatalf("a tool with no text got %q", tools["slack_send_message"].Description)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "slack_read.params.nope names no parameter") {
		t.Fatalf("logged %q", logged)
	}
	describedTools(t, s)
	if len(logged) != 1 {
		t.Fatalf("logged twice: %q", logged)
	}
}

func TestAnswersComeFromTheProfile(t *testing.T) {
	var logged []string
	texts := map[string]ToolText{"slack_send_message": {Answers: map[string]string{
		"posted": "Sent to {{.Conversation}} by {{.Name}}; check it shows: {{.Permalink}}",
	}}}
	f := &fakeSlack{loggedIn: true, answer: send.Sent{Conversation: "#releases", Permalink: "https://acme.slack.com/archives/C1/p1"}}
	s := &Server{Turns: &fakeTurns{}, Port: 47800, Slack: f, Texts: func() map[string]ToolText { return texts },
		Log: func(format string, a ...any) { logged = append(logged, fmt.Sprintf(format, a...)) }}
	sid := call(t, s, "", "term_1", initialize).Header().Get("Mcp-Session-Id")
	res := decodeResult(t, call(t, s, sid, "term_1", toolCallBody(slackSendName, `{"target":"#releases","text":"hi"}`)))
	if got := res.Result.Content[0].Text; got != "Sent to #releases by "+brand.Name+"; check it shows: https://acme.slack.com/archives/C1/p1" {
		t.Fatalf("%q", got)
	}
	res = decodeResult(t, call(t, s, sid, "term_1", toolCallBody(slackSendName, `{"target":"#releases","text":" "}`)))
	if got := res.Result.Content[0].Text; !res.Result.IsError || !strings.HasPrefix(got, "fix_these ") {
		t.Fatalf("an answer the profile lacks: %q", got)
	}
	if !slices.ContainsFunc(logged, func(l string) bool {
		return strings.Contains(l, "tools.toml has no slack_send_message.answers.fix_these")
	}) {
		t.Fatalf("logged %q", logged)
	}
}
