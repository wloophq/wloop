package send_test

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/wloophq/wloop/plugins/slack/send"
)

const editLink = "https://example.slack.com/archives/C100/p1700000100000200?thread_ts=1699999999.000100&cid=C100"

func editFixture(t *testing.T, author string) fixture {
	t.Helper()
	f := newFixture(t, newWorkspace())
	f.slack.Handle("conversations.replies", func(v url.Values) any {
		return map[string]any{"messages": []map[string]any{
			{"ts": "1699999999.000100", "thread_ts": "1699999999.000100", "user": "U002", "text": "The real parent"},
			{"ts": "1700000100.000200", "thread_ts": "1699999999.000100", "user": author, "text": "release 1.2 :hourglass:"},
		}}
	})
	f.slack.Handle("chat.update", func(v url.Values) any { return map[string]any{"channel": v.Get("channel"), "ts": v.Get("ts")} })
	return f
}

func TestEditReplacesTheUsersMessageAndKeepsWhatItReplaced(t *testing.T) {
	f := editFixture(t, "U001")
	got, err := f.svc.Edit(context.Background(), send.Edit{Message: editLink, Text: "release 1.2 **done**, thanks @bobby", User: "U001"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Channel != "C100" || got.TS != "1700000100.000200" || got.Conversation != "#general" || got.Replaced != "release 1.2 :hourglass:" ||
		got.Text != "release 1.2 *done*, thanks <@U002>" {
		t.Fatalf("Edit = %+v", got)
	}
	ups := f.slack.Calls("chat.update")
	if len(ups) != 1 || ups[0].Get("channel") != "C100" || ups[0].Get("ts") != "1700000100.000200" || ups[0].Get("text") != got.Text || ups[0].Get("blocks") != "[]" {
		t.Fatalf("chat.update %v", ups)
	}
	assertNoPost(t, f)
}

func TestEditRefusesAnotherUsersMessageAndWhatSendRefuses(t *testing.T) {
	f := editFixture(t, "U002")
	if _, err := f.svc.Edit(context.Background(), send.Edit{Message: editLink, Text: "x", User: "U001"}); !errors.Is(err, send.ErrCannotPost) {
		t.Fatalf("another's message: %v", err)
	}
	f = editFixture(t, "U001")
	f.svc.PostAllowlist = []string{"C999"}
	if _, err := f.svc.Edit(context.Background(), send.Edit{Message: editLink, Text: "x", User: "U001"}); !errors.Is(err, send.ErrNotAllowed) {
		t.Fatalf("outside the allowlist: %v", err)
	}
	f.svc.PostAllowlist = nil
	for _, e := range []send.Edit{{Message: "#general", Text: "x", User: "U001"}, {Message: editLink, Text: " ", User: "U001"},
		{Message: editLink, Text: "x"}} {
		if _, err := f.svc.Edit(context.Background(), e); err == nil {
			t.Errorf("%+v edited", e)
		}
	}
	if n := len(f.slack.Calls("chat.update")); n != 0 {
		t.Fatalf("chat.update %d", n)
	}
}

func TestEditOnARefReadsTheMessageInTheThreadTheStoreHolds(t *testing.T) {
	f := editFixture(t, "U001")
	f.svc.Stored, _ = storedMessages(send.StoredMessage{TS: "1700000100.000200", ThreadTS: "1699999999.000100"})
	got, err := f.svc.Edit(context.Background(), send.Edit{Message: "C100/1700000100.000200", Text: "done", User: "U001"})
	if err != nil || got.TS != "1700000100.000200" || got.Replaced != "release 1.2 :hourglass:" || got.Permalink == "" {
		t.Fatalf("Edit = %+v %v", got, err)
	}
	replies := f.slack.Calls("conversations.replies")
	if len(replies) != 1 || replies[0].Get("ts") != "1699999999.000100" {
		t.Fatalf("conversations.replies %v, want one read of the stored thread", replies)
	}
}

func TestEditOnARefNotStoredFindsTheMessageOnSlack(t *testing.T) {
	f := editFixture(t, "U001")
	f.slack.Handle("conversations.history", func(url.Values) any { return map[string]any{"messages": []any{}} })
	got, err := f.svc.Edit(context.Background(), send.Edit{Message: "C100/1700000100.000200", Text: "done", User: "U001"})
	if err != nil || got.TS != "1700000100.000200" || got.Replaced != "release 1.2 :hourglass:" {
		t.Fatalf("Edit = %+v %v", got, err)
	}
}
