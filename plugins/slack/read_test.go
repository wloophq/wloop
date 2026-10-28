package slack

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/plugins/slack/auth"
	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	slackapi "github.com/wloophq/wloop/plugins/slack/slackapi"
)

const (
	readConv   = "C0000000001"
	readParent = "1700000000.000100"
	readReply  = "1700000000.000200"
)

func readPermalink(ts string, thread string) string {
	link := "https://example.slack.com/archives/" + readConv + "/p" + strings.ReplaceAll(ts, ".", "")
	if thread != "" {
		link += "?thread_ts=" + thread + "&cid=" + readConv
	}
	return link
}

func (f *fixture) reader() *Reader { return ReaderOver(f.db, f.slack.Client, nil) }

func (f *fixture) seedReadThread(t *testing.T) {
	t.Helper()
	f.knownUser(t, other, false)
	f.seed(t, dslack.Facts{
		Conversations: []dslack.Conversation{{ID: readConv, Name: "general", Kind: dslack.KindChannel, IsMember: true}},
		Threads: []dslack.Thread{{Conversation: readConv, TS: readParent, LatestReply: readReply, ReplyCount: 1, Messages: []dslack.Message{
			{TS: readParent, ThreadTS: readParent, User: other, Text: "can you review this?", Permalink: readPermalink(readParent, ""),
				Reactions: []dslack.Reaction{{Name: "eyes", Users: []string{me}}}},
			{TS: readReply, ThreadTS: readParent, User: me, Text: "on it\nlater today"},
		}}},
	})
}

func (f *fixture) slackRows(t *testing.T) map[string]int {
	t.Helper()
	out := map[string]int{}
	err := f.db.Read(context.Background(), func(tx *sql.Tx) error {
		for _, table := range []string{"slack_conversations", "slack_objects", "slack_messages", "slack_reactions", "slack_users",
			"slack_changes", "slack_cursors"} {
			var n int
			if err := tx.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
				return err
			}
			out[table] = n
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSlackReadStoredUnit(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.seedReadThread(t)
	ctx := context.Background()
	r := f.reader()

	unitTarget := "slack:" + dslack.ThreadUnit(team, readConv, readParent).Key
	for _, target := range []string{unitTarget, readPermalink(readReply, readParent), readConv + "/" + readReply, readConv + "/" + readParent} {
		got, err := r.Read(ctx, target, 50)
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		for _, want := range []string{
			"Slack thread in #general: " + readPermalink(readParent, ""),
			"[m1 " + readConv + "/" + readParent + "] n (" + other + ")\ncan you review this?\nreactions: eyes by " + me,
			"[m2 " + readConv + "/" + readReply + "] " + me + "\non it\nlater today",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("%s: missing %q in\n%s", target, want, got)
			}
		}
	}
	if calls := f.calls(apiMethods...); len(calls) != 0 {
		t.Fatalf("stored reads called Slack: %v", calls)
	}

	got, err := r.Read(ctx, unitTarget, 1)
	if err != nil || !strings.Contains(got, "[m1 "+readConv+"/"+readParent) || strings.Contains(got, "[m2") || !strings.Contains(got, "(1 message left out)") {
		t.Fatalf("limit 1: %v\n%s", err, got)
	}
}

func TestSlackReadKeepsParentAndNewest(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	var msgs []dslack.Message
	for i := range 6 {
		ts := fmt.Sprintf("1700000000.%06d", 100+i)
		msgs = append(msgs, dslack.Message{TS: ts, ThreadTS: readParent, User: other, Text: fmt.Sprintf("message %d", i)})
	}
	f.seed(t, dslack.Facts{Threads: []dslack.Thread{{Conversation: readConv, TS: readParent, Messages: msgs}}})
	got, err := f.reader().Read(context.Background(), "slack:"+dslack.ThreadUnit(team, readConv, readParent).Key, 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"message 0", "(3 messages left out)", "message 4", "message 5", "Slack thread in " + readConv} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in\n%s", want, got)
		}
	}
	for _, gone := range []string{"message 1", "message 3"} {
		if strings.Contains(got, gone) {
			t.Fatalf("%q kept in\n%s", gone, got)
		}
	}
}

