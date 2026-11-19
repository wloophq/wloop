package send_test

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/wloophq/wloop/plugins/slack/send"
)

const messageLink = "https://example.slack.com/archives/C100/p1700000100000200?thread_ts=1699999999.000100&cid=C100"

func messageFixture(t *testing.T, author string) fixture {
	t.Helper()
	f := newFixture(t, newWorkspace())
	f.slack.Handle("conversations.replies", func(v url.Values) any {
		return map[string]any{"messages": []map[string]any{
			{"ts": "1699999999.000100", "thread_ts": "1699999999.000100", "user": "U000000002", "text": "The real parent"},
			{"ts": "1700000100.000200", "thread_ts": "1699999999.000100", "user": author, "text": "release 1.2 :hourglass:"},
		}}
	})
	return f
}

func TestReadGivesTheMessagesAuthorAndText(t *testing.T) {
	f := messageFixture(t, "U000000001")
	got, ok, err := f.svc.Read(context.Background(), messageLink)
	if err != nil || !ok || got.Channel != "C100" || got.TS != "1700000100.000200" || got.User != "U000000001" || got.Text != "release 1.2 :hourglass:" {
		t.Fatalf("Read = %+v %v %v", got, ok, err)
	}
	f.svc.PostAllowlist = []string{"C999"}
	if _, _, err := f.svc.Read(context.Background(), messageLink); !errors.Is(err, send.ErrNotAllowed) {
		t.Fatalf("outside the allowlist: %v", err)
	}
	f.svc.PostAllowlist = nil
	if _, _, err := f.svc.Read(context.Background(), "#general"); !errors.Is(err, send.ErrAmbiguous) {
		t.Fatalf("a channel: %v", err)
	}
	assertNoPost(t, f)
}

func TestReadOnARefReadsTheMessageInTheThreadTheStoreHolds(t *testing.T) {
	f := messageFixture(t, "U000000001")
	f.svc.Stored, _ = storedMessages(send.StoredMessage{TS: "1700000100.000200", ThreadTS: "1699999999.000100"})
	got, ok, err := f.svc.Read(context.Background(), "C100/1700000100.000200")
	if err != nil || !ok || got.TS != "1700000100.000200" || got.Text != "release 1.2 :hourglass:" {
		t.Fatalf("Read = %+v %v %v", got, ok, err)
	}
	replies := f.slack.Calls("conversations.replies")
	if len(replies) != 1 || replies[0].Get("ts") != "1699999999.000100" {
		t.Fatalf("conversations.replies %v, want one read of the stored thread", replies)
	}
}
