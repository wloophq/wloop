package send_test

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/plugins/slack/send"
)

func TestParseMessageReadsARefOrAPermalink(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]send.Message{
		"C100/1700000100.000200":                                 {Conversation: "C100", TS: "1700000100.000200"},
		" D500/1700000100.000200 ":                               {Conversation: "D500", TS: "1700000100.000200"},
		"G400/1700000100.000200":                                 {Conversation: "G400", TS: "1700000100.000200"},
		"https://acme.slack.com/archives/C100/p1700000100000200": {Conversation: "C100", TS: "1700000100.000200"},
		"https://acme.slack.com/archives/C100/p1700000100000200?thread_ts=1699999999.000100&cid=C100": {
			Conversation: "C100", TS: "1700000100.000200", ThreadTS: "1699999999.000100"},
	} {
		got, ok := send.ParseMessage(in)
		if !ok || got != want {
			t.Errorf("ParseMessage(%q) = %+v %t, want %+v", in, got, ok, want)
		}
		if !send.IsMessage(in) {
			t.Errorf("IsMessage(%q) = false", in)
		}
	}
	if got := (send.Message{Conversation: "C100", TS: "1700000100.000200", ThreadTS: "1699999999.000100"}).Ref(); got != "C100/1700000100.000200" {
		t.Errorf("Ref = %q", got)
	}
}

func TestParseMessageRefusesWhatNamesNoMessage(t *testing.T) {
	t.Parallel()
	for _, in := range []string{
		"", "C100", "#general", "@alice", "U000000001", "U000000001/1700000100.000200", "c100/1700000100.000200",
		"C100/1700000100", "C100/1700000100.0002", "C100/1700000100.000200/1700000100.000300", "C100/ 1700000100.000200",
		"/1700000100.000200", "slack:T000/C100/1700000100.000200",
		"https://example.com/archives/C100/p1700000100000200", "https://acme.slack.com/archives/C100",
	} {
		if got, ok := send.ParseMessage(in); ok {
			t.Errorf("ParseMessage(%q) = %+v", in, got)
		}
		if send.IsMessage(in) {
			t.Errorf("IsMessage(%q) = true", in)
		}
	}
	if send.IsPermalink("C100/1700000100.000200") {
		t.Error("a ref is a permalink")
	}
}

func storedMessages(ms ...send.StoredMessage) (send.Stored, *[]string) {
	var asked []string
	return func(_ context.Context, conv, ts string) (send.StoredMessage, bool) {
		asked = append(asked, conv+"/"+ts)
		for _, m := range ms {
			if m.TS == ts {
				return m, true
			}
		}
		return send.StoredMessage{}, false
	}, &asked
}

func TestRefToAReplyRepliesInItsThreadReadFromSlack(t *testing.T) {
	t.Parallel()
	f := newFixture(t, newWorkspace())
	sent := mustSend(t, f, send.Request{Target: "C100/1700000100.000200", Text: "hi"})
	post := onlyPost(t, f)
	if post.Get("channel") != "C100" || post.Get("thread_ts") != "1699999999.000100" || sent.Parent != "The real parent" {
		t.Fatalf("posted to %v, parent %q", post, sent.Parent)
	}
}

func TestRefToAStoredReplyRepliesInItsThreadWithoutReadingSlack(t *testing.T) {
	t.Parallel()
	f := newFixture(t, newWorkspace())
	var asked *[]string
	f.svc.Stored, asked = storedMessages(
		send.StoredMessage{TS: "1690000000.000100", ThreadTS: "1690000000.000100", Text: "Stored parent\nmore"},
		send.StoredMessage{TS: "1690000000.000300", ThreadTS: "1690000000.000100", Text: "a stored reply"},
	)
	sent := mustSend(t, f, send.Request{Target: "C100/1690000000.000300", Text: "hi"})
	post := onlyPost(t, f)
	if post.Get("channel") != "C100" || post.Get("thread_ts") != "1690000000.000100" || sent.Parent != "Stored parent" || sent.ThreadTS != "1690000000.000100" {
		t.Fatalf("posted to %v, sent %+v", post, sent)
	}
	if n := len(f.slack.Calls("conversations.replies")); n != 0 {
		t.Fatalf("conversations.replies calls = %d, want none", n)
	}
	if len(*asked) != 2 {
		t.Fatalf("store asked %v", *asked)
	}
}

func TestRefToANonStoredMessageFallsBackToSlack(t *testing.T) {
	t.Parallel()
	f := newFixture(t, newWorkspace())
	f.svc.Stored, _ = storedMessages()
	mustSend(t, f, send.Request{Target: "C100/1700000100.000200", Text: "hi"})
	if post := onlyPost(t, f); post.Get("thread_ts") != "1699999999.000100" {
		t.Fatalf("posted to %v", post)
	}
	if n := len(f.slack.Calls("conversations.replies")); n == 0 {
		t.Fatal("Slack was not asked for the thread")
	}
}

func TestRefWithThreadFalsePostsToTheConversation(t *testing.T) {
	t.Parallel()
	f := newFixture(t, newWorkspace())
	mustSend(t, f, send.Request{Target: "C100/1700000100.000200", Text: "hi", Thread: ptr(false)})
	if post := onlyPost(t, f); post.Get("channel") != "C100" || post.Get("thread_ts") != "" {
		t.Fatalf("posted to %v", post)
	}
}
