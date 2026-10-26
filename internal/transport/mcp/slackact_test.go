package mcp

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

const actLink = "https://acme.slack.com/archives/C0000000001/p1700000000000200"

func toolCallBody(name, args string) string {
	return `{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
}

func TestSlackReactAndEditAreListedWithSlackDraft(t *testing.T) {
	list := `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	s, sid := draftSession(t, &fakeSlack{loggedIn: true}, &fakeDrafts{}, "term_1")
	got := listedTools(t, call(t, s, sid, "term_1", list))
	for _, name := range []string{slackReactName, slackEditName} {
		if !slices.Contains(got, name) {
			t.Fatalf("tools %v lack %s", got, name)
		}
	}
	s, sid = draftSession(t, &fakeSlack{loggedIn: true}, nil, "term_1")
	got = listedTools(t, call(t, s, sid, "term_1", list))
	if slices.Contains(got, slackReactName) || slices.Contains(got, slackEditName) {
		t.Fatalf("SlackDraft nil lists %v", got)
	}
}

func TestSlackReactAndEditProposeDrafts(t *testing.T) {
	d := &fakeDrafts{answer: DraftResult{ID: 5, Unit: "slack T0/C1/1.2", Conversation: "#releases", State: "proposed"}}
	s, sid := draftSession(t, &fakeSlack{loggedIn: true}, d, "term_1")
	for _, c := range []struct{ name, args string }{
		{slackReactName, `{"permalink":" ` + actLink + ` ","emoji":":white_check_mark:"}`},
		{slackEditName, `{"permalink":"` + actLink + `","text":"release 1.2 done"}`},
	} {
		res := decodeResult(t, call(t, s, sid, "term_1", toolCallBody(c.name, c.args)))
		if got := res.Result.Content[0].Text; res.Result.IsError || !strings.Contains(got, "draft 5") || !strings.Contains(got, "approves") {
			t.Fatalf("%s: %q", c.name, got)
		}
	}
	want := []draftCall{
		{"term_1", DraftArgs{Purpose: "react", Delivery: "reaction", Target: actLink, Reaction: "white_check_mark"}},
		{"term_1", DraftArgs{Purpose: "edit", Delivery: "edit", Target: actLink, Text: "release 1.2 done"}},
	}
	if fmt.Sprint(d.calls) != fmt.Sprint(want) {
		t.Fatalf("calls %+v", d.calls)
	}

	for _, c := range []struct{ name, args, want string }{
		{slackReactName, `{"emoji":"eyes"}`, "permalink"},
		{slackReactName, `{"permalink":"#general","emoji":"eyes"}`, "permalink"},
		{slackReactName, `{"permalink":"` + actLink + `"}`, "emoji"},
		{slackReactName, `{"permalink":"` + actLink + `","emoji":"eyes","text":"x"}`, "text"},
		{slackEditName, `{"permalink":"` + actLink + `","text":" "}`, "text"},
		{slackEditName, `{"permalink":"C1","text":"x"}`, "permalink"},
	} {
		res := decodeResult(t, call(t, s, sid, "term_1", toolCallBody(c.name, c.args)))
		if !res.Result.IsError || !strings.Contains(res.Result.Content[0].Text, c.want) {
			t.Errorf("%s %s: %+v", c.name, c.args, res.Result)
		}
	}
	if len(d.calls) != 2 {
		t.Fatalf("bad arguments drafted: %+v", d.calls[2:])
	}
}
