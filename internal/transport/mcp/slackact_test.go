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
	f := &fakeSlack{loggedIn: true, reaction: send.Reacted{Conversation: "#releases", Channel: "C0000000001", TS: "1700000000.000200", Name: "white_check_mark",
		Permalink: actLink}}
	s, sid := draftSession(t, f, &fakeDrafts{}, "term_1")
	res := decodeResult(t, call(t, s, sid, "term_1", toolCallBody(slackReactName, `{"message":" `+actLink+` ","emoji":":white_check_mark:"}`)))
	if got := res.Result.Content[0].Text; res.Result.IsError || got != "Reacted :white_check_mark: to the message C0000000001/1700000000.000200 in #releases: "+actLink {
		t.Fatalf("%q", got)
	}
	f.reaction = send.Reacted{Conversation: "#releases", Name: "hourglass", Unchanged: true}
	res = decodeResult(t, call(t, s, sid, "term_1", toolCallBody(slackReactName, `{"message":"`+actLink+`","emoji":"hourglass","remove":true}`)))
	if got := res.Result.Content[0].Text; res.Result.IsError || !strings.Contains(got, "nothing changed") {
		t.Fatalf("%q", got)
	}
	want := []send.Reaction{{Message: actLink, Name: "white_check_mark"}, {Message: actLink, Name: "hourglass", Remove: true}}
	if fmt.Sprint(f.reacted) != fmt.Sprint(want) || f.sessions[0] != "term_1" {
		t.Fatalf("reacted %+v by %v", f.reacted, f.sessions)
	}
	for _, args := range []string{`{"emoji":"eyes"}`, `{"message":"#general","emoji":"eyes"}`, `{"message":"` + actLink + `"}`,
		`{"message":"` + actLink + `","emoji":"eyes","text":"x"}`} {
		res := decodeResult(t, call(t, s, sid, "term_1", toolCallBody(slackReactName, args)))
		if got := res.Result.Content[0].Text; !res.Result.IsError || !strings.HasPrefix(got, "Nothing was changed") {
			t.Errorf("%s: %q", args, got)
		}
	}
	f.err = errors.New("slack reactions.add: invalid_name token=xoxp-1111-fake-token")
	res = decodeResult(t, call(t, s, sid, "term_1", toolCallBody(slackReactName, `{"message":"`+actLink+`","emoji":"eyes"}`)))
	if got := res.Result.Content[0].Text; !res.Result.IsError || strings.Contains(got, "xoxp-1111") {
		t.Fatalf("%q", got)
	}
	if len(f.reacted) != 3 {
		t.Fatalf("bad arguments reacted: %+v", f.reacted)
	}
}

func TestSlackEditEditsAtOnce(t *testing.T) {
	f := &fakeSlack{loggedIn: true, edited: send.Edited{Conversation: "#releases", Channel: "C0000000001", TS: "1700000000.000200", Permalink: actLink,
		Text: "release 1.2 *done*", Replaced: "release 1.2 :hourglass:"}}
	s, sid := draftSession(t, f, nil, "term_1")
	res := decodeResult(t, call(t, s, sid, "term_1", toolCallBody(slackEditName, `{"message":"`+actLink+`","text":"release 1.2 **done**"}`)))
	if got := res.Result.Content[0].Text; res.Result.IsError || !strings.HasPrefix(got, "Edited the message C0000000001/1700000000.000200 in #releases: "+actLink) ||
		!strings.Contains(got, "It replaced:\n\nrelease 1.2 :hourglass:") {
		t.Fatalf("%q", got)
	}
	if want := []send.Edit{{Message: actLink, Text: "release 1.2 **done**"}}; fmt.Sprint(f.edits) != fmt.Sprint(want) || f.sessions[0] != "term_1" {
		t.Fatalf("edits %+v by %v", f.edits, f.sessions)
	}
	for _, args := range []string{`{"message":"` + actLink + `","text":" "}`, `{"message":"C1","text":"x"}`,
		`{"message":"` + actLink + `","text":"<!here> look"}`} {
		res := decodeResult(t, call(t, s, sid, "term_1", toolCallBody(slackEditName, args)))
		if !res.Result.IsError || !strings.HasPrefix(res.Result.Content[0].Text, "Nothing was changed") {
			t.Errorf("%s: %+v", args, res.Result)
		}
	}
	if len(f.edits) != 1 {
		t.Fatalf("bad arguments edited: %+v", f.edits[1:])
	}
}

func TestSlackReactAndEditTakeAMessageRef(t *testing.T) {
	const ref = "C0000000001/1700000000.000200"
	f := &fakeSlack{loggedIn: true, reaction: send.Reacted{Conversation: "#releases", Channel: "C0000000001", TS: "1700000000.000200", Name: "eyes"},
		edited: send.Edited{Conversation: "#releases", Channel: "C0000000001", TS: "1700000000.000200", Text: "x", Replaced: "y"}}
	s, sid := draftSession(t, f, nil, "term_1")
	res := decodeResult(t, call(t, s, sid, "term_1", toolCallBody(slackReactName, `{"message":" `+ref+` ","emoji":"eyes"}`)))
	if got := res.Result.Content[0].Text; res.Result.IsError || got != "Reacted :eyes: to the message "+ref+" in #releases" {
		t.Fatalf("%q", got)
	}
	res = decodeResult(t, call(t, s, sid, "term_1", toolCallBody(slackEditName, `{"message":"`+ref+`","text":"x"}`)))
	if got := res.Result.Content[0].Text; res.Result.IsError || !strings.HasPrefix(got, "Edited the message "+ref+" in #releases\n") {
		t.Fatalf("%q", got)
	}
	if len(f.reacted) != 1 || f.reacted[0].Message != ref || len(f.edits) != 1 || f.edits[0].Message != ref {
		t.Fatalf("reacted %+v, edits %+v", f.reacted, f.edits)
	}
	for _, args := range []string{`{"message":"C0000000001/1700000000","emoji":"eyes"}`, `{"message":"U0000000001/1700000000.000200","emoji":"eyes"}`} {
		res := decodeResult(t, call(t, s, sid, "term_1", toolCallBody(slackReactName, args)))
		if got := res.Result.Content[0].Text; !res.Result.IsError || !strings.Contains(got, "message: required, a Slack message ref") {
			t.Errorf("%s: %q", args, got)
		}
	}
}
