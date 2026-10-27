package mcp

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/slack/send"
)

const actLink = "https://acme.slack.com/archives/C0000000001/p1700000000000200"

func toolCallBody(name, args string) string {
	return `{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
}

func TestSlackReactPostsAtOnce(t *testing.T) {
	f := &fakeSlack{loggedIn: true, reaction: send.Reacted{Conversation: "#releases", Name: "white_check_mark"}}
	s, sid := draftSession(t, f, &fakeDrafts{}, "term_1")
	res := decodeResult(t, call(t, s, sid, "term_1", toolCallBody(slackReactName, `{"permalink":" `+actLink+` ","emoji":":white_check_mark:"}`)))
	if got := res.Result.Content[0].Text; res.Result.IsError || got != "Reacted :white_check_mark: to the message in #releases: "+actLink {
		t.Fatalf("%q", got)
	}
	f.reaction = send.Reacted{Conversation: "#releases", Name: "hourglass", Unchanged: true}
	res = decodeResult(t, call(t, s, sid, "term_1", toolCallBody(slackReactName, `{"permalink":"`+actLink+`","emoji":"hourglass","remove":true}`)))
	if got := res.Result.Content[0].Text; res.Result.IsError || !strings.Contains(got, "nothing changed") {
		t.Fatalf("%q", got)
	}
	want := []send.Reaction{{Permalink: actLink, Name: "white_check_mark"}, {Permalink: actLink, Name: "hourglass", Remove: true}}
	if fmt.Sprint(f.reacted) != fmt.Sprint(want) || f.sessions[0] != "term_1" {
		t.Fatalf("reacted %+v by %v", f.reacted, f.sessions)
	}
	for _, args := range []string{`{"emoji":"eyes"}`, `{"permalink":"#general","emoji":"eyes"}`, `{"permalink":"` + actLink + `"}`,
		`{"permalink":"` + actLink + `","emoji":"eyes","text":"x"}`} {
		res := decodeResult(t, call(t, s, sid, "term_1", toolCallBody(slackReactName, args)))
		if got := res.Result.Content[0].Text; !res.Result.IsError || !strings.HasPrefix(got, "Nothing was changed") {
			t.Errorf("%s: %q", args, got)
		}
	}
	f.err = errors.New("slack reactions.add: invalid_name token=xoxp-1111-fake-token")
	res = decodeResult(t, call(t, s, sid, "term_1", toolCallBody(slackReactName, `{"permalink":"`+actLink+`","emoji":"eyes"}`)))
	if got := res.Result.Content[0].Text; !res.Result.IsError || strings.Contains(got, "xoxp-1111") {
		t.Fatalf("%q", got)
	}
	if len(f.reacted) != 3 {
		t.Fatalf("bad arguments reacted: %+v", f.reacted)
	}
}

func TestSlackEditEditsAtOnce(t *testing.T) {
	f := &fakeSlack{loggedIn: true, edited: send.Edited{Conversation: "#releases", Text: "release 1.2 *done*", Replaced: "release 1.2 :hourglass:"}}
	s, sid := draftSession(t, f, nil, "term_1")
	res := decodeResult(t, call(t, s, sid, "term_1", toolCallBody(slackEditName, `{"permalink":"`+actLink+`","text":"release 1.2 **done**"}`)))
	if got := res.Result.Content[0].Text; res.Result.IsError || !strings.HasPrefix(got, "Edited the message in #releases: "+actLink) ||
		!strings.Contains(got, "It replaced:\n\nrelease 1.2 :hourglass:") {
		t.Fatalf("%q", got)
	}
	if want := []send.Edit{{Permalink: actLink, Text: "release 1.2 **done**"}}; fmt.Sprint(f.edits) != fmt.Sprint(want) || f.sessions[0] != "term_1" {
		t.Fatalf("edits %+v by %v", f.edits, f.sessions)
	}
	for _, args := range []string{`{"permalink":"` + actLink + `","text":" "}`, `{"permalink":"C1","text":"x"}`,
		`{"permalink":"` + actLink + `","text":"<!here> look"}`} {
		res := decodeResult(t, call(t, s, sid, "term_1", toolCallBody(slackEditName, args)))
		if !res.Result.IsError || !strings.HasPrefix(res.Result.Content[0].Text, "Nothing was changed") {
			t.Errorf("%s: %+v", args, res.Result)
		}
	}
	if len(f.edits) != 1 {
		t.Fatalf("bad arguments edited: %+v", f.edits[1:])
	}
}
