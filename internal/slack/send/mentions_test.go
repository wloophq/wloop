package send_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/slack/send"
)

func slackToken(id string) string { return "<@" + id + ">" }

func TestMentionsResolveHandlesEmailsAndUserIDs(t *testing.T) {
	f := newFixture(t, newWorkspace())
	for in, want := range map[string]string{
		"@alice, can you look?":                      "<@U001>, can you look?",
		"ask @bobby and bob@example.com.":            "ask <@U002> and <@U002>.",
		"cc @U003":                                   "cc <@U003>",
		"@Alice: done":                               "<@U001>: done",
		"`@alice` and\n```\n@alice\n```\nstay":       "`@alice` and\n```\n@alice\n```\nstay",
		"[mail](mailto:alice@example.com) <@U002>":   "[mail](mailto:alice@example.com) <@U002>",
		"@nobody and ghost@example.com stay as text": "@nobody and ghost@example.com stay as text",
		"@here and @channel are not people":          "@here and @channel are not people",
		"scoped @types/node and @{slack:U001} stay":  "scoped @types/node and @{slack:U001} stay",
		"a deactivated @gone stays":                  "a deactivated @gone stays",
	} {
		got, err := f.svc.Mentions(context.Background(), in, slackToken)
		if err != nil || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, err, want)
		}
	}
	if _, err := f.svc.Mentions(context.Background(), "hi @sam", slackToken); !errors.Is(err, send.ErrAmbiguous) || !strings.Contains(err.Error(), "U003") {
		t.Fatalf("two users named sam: %v", err)
	}
}

func TestSendResolvesMentions(t *testing.T) {
	f := newFixture(t, newWorkspace())
	mustSend(t, f, send.Request{Target: "#general", Text: "@alice **shipped**"})
	if got := onlyPost(t, f).Get("text"); got != "<@U001> *shipped*" {
		t.Fatalf("posted %q", got)
	}
}