func TestSlackReadLiveStoresNothing(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.api.Handle("conversations.replies", func(v url.Values) any {
		return map[string]any{"messages": []map[string]any{
			{"ts": readParent, "thread_ts": readParent, "user": other, "text": "live parent", "reply_count": 1},
			{"ts": readReply, "thread_ts": readParent, "user": me, "text": "live reply", "reactions": []map[string]any{{"name": "+1", "users": []string{other}}}},
		}, "has_more": false}
	})
	f.api.Handle("conversations.info", func(v url.Values) any {
		return map[string]any{"channel": map[string]any{"id": v.Get("channel"), "name": "general", "is_channel": true}}
	})
	before := f.slackRows(t)
	link := readPermalink(readReply, readParent)
	got, err := f.reader().Read(context.Background(), link, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Slack thread in #general: " + link, "live parent", "live reply", "reactions: +1 by " + other} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in\n%s", want, got)
		}
	}
	calls := f.api.Calls("conversations.replies")
	if len(calls) != 1 || calls[0].Get("channel") != readConv || calls[0].Get("ts") != readParent {
		t.Fatalf("replies calls = %v", calls)
	}
	if other := f.calls(apiMethods...); len(other) != 2 || other["conversations.info"] != 1 {
		t.Fatalf("Slack calls = %v", other)
	}
	if after := f.slackRows(t); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("slack rows changed: %v -> %v", before, after)
	}
}

func TestSlackReadRefToANonStoredReplyReadsItsThreadFromSlack(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.api.Handle("conversations.replies", func(v url.Values) any {
		return map[string]any{"messages": []map[string]any{
			{"ts": readParent, "thread_ts": readParent, "user": other, "text": "live parent", "reply_count": 1},
			{"ts": readReply, "thread_ts": readParent, "user": me, "text": "live reply"},
		}, "has_more": false}
	})
	f.api.Handle("conversations.info", func(v url.Values) any {
		return map[string]any{"channel": map[string]any{"id": v.Get("channel"), "name": "general", "is_channel": true}}
	})
	f.api.Handle("chat.getPermalink", func(v url.Values) any {
		return map[string]any{"permalink": readPermalink(v.Get("message_ts"), "")}
	})
	got, err := f.reader().Read(context.Background(), readConv+"/"+readReply, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{": " + readPermalink(readParent, "") + "\n", "[m1 " + readConv + "/" + readParent + "] " + other, "[m2 " + readConv + "/" + readReply + "] " + me + "\nlive reply"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "reply to") {
		t.Fatalf("the thread's replies are marked as replies to one of its messages:\n%s", got)
	}
}

func TestSlackReadRefusesBadTargets(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	r := f.reader()
	for _, target := range []string{"", "#general", "slack:T0001/nope", "github:o/r#1", "https://example.com/archives/C1/p1700000000000100",
		readConv + "/1700000000", "U0000000001/" + readParent} {
		if _, err := r.Read(context.Background(), target, 50); err == nil {
			t.Fatalf("%q: no error", target)
		}
	}
	if _, err := r.Read(context.Background(), "slack:"+dslack.ThreadUnit(team, readConv, readParent).Key, 50); !errors.Is(err, dslack.ErrUnknownUnit) {
		t.Fatalf("unstored unit: %v", err)
	}
	if calls := f.calls(apiMethods...); len(calls) != 0 {
		t.Fatalf("bad targets called Slack: %v", calls)
	}
}

func TestSlackReadHoldsNoTokenOnFailure(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.api.Handle("conversations.info", func(v url.Values) any {
		return map[string]any{"channel": map[string]any{"id": v.Get("channel"), "name": "general", "is_channel": true}}
	})
	f.api.Handle("conversations.replies", func(url.Values) any { return map[string]any{"ok": false, "error": "channel_not_found"} })
	_, err := f.reader().Read(context.Background(), readPermalink(readReply, readParent), 50)
	if err == nil || strings.Contains(err.Error(), fakeUserToken) || !strings.Contains(err.Error(), "channel_not_found") {
		t.Fatalf("err = %v", err)
	}

	client := &slackapi.Client{BaseURL: f.api.URL}
	client.Tokens = &auth.Store{Keychain: &keychain{items: map[string][]byte{}}, Service: f.cfg.Service(),
		LockPath: filepath.Join(t.TempDir(), "slack.lock"), Client: client, ClientID: "1.2"}
	_, err = ReaderOver(f.db, client, nil).Read(context.Background(), readPermalink(readReply, readParent), 50)
	if !errors.Is(err, auth.ErrNotLoggedIn) {
		t.Fatalf("logged out: %v", err)
	}
}

func BenchmarkSlackReadStoredThread(b *testing.B) {
	f := newFixture(b)
	var msgs []dslack.Message
	for i := range 2000 {
		msgs = append(msgs, dslack.Message{TS: fmt.Sprintf("1700000000.%06d", 100+i), ThreadTS: readParent, User: other,
			Text: strings.Repeat("word ", 40)})
	}
	f.seed(b, dslack.Facts{Threads: []dslack.Thread{{Conversation: readConv, TS: readParent, Messages: msgs}}})
	r, target := f.reader(), "slack:"+dslack.ThreadUnit(team, readConv, readParent).Key
	for b.Loop() {
		if _, err := r.Read(context.Background(), target, 200); err != nil {
			b.Fatal(err)
		}
	}
}

type fixedClock time.Time

func (c fixedClock) Now() time.Time { return time.Time(c) }

const readDM = "D0000000001"

func dmPermalink(ts string) string {
	return "https://example.slack.com/archives/" + readDM + "/p" + strings.ReplaceAll(ts, ".", "")
}

func TestSlackReadDMStoredSegment(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	if _, err := (Segments{Store: f.db.Segments(), Clock: fixedClock(f.now)}).Apply(context.Background(), team, readDM,
		[]dslack.Span{{FromTS: readParent}}); err != nil {
		t.Fatal(err)
	}
	f.seed(t, dslack.Facts{
		Conversations: []dslack.Conversation{{ID: readDM, Kind: dslack.KindIM, DMUser: other}},
		DMs: []dslack.DMWindow{{Conversation: readDM, Latest: readReply, Messages: []dslack.Message{
			{TS: readParent, User: other, Text: "dm question"},
			{TS: readReply, User: me, Text: "dm answer"},
		}}},
	})
	got, err := f.reader().Read(context.Background(), dmPermalink(readReply), 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Direct message with n (" + other + ")", "dm question", "dm answer"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in\n%s", want, got)
		}
	}
	if calls := f.calls(apiMethods...); len(calls) != 0 {
		t.Fatalf("a stored DM read called Slack: %v", calls)
	}
}

