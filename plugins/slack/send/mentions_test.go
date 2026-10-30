package send_test

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/wloophq/wloop/plugins/slack/send"
)

func slackToken(id string) string { return "<@" + id + ">" }

func TestMentionsResolveHandlesEmailsAndUserIDs(t *testing.T) {
	f := newFixture(t, newWorkspace())
	for in, want := range map[string]string{
		"@alice, can you look?":                           "<@U000000001>, can you look?",
		"ask @bobby and bob@example.com.":                 "ask <@U000000002> and <@U000000002>.",
		"cc @U000000003":                                  "cc <@U000000003>",
		"@Alice: done":                                    "<@U000000001>: done",
		"`@alice` and\n```\n@alice\n```\nstay":            "`@alice` and\n```\n@alice\n```\nstay",
		"[mail](mailto:alice@example.com) <@U000000002>":  "[mail](mailto:alice@example.com) <@U000000002>",
		"@nobody and ghost@example.com stay as text":      "@nobody and ghost@example.com stay as text",
		"@here and @channel are not people":               "@here and @channel are not people",
		"scoped @types/node and @{slack:U000000001} stay": "scoped @types/node and @{slack:U000000001} stay",
		"a deactivated @gone stays":                       "a deactivated @gone stays",
		"@WIP hold, deploy at @UTC midnight":              "@WIP hold, deploy at @UTC midnight",
		"see https://social.example/@alice now":           "see https://social.example/@alice now",
		"[@alice](https://x.example) and <@U000000001>":   "[@alice](https://x.example) and <@U000000001>",
	} {
		got, err := f.svc.Mentions(context.Background(), in, slackToken)
		if err != nil || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, err, want)
		}
	}
	if _, err := f.svc.Mentions(context.Background(), "hi @sam", slackToken); !errors.Is(err, send.ErrAmbiguous) || !strings.Contains(err.Error(), "U000000003") {
		t.Fatalf("two users named sam: %v", err)
	}
}

func TestTypedUserMentionsBecomeMentions(t *testing.T) {
	f := newFixture(t, newWorkspace())
	draftMention := func(user string) string { return "@{slack:" + user + "}" }
	for in, want := range map[string]string{
		"<@U000000001> re-review done":                       "@{slack:U000000001} re-review done",
		"<@W0000000042|Ana Souza>, see this":                 "@{slack:W0000000042}, see this",
		"<@U000000001|@here> and <@U000000002|!channel>":     "@{slack:U000000001} and @{slack:U000000002}",
		"`<@U000000001>` and\n```\n<@U000000001>\n```\nstay": "`<@U000000001>` and\n```\n<@U000000001>\n```\nstay",
		"[<@U000000001>](https://x.example) stays":           "[<@U000000001>](https://x.example) stays",
		"<@u001> <@U0> <@B001> <@U000000001 > stay as text":  "<@u001> <@U0> <@B001> <@U000000001 > stay as text",
		"<#C001|ops> and <!here> stay":                       "<#C001|ops> and <!here> stay",
	} {
		got, err := f.svc.Mentions(context.Background(), in, draftMention)
		if err != nil || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, err, want)
		}
	}
}

func TestSendPostsATypedLabeledMention(t *testing.T) {
	f := newFixture(t, newWorkspace())
	mustSend(t, f, send.Request{Target: "#general", Text: "<@U000000002|Bob Example> **shipped**"})
	if got := onlyPost(t, f).Get("text"); got != "<@U000000002> *shipped*" {
		t.Fatalf("posted %q", got)
	}
}

func TestSendResolvesMentions(t *testing.T) {
	f := newFixture(t, newWorkspace())
	mustSend(t, f, send.Request{Target: "#general", Text: "@alice **shipped**"})
	if got := onlyPost(t, f).Get("text"); got != "<@U000000001> *shipped*" {
		t.Fatalf("posted %q", got)
	}
}

func TestMentionsReadTheUserListingOnceAndNeverFailOnALookup(t *testing.T) {
	f := newFixture(t, newWorkspace())
	got, err := f.svc.Mentions(context.Background(), "@nobody @ghost @nope and @alice", slackToken)
	if err != nil || got != "@nobody @ghost @nope and <@U000000001>" {
		t.Fatalf("%q %v", got, err)
	}
	if n := len(f.slack.Calls("users.list")); n > 2 {
		t.Fatalf("users.list called %d times for one text", n)
	}
	f = newFixture(t, newWorkspace())
	for _, m := range []string{"users.list", "users.lookupByEmail"} {
		f.slack.Handle(m, func(url.Values) any { return map[string]any{"ok": false, "error": "internal_error"} })
	}
	if got, err := f.svc.Mentions(context.Background(), "@alice and bob@example.com", slackToken); err != nil || got != "@alice and bob@example.com" {
		t.Fatalf("a failing lookup: %q %v", got, err)
	}
}
