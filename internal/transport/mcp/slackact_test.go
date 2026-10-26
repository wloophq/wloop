package mcp

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/slack/send"
)

const actLink = "https://acme.slack.com/archives/C0000000001/p1700000000000200"

func toolCallBody(name, args string) string {
	return `{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
}

func TestSlackEditIsListedWithSlackDraft(t *testing.T) {
	list := `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	s, sid := draftSession(t, &fakeSlack{loggedIn: true}, &fakeDrafts{}, "term_1")
	if got := listedTools(t, call(t, s, sid, "term_1", list)); !slices.Contains(got, slackEditName) {
		t.Fatalf("tools %v lack %s", got, slackEditName)
	}
	s, sid = draftSession(t, &fakeSlack{loggedIn: true}, nil, "term_1")
	if got := listedTools(t, call(t, s, sid, "term_1", list)); slices.Contains(got, slackEditName) {
		t.Fatalf("SlackDraft nil lists %v", got)
	}
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

func TestSlackEditProposesADraft(t *testing.T) {
	d := &fakeDrafts{answer: DraftResult{ID: 5, Unit: "slack T0/C1/1.2", Conversation: "#releases", State: "proposed"}}
	s, sid := draftSession(t, &fakeSlack{loggedIn: true}, d, "term_1")
	res := decodeResult(t, call(t, s, sid, "term_1", toolCallBody(slackEditName, `{"permalink":"`+actLink+`","text":"release 1.2 done"}`)))
	if got := res.Result.Content[0].Text; res.Result.IsError || !strings.Contains(got, "draft 5") || !strings.Contains(got, "approves") {
		t.Fatalf("%q", got)
	}
	want := []draftCall{{"term_1", DraftArgs{Purpose: "edit", Delivery: "edit", Target: actLink, Text: "release 1.2 done"}}}
	if fmt.Sprint(d.calls) != fmt.Sprint(want) {
		t.Fatalf("calls %+v", d.calls)
	}
	for _, args := range []string{`{"permalink":"` + actLink + `","text":" "}`, `{"permalink":"C1","text":"x"}`} {
		res := decodeResult(t, call(t, s, sid, "term_1", toolCallBody(slackEditName, args)))
		if !res.Result.IsError || !strings.HasPrefix(res.Result.Content[0].Text, "Nothing was drafted") {
			t.Errorf("%s: %+v", args, res.Result)
		}
	}
	if len(d.calls) != 1 {
		t.Fatalf("bad arguments drafted: %+v", d.calls[1:])
	}
}
