package send_test

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/wloophq/wloop/plugins/slack/send"
)

func deleteFixture(t *testing.T, author string) fixture {
	t.Helper()
	f := editFixture(t, author)
	f.slack.Handle("chat.delete", func(v url.Values) any { return map[string]any{"channel": v.Get("channel"), "ts": v.Get("ts")} })
	return f
}

func TestDeleteRemovesTheUsersReplyAndKeepsItsText(t *testing.T) {
	f := deleteFixture(t, "U000000001")
	got, err := f.svc.Delete(context.Background(), send.Delete{Message: editLink, User: "U000000001"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Channel != "C100" || got.TS != "1700000100.000200" || got.Conversation != "#general" || got.Deleted != "release 1.2 :hourglass:" || got.ThreadTS != "1699999999.000100" ||
		got.Permalink != editLink {
		t.Fatalf("Delete = %+v", got)
	}
	dels := f.slack.Calls("chat.delete")
	if len(dels) != 1 || dels[0].Get("channel") != "C100" || dels[0].Get("ts") != "1700000100.000200" {
		t.Fatalf("chat.delete %v", dels)
	}
	if n := len(f.slack.Calls("chat.update")); n != 0 {
		t.Fatalf("chat.update %d", n)
	}
	assertNoPost(t, f)
}

func TestDeleteRefusesAnotherUsersMessageAndWhatSendRefuses(t *testing.T) {
	f := deleteFixture(t, "U000000002")
	if _, err := f.svc.Delete(context.Background(), send.Delete{Message: editLink, User: "U000000001"}); !errors.Is(err, send.ErrCannotPost) {
		t.Fatalf("another's message: %v", err)
	}
	f = deleteFixture(t, "U000000001")
	f.svc.PostAllowlist = []string{"C999"}
	if _, err := f.svc.Delete(context.Background(), send.Delete{Message: editLink, User: "U000000001"}); !errors.Is(err, send.ErrNotAllowed) {
		t.Fatalf("outside the allowlist: %v", err)
	}
	f.svc.PostAllowlist = nil
	for _, d := range []send.Delete{{Message: "#general", User: "U000000001"}, {Message: editLink}} {
		if _, err := f.svc.Delete(context.Background(), d); err == nil {
			t.Errorf("%+v deleted", d)
		}
	}
	if n := len(f.slack.Calls("chat.delete")); n != 0 {
		t.Fatalf("chat.delete %d", n)
	}
}

func TestDeleteOnARefReadsTheMessageInTheThreadTheStoreHolds(t *testing.T) {
	f := deleteFixture(t, "U000000001")
	f.svc.Stored, _ = storedMessages(send.StoredMessage{TS: "1700000100.000200", ThreadTS: "1699999999.000100"})
	got, err := f.svc.Delete(context.Background(), send.Delete{Message: "C100/1700000100.000200", User: "U000000001"})
	if err != nil || got.TS != "1700000100.000200" || got.Deleted != "release 1.2 :hourglass:" || got.Permalink == "" {
		t.Fatalf("Delete = %+v %v", got, err)
	}
	replies := f.slack.Calls("conversations.replies")
	if len(replies) != 1 || replies[0].Get("ts") != "1699999999.000100" {
		t.Fatalf("conversations.replies %v, want one read of the stored thread", replies)
	}
	if links := f.slack.Calls("chat.getPermalink"); len(links) != 1 {
		t.Fatalf("chat.getPermalink %v, want one read before the message goes", links)
	}
}

func TestDeleteOnARefNotStoredFindsTheMessageOnSlack(t *testing.T) {
	f := deleteFixture(t, "U000000001")
	f.slack.Handle("conversations.history", func(url.Values) any { return map[string]any{"messages": []any{}} })
	got, err := f.svc.Delete(context.Background(), send.Delete{Message: "C100/1700000100.000200", User: "U000000001"})
	if err != nil || got.TS != "1700000100.000200" || got.Deleted != "release 1.2 :hourglass:" {
		t.Fatalf("Delete = %+v %v", got, err)
	}
}

func TestDeleteInASlackConnectChannelChecksTheAuthorByUserID(t *testing.T) {
	ws := newWorkspace()
	ws.conversations["C800"] = map[string]any{"id": "C800", "name": "shared-partner", "is_channel": true, "is_member": true,
		"is_ext_shared": true, "is_shared": true}
	f := newFixture(t, ws)
	f.slack.Handle("conversations.replies", func(url.Values) any {
		return map[string]any{"messages": []map[string]any{
			{"ts": "1699999999.000100", "thread_ts": "1699999999.000100", "user": "U900000009", "user_team": "T900000009", "text": "Partner question"},
			{"ts": "1700000100.000200", "thread_ts": "1699999999.000100", "user": "U000000001", "user_team": "T000000001", "text": "our answer"},
		}}
	})
	f.slack.Handle("conversations.history", func(url.Values) any { return map[string]any{"messages": []any{}} })
	f.slack.Handle("chat.delete", func(v url.Values) any { return map[string]any{"channel": v.Get("channel"), "ts": v.Get("ts")} })
	link := "https://example.slack.com/archives/C800/p1700000100000200?thread_ts=1699999999.000100&cid=C800"
	got, err := f.svc.Delete(context.Background(), send.Delete{Message: link, User: "U000000001"})
	if err != nil || got.Channel != "C800" || got.Deleted != "our answer" {
		t.Fatalf("Delete = %+v %v", got, err)
	}
	parent := "https://example.slack.com/archives/C800/p1699999999000100"
	if _, err := f.svc.Delete(context.Background(), send.Delete{Message: parent, User: "U000000001"}); !errors.Is(err, send.ErrCannotPost) {
		t.Fatalf("the partner's message: %v", err)
	}
	if dels := f.slack.Calls("chat.delete"); len(dels) != 1 {
		t.Fatalf("chat.delete %v", dels)
	}
}
