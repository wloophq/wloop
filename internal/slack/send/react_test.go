package send_test

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/wloophq/wloop/internal/slack/send"
)

const reactLink = "https://example.slack.com/archives/C100/p1700000100000200?thread_ts=1699999999.000100&cid=C100"

func reactFixture(t *testing.T, code string) fixture {
	t.Helper()
	f := newFixture(t, newWorkspace())
	for _, m := range []string{"reactions.add", "reactions.remove"} {
		f.slack.Handle(m, func(url.Values) any {
			if code != "" {
				return map[string]any{"ok": false, "error": code}
			}
			return map[string]any{}
		})
	}
	return f
}

func TestReactAddsAndRemovesOnThePermalinksMessage(t *testing.T) {
	f := reactFixture(t, "")
	got, err := f.svc.React(context.Background(), send.Reaction{Message: reactLink, Name: ":hourglass:"})
	if err != nil || got.Channel != "C100" || got.TS != "1700000100.000200" || got.Conversation != "#general" || got.Unchanged {
		t.Fatalf("React = %+v %v", got, err)
	}
	if _, err := f.svc.React(context.Background(), send.Reaction{Message: reactLink, Name: "hourglass", Remove: true}); err != nil {
		t.Fatal(err)
	}
	adds, removes := f.slack.Calls("reactions.add"), f.slack.Calls("reactions.remove")
	if len(adds) != 1 || adds[0].Get("name") != "hourglass" || adds[0].Get("timestamp") != "1700000100.000200" || adds[0].Get("channel") != "C100" ||
		len(removes) != 1 || removes[0].Get("name") != "hourglass" {
		t.Fatalf("adds %v, removes %v", adds, removes)
	}
	assertNoPost(t, f)
}

func TestReactThatChangesNothingIsUnchanged(t *testing.T) {
	for code, remove := range map[string]bool{"already_reacted": false, "no_reaction": true} {
		got, err := reactFixture(t, code).svc.React(context.Background(), send.Reaction{Message: reactLink, Name: "eyes", Remove: remove})
		if err != nil || !got.Unchanged {
			t.Errorf("%s: %+v %v", code, got, err)
		}
	}
}

func TestReactRefusesWhatSendRefuses(t *testing.T) {
	f := reactFixture(t, "")
	f.svc.PostAllowlist = []string{"C999"}
	if _, err := f.svc.React(context.Background(), send.Reaction{Message: reactLink, Name: "eyes"}); !errors.Is(err, send.ErrNotAllowed) {
		t.Fatalf("outside the allowlist: %v", err)
	}
	f.svc.PostAllowlist = nil
	for _, r := range []send.Reaction{{Message: "#general", Name: "eyes"}, {Message: reactLink, Name: " : "}} {
		if _, err := f.svc.React(context.Background(), r); err == nil {
			t.Errorf("%+v reacted", r)
		}
	}
	if n := len(f.slack.Calls("reactions.add")); n != 0 {
		t.Fatalf("reactions.add %d", n)
	}
}

func TestReactOnARefReactsToItsMessageAndGivesItsPermalink(t *testing.T) {
	f := reactFixture(t, "")
	got, err := f.svc.React(context.Background(), send.Reaction{Message: "C100/1700000100.000200", Name: "eyes"})
	if err != nil || got.Channel != "C100" || got.TS != "1700000100.000200" || got.Permalink != "https://example.slack.com/archives/C100/p1700000100000200" {
		t.Fatalf("React = %+v %v", got, err)
	}
	if adds := f.slack.Calls("reactions.add"); len(adds) != 1 || adds[0].Get("channel") != "C100" || adds[0].Get("timestamp") != "1700000100.000200" {
		t.Fatalf("adds %v", adds)
	}
	if got, _ := f.svc.React(context.Background(), send.Reaction{Message: reactLink, Name: "eyes"}); got.Permalink != reactLink {
		t.Fatalf("a permalink's reaction gives %q", got.Permalink)
	}
	if n := len(f.slack.Calls("chat.getPermalink")); n != 1 {
		t.Fatalf("chat.getPermalink calls = %d, want 1", n)
	}
}

func TestReactOnAStoredRefGivesTheStoredPermalinkWithoutAskingSlack(t *testing.T) {
	f := reactFixture(t, "")
	f.svc.Stored, _ = storedMessages(send.StoredMessage{TS: "1700000100.000200", Permalink: "https://example.slack.com/archives/C100/p1700000100000200?stored=1"})
	got, err := f.svc.React(context.Background(), send.Reaction{Message: "C100/1700000100.000200", Name: "eyes"})
	if err != nil || got.Permalink != "https://example.slack.com/archives/C100/p1700000100000200?stored=1" {
		t.Fatalf("React = %+v %v", got, err)
	}
	if n := len(f.slack.Calls("chat.getPermalink")); n != 0 {
		t.Fatalf("chat.getPermalink calls = %d, want 0", n)
	}
}