func TestSlackReadRefusesUntrackedDM(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.api.Handle("conversations.info", func(v url.Values) any {
		return map[string]any{"channel": map[string]any{"id": v.Get("channel"), "is_im": true, "user": other}}
	})
	f.api.Handle("conversations.replies", func(url.Values) any { return map[string]any{"messages": []any{}} })
	before := f.slackRows(t)
	_, err := f.reader().Read(context.Background(), dmPermalink(readParent), 50)
	if err == nil || !strings.Contains(err.Error(), "is not tracked") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.api.Calls("conversations.replies")); n != 0 {
		t.Fatalf("conversations.replies called %d times", n)
	}
	if after := f.slackRows(t); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("slack rows changed: %v -> %v", before, after)
	}
}

func TestSlackReadSaysALongThreadWasCutOff(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.api.Handle("conversations.info", func(v url.Values) any {
		return map[string]any{"channel": map[string]any{"id": v.Get("channel"), "name": "general", "is_channel": true}}
	})
	f.api.Handle("conversations.replies", func(url.Values) any {
		return map[string]any{"messages": []map[string]any{{"ts": readParent, "thread_ts": readParent, "user": other, "text": "page"}},
			"has_more": true, "response_metadata": map[string]any{"next_cursor": "next"}}
	})
	got, err := f.reader().Read(context.Background(), readPermalink(readParent, ""), 50)
	if err != nil || !strings.Contains(got, "(thread longer than 1000 messages; newer ones not read, open the permalink)") {
		t.Fatalf("%v\n%s", err, got)
	}
	if n := len(f.api.Calls("conversations.replies")); n != readMaxPages {
		t.Fatalf("replies pages = %d", n)
	}
}

func TestSlackReadRefLogsAPermalinkSlackRefuses(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.api.Handle("conversations.replies", func(v url.Values) any {
		return map[string]any{"messages": []map[string]any{{"ts": readParent, "user": other, "text": "live parent"}}, "has_more": false}
	})
	f.api.Handle("conversations.info", func(v url.Values) any {
		return map[string]any{"channel": map[string]any{"id": v.Get("channel"), "name": "general", "is_channel": true}}
	})
	f.api.Handle("chat.getPermalink", func(url.Values) any { return map[string]any{"ok": false, "error": "ratelimited"} })
	var logged []string
	got, err := ReaderOver(f.db, f.slack.Client, func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }).
		Read(context.Background(), readConv+"/"+readParent, 50)
	if err != nil || !strings.Contains(got, "no permalink ("+readConv+"/"+readParent+")") {
		t.Fatalf("Read = %q, %v", got, err)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "slack_permalink") || !strings.Contains(logged[0], "ratelimited") {
		t.Fatalf("logged %q", logged)
	}
}
