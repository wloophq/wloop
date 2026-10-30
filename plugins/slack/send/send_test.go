package send_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/wloophq/wloop/plugins/slack/auth"
	"github.com/wloophq/wloop/plugins/slack/send"
	slack "github.com/wloophq/wloop/plugins/slack/slackapi"
	"github.com/wloophq/wloop/plugins/slack/slacktest"
)

const fakeToken = "fake-user-token-0000"

type staticToken struct{ tok string }

func (s staticToken) Token(context.Context) (string, error) { return s.tok, nil }

type failingToken struct{ err error }

func (f failingToken) Token(context.Context) (string, error) { return "", f.err }

type message struct{ ts, threadTS, text string }

type fakeWorkspace struct {
	conversations map[string]map[string]any // conversations.info "channel" objects by id
	mine          []map[string]any          // users.conversations
	users         []map[string]any          // users.list members
	messages      map[string]message        // by "<channel>/<ts>": the first message conversations.replies returns
	dms           map[string]string         // conversations.open: user id to im id
}

func newWorkspace() *fakeWorkspace {
	return &fakeWorkspace{
		conversations: map[string]map[string]any{
			"C100": {"id": "C100", "name": "general", "is_channel": true, "is_member": true},
			"C200": {"id": "C200", "name": "outsiders", "is_channel": true, "is_member": false},
			"C300": {"id": "C300", "name": "old", "is_channel": true, "is_member": true, "is_archived": true},
			"G400": {"id": "G400", "name": "secret", "is_group": true, "is_private": true, "is_member": true},
			"D500": {"id": "D500", "is_im": true, "user": "U000000001"},
			"D505": {"id": "D505", "is_im": true, "user": "U000000005"},
			"G600": {"id": "G600", "name": "mpdm-a--b-1", "is_mpim": true, "is_member": true},
		},
		mine: []map[string]any{
			{"id": "C100", "name": "general", "is_channel": true, "is_member": true},
			{"id": "G400", "name": "secret", "is_group": true, "is_member": true},
			{"id": "C700", "name": "dev-team", "is_channel": true, "is_member": true},
			{"id": "C701", "name": "dev-ops", "is_channel": true, "is_member": true},
		},
		users: []map[string]any{
			{"id": "U000000001", "name": "alice", "profile": map[string]any{"display_name": "alice", "email": "alice@example.com"}},
			{"id": "U000000002", "name": "bob", "profile": map[string]any{"display_name": "bobby", "email": "bob@example.com"}},
			{"id": "U000000003", "name": "sam.one", "profile": map[string]any{"display_name": "sam"}},
			{"id": "U000000004", "name": "sam.two", "profile": map[string]any{"display_name": "sam"}},
			{"id": "U000000005", "name": "gone", "deleted": true, "profile": map[string]any{"display_name": "gone"}},
		},
		messages: map[string]message{
			"C100/1700000000.123456": {ts: "1700000000.123456", text: "Parent question\nsecond line"},
			"C100/1699999999.000100": {ts: "1699999999.000100", threadTS: "1699999999.000100", text: "The real parent"},
			"C100/1700000100.000200": {ts: "1700000100.000200", threadTS: "1699999999.000100", text: "a reply"},
		},
		dms: map[string]string{"U000000001": "D500", "U000000002": "D501", "U000000003": "D503"},
	}
}

type fixture struct {
	slack *slacktest.Server
	svc   *send.Service
	logs  *[]string
}

func newFixture(t testing.TB, ws *fakeWorkspace) fixture {
	t.Helper()
	srv := slacktest.New(t)
	srv.Handle("conversations.info", func(v url.Values) any {
		c, ok := ws.conversations[v.Get("channel")]
		if !ok {
			return map[string]any{"ok": false, "error": "channel_not_found"}
		}
		return map[string]any{"channel": c}
	})
	srv.Handle("users.conversations", func(v url.Values) any {
		return map[string]any{"channels": ws.mine, "response_metadata": map[string]any{"next_cursor": ""}}
	})
	srv.Handle("users.list", func(v url.Values) any {
		return map[string]any{"members": ws.users}
	})
	srv.Handle("users.info", func(v url.Values) any {
		for _, u := range ws.users {
			if u["id"] == v.Get("user") {
				return map[string]any{"user": u}
			}
		}
		return map[string]any{"ok": false, "error": "user_not_found"}
	})
	srv.Handle("users.lookupByEmail", func(v url.Values) any {
		for _, u := range ws.users {
			if p, _ := u["profile"].(map[string]any); p["email"] == v.Get("email") {
				return map[string]any{"user": u}
			}
		}
		return map[string]any{"ok": false, "error": "users_not_found"}
	})
	srv.Handle("conversations.open", func(v url.Values) any {
		id, ok := ws.dms[v.Get("users")]
		if !ok {
			return map[string]any{"ok": false, "error": "user_not_found"}
		}
		return map[string]any{"channel": map[string]any{"id": id}}
	})
	srv.Handle("conversations.replies", func(v url.Values) any {
		m, ok := ws.messages[v.Get("channel")+"/"+v.Get("ts")]
		if !ok {
			return map[string]any{"ok": false, "error": "thread_not_found"}
		}
		raw := map[string]any{"ts": m.ts, "text": m.text, "type": "message"}
		if m.threadTS != "" {
			raw["thread_ts"] = m.threadTS
		}
		return map[string]any{"messages": []any{raw}, "has_more": false}
	})
	srv.Handle("chat.postMessage", func(v url.Values) any {
		return map[string]any{"channel": v.Get("channel"), "ts": "1800000000.000001"}
	})
	srv.Handle("chat.getPermalink", func(v url.Values) any {
		return map[string]any{"permalink": "https://example.slack.com/archives/" + v.Get("channel") + "/p" + strings.ReplaceAll(v.Get("message_ts"), ".", "")}
	})
	var mu sync.Mutex
	logs := []string{}
	svc := &send.Service{
		Client: &slack.Client{BaseURL: srv.URL, Tokens: staticToken{fakeToken}},
		Log: func(format string, args ...any) {
			mu.Lock()
			defer mu.Unlock()
			logs = append(logs, fmt.Sprintf(format, args...))
		},
	}
	return fixture{slack: srv, svc: svc, logs: &logs}
}

func (f fixture) posts() []url.Values { return f.slack.Calls("chat.postMessage") }

func ptr(b bool) *bool { return &b }

func mustSend(t *testing.T, f fixture, r send.Request) send.Sent {
	t.Helper()
	sent, err := f.svc.Send(context.Background(), r)
	if err != nil {
		t.Fatalf("Send(%q): %v", r.Target, err)
	}
	return sent
}

func onlyPost(t *testing.T, f fixture) url.Values {
	t.Helper()
	posts := f.posts()
	if len(posts) != 1 {
		t.Fatalf("chat.postMessage calls = %d, want 1", len(posts))
	}
	return posts[0]
}

func assertNoPost(t *testing.T, f fixture) {
	t.Helper()
	if n := len(f.posts()); n != 0 {
		t.Fatalf("chat.postMessage calls = %d, want none", n)
	}
}

func TestPermalinkRepliesInThread(t *testing.T) {
	t.Parallel()
	f := newFixture(t, newWorkspace())
	mustSend(t, f, send.Request{Target: "https://acme.slack.com/archives/C100/p1700000000123456", Text: "hi", Source: "cli"})
	post := onlyPost(t, f)
	if post.Get("channel") != "C100" || post.Get("thread_ts") != "1700000000.123456" {
		t.Fatalf("posted to %v", post)
	}
}

func TestPermalinkToAReplyUsesTheParent(t *testing.T) {
	t.Parallel()
	f := newFixture(t, newWorkspace())
	sent := mustSend(t, f, send.Request{
		Target: "https://acme.slack.com/archives/C100/p1700000100000200?thread_ts=1699999999.000100&cid=C100",
		Text:   "hi",
	})
	post := onlyPost(t, f)
	if post.Get("channel") != "C100" || post.Get("thread_ts") != "1699999999.000100" {
		t.Fatalf("posted to %v", post)
	}
	if sent.Parent != "The real parent" {
		t.Fatalf("Parent = %q", sent.Parent)
	}
	if n := len(f.slack.Calls("conversations.replies")); n != 1 {
		t.Fatalf("conversations.replies calls = %d, want 1", n)
	}
}

func TestReplyPermalinkWithoutThreadTSUsesTheParent(t *testing.T) {
	t.Parallel()
	f := newFixture(t, newWorkspace())
	sent := mustSend(t, f, send.Request{Target: "https://acme.slack.com/archives/C100/p1700000100000200", Text: "hi"})
	post := onlyPost(t, f)
	if post.Get("thread_ts") != "1699999999.000100" {
		t.Fatalf("thread_ts = %q, want the parent's", post.Get("thread_ts"))
	}
	if sent.Parent != "The real parent" {
		t.Fatalf("Parent = %q", sent.Parent)
	}
	first := f.slack.Calls("conversations.replies")[0]
	if first.Get("ts") != "1700000100.000200" || first.Get("limit") != "1" {
		t.Fatalf("first replies read = %v", first)
	}
}

func TestThreadFalsePostsToChannel(t *testing.T) {
	t.Parallel()
	f := newFixture(t, newWorkspace())
	sent := mustSend(t, f, send.Request{Target: "https://acme.slack.com/archives/C100/p1700000000123456", Text: "hi", Thread: ptr(false)})
	post := onlyPost(t, f)
	if post.Get("channel") != "C100" || post.Has("thread_ts") {
		t.Fatalf("posted to %v", post)
	}
	if sent.Parent != "" {
		t.Fatalf("Parent = %q, want none", sent.Parent)
	}
}

func TestChannelByName(t *testing.T) {
	t.Parallel()
	f := newFixture(t, newWorkspace())
	for _, target := range []string{"#general", "#General", "C100"} {
		sent := mustSend(t, f, send.Request{Target: target, Text: "hi"})
		if sent.Conversation != "#general" || sent.Channel != "C100" {
			t.Fatalf("%s: sent = %+v", target, sent)
		}
	}
	for _, post := range f.posts() {
		if post.Get("channel") != "C100" || post.Has("thread_ts") {
			t.Fatalf("posted to %v", post)
		}
	}
	res, err := f.svc.Resolve(context.Background(), send.Request{Target: "#secret"})
	if err != nil || res.Channel != "G400" || res.Conversation != "#secret" {
		t.Fatalf("private channel: %+v, %v", res, err)
	}
}

func TestUnknownChannelFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t, newWorkspace())
	_, err := f.svc.Send(context.Background(), send.Request{Target: "#dev", Text: "hi"})
	if !errors.Is(err, send.ErrAmbiguous) {
		t.Fatalf("err = %v, want ErrAmbiguous", err)
	}
	for _, want := range []string{"#dev-team", "#dev-ops"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err %q does not list %s", err, want)
		}
	}
	for _, target := range []string{"#nowhere", "#outsiders"} {
		_, err = f.svc.Send(context.Background(), send.Request{Target: target, Text: "hi"})
		if !errors.Is(err, send.ErrAmbiguous) || !strings.Contains(err.Error(), "among the channels you are a member of") {
			t.Fatalf("%s: err = %v, want ErrAmbiguous naming the member-only search", target, err)
		}
	}
	_, err = f.svc.Send(context.Background(), send.Request{Target: "C999", Text: "hi"})
	if !errors.Is(err, send.ErrCannotPost) {
		t.Fatalf("unknown id: err = %v, want ErrCannotPost", err)
	}
	_, err = f.svc.Send(context.Background(), send.Request{Target: "general", Text: "hi"})
	if err == nil {
		t.Fatal("a bare word was accepted as a target")
	}
	assertNoPost(t, f)
}

func TestDMByHandleEmailOrID(t *testing.T) {
	t.Parallel()
	cases := []struct{ target, channel, conversation, user string }{
		{"@alice", "D500", "@alice", "U000000001"},
		{"@bobby", "D501", "@bob", "U000000002"},
		{"bob@example.com", "D501", "@bob", "U000000002"},
		{"U000000001", "D500", "@alice", "U000000001"},
		{"@U000000002", "D501", "@bob", "U000000002"},
		{"https://acme.slack.com/archives/D500/p1700000000000001", "D500", "@alice", "U000000001"},
	}
	for _, c := range cases {
		f := newFixture(t, newWorkspace())
		res, err := f.svc.Resolve(context.Background(), send.Request{Target: c.target, Thread: ptr(false)})
		if err != nil {
			t.Fatalf("%s: %v", c.target, err)
		}
		if res.Channel != c.channel || res.Conversation != c.conversation || res.DMUser != c.user || res.ThreadTS != "" {
			t.Fatalf("%s: resolved %+v", c.target, res)
		}
		sent := mustSend(t, f, send.Request{Target: c.target, Text: "hi", Thread: ptr(false)})
		if sent.Channel != c.channel || onlyPost(t, f).Get("channel") != c.channel {
			t.Fatalf("%s: sent %+v", c.target, sent)
		}
	}
}

func TestAmbiguousHandleFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t, newWorkspace())
	_, err := f.svc.Send(context.Background(), send.Request{Target: "@sam", Text: "hi"})
	if !errors.Is(err, send.ErrAmbiguous) {
		t.Fatalf("err = %v, want ErrAmbiguous", err)
	}
	for _, want := range []string{"@sam.one", "@sam.two"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err %q does not list %s", err, want)
		}
	}
	for _, target := range []string{"@nobody", "@gone", "nobody@example.com", "U999"} {
		if _, err := f.svc.Send(context.Background(), send.Request{Target: target, Text: "hi"}); err == nil {
			t.Fatalf("%s was accepted", target)
		}
	}
	if n := len(f.slack.Calls("conversations.open")); n != 0 {
		t.Fatalf("conversations.open calls = %d, want none", n)
	}
	assertNoPost(t, f)
}

func TestNoPostWhenTheTokenCannotPost(t *testing.T) {
	t.Parallel()
	f := newFixture(t, newWorkspace())
	for _, target := range []string{
		"C200", "C300",
		"https://acme.slack.com/archives/C200/p1700000000123456",
		"https://acme.slack.com/archives/C300/p1700000000123456",
		"https://acme.slack.com/archives/C999/p1700000000123456",
		"D505",
		"https://acme.slack.com/archives/D505/p1700000000123456",
	} {
		_, err := f.svc.Send(context.Background(), send.Request{Target: target, Text: "hi"})
		if !errors.Is(err, send.ErrCannotPost) {
			t.Fatalf("%s: err = %v, want ErrCannotPost", target, err)
		}
	}
	assertNoPost(t, f)
}

func TestThreadWithoutPermalinkFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t, newWorkspace())
	if _, err := f.svc.Send(context.Background(), send.Request{Target: "#general", Text: "hi", Thread: ptr(true)}); err == nil {
		t.Fatal("thread=true on a channel target was accepted")
	}
	assertNoPost(t, f)
}

func TestNotLoggedIn(t *testing.T) {
	t.Parallel()
	f := newFixture(t, newWorkspace())
	f.svc.Client.Tokens = failingToken{auth.ErrNotLoggedIn}
	if _, err := f.svc.Send(context.Background(), send.Request{Target: "#general", Text: "hi"}); !errors.Is(err, send.ErrNotLoggedIn) {
		t.Fatalf("err = %v, want ErrNotLoggedIn", err)
	}

	g := newFixture(t, newWorkspace())
	g.slack.Handle("conversations.info", func(url.Values) any { return map[string]any{"ok": false, "error": "invalid_auth"} })
	if _, err := g.svc.Send(context.Background(), send.Request{Target: "C100", Text: "hi"}); !errors.Is(err, send.ErrNotLoggedIn) {
		t.Fatalf("invalid_auth: err = %v, want ErrNotLoggedIn", err)
	}

	if _, err := (&send.Service{}).Send(context.Background(), send.Request{Target: "C100", Text: "hi"}); !errors.Is(err, send.ErrNotLoggedIn) {
		t.Fatalf("no client: err = %v, want ErrNotLoggedIn", err)
	}
	assertNoPost(t, f)
	assertNoPost(t, g)
}

func TestEmptyTextFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t, newWorkspace())
	if _, err := f.svc.Send(context.Background(), send.Request{Target: "#general", Text: "  \n"}); err == nil {
		t.Fatal("empty text was sent")
	}
	assertNoPost(t, f)
}

func TestPostAllowlistRefusesOtherConversations(t *testing.T) {
	t.Parallel()
	f := newFixture(t, newWorkspace())
	f.svc.PostAllowlist = []string{"U000000001", "G400"}
	for _, target := range []string{"#general", "@bob", "https://acme.slack.com/archives/C100/p1700000000123456"} {
		_, err := f.svc.Send(context.Background(), send.Request{Target: target, Text: "hi"})
		if !errors.Is(err, send.ErrNotAllowed) {
			t.Fatalf("%s: err = %v, want ErrNotAllowed", target, err)
		}
		res, _ := f.svc.Resolve(context.Background(), send.Request{Target: target})
		if !strings.Contains(err.Error(), res.Channel) {
			t.Fatalf("%s: err %q does not name %s", target, err, res.Channel)
		}
	}
	assertNoPost(t, f)
	mustSend(t, f, send.Request{Target: "@alice", Text: "hi"})
	mustSend(t, f, send.Request{Target: "#secret", Text: "hi"})
	if n := len(f.posts()); n != 2 {
		t.Fatalf("posts = %d, want 2", n)
	}
}

func TestEmptyAllowlistAllowsAll(t *testing.T) {
	t.Parallel()
	f := newFixture(t, newWorkspace())
	for _, target := range []string{"#general", "@bob", "#secret", "C100"} {
		mustSend(t, f, send.Request{Target: target, Text: "hi"})
	}
	if err := f.svc.Allowed(send.Resolved{Channel: "C1", DMUser: "U1"}); err != nil {
		t.Fatalf("Allowed with an empty list: %v", err)
	}
}

func TestSentDescribesThePost(t *testing.T) {
	t.Parallel()
	f := newFixture(t, newWorkspace())
	sent := mustSend(t, f, send.Request{
		Target: "https://acme.slack.com/archives/C100/p1700000000123456",
		Text:   "**done** & shipped",
		Source: "session",
	})
	want := send.Sent{
		Conversation: "#general",
		Parent:       "Parent question",
		ThreadTS:     "1700000000.123456",
		Text:         "*done* &amp; shipped",
		Permalink:    "https://example.slack.com/archives/C100/p1800000000000001",
		Channel:      "C100",
		TS:           "1800000000.000001",
	}
	if sent != want {
		t.Fatalf("sent = %+v\nwant %+v", sent, want)
	}
	if got := onlyPost(t, f).Get("text"); got != want.Text {
		t.Fatalf("posted text = %q", got)
	}
	perma := f.slack.Calls("chat.getPermalink")
	if len(perma) != 1 || perma[0].Get("channel") != "C100" || perma[0].Get("message_ts") != "1800000000.000001" {
		t.Fatalf("chat.getPermalink calls = %v", perma)
	}
}

func TestBroadcastOnlyInAThread(t *testing.T) {
	t.Parallel()
	f := newFixture(t, newWorkspace())
	mustSend(t, f, send.Request{Target: "https://acme.slack.com/archives/C100/p1700000000123456", Text: "hi", Broadcast: true})
	mustSend(t, f, send.Request{Target: "#general", Text: "hi", Broadcast: true})
	posts := f.posts()
	if posts[0].Get("reply_broadcast") != "true" || posts[1].Has("reply_broadcast") {
		t.Fatalf("posts = %v", posts)
	}
}

func TestSendLogsWithoutText(t *testing.T) {
	t.Parallel()
	f := newFixture(t, newWorkspace())
	mustSend(t, f, send.Request{Target: "#general", Text: "secret plans", Source: "draft"})
	if len(*f.logs) != 1 {
		t.Fatalf("logs = %q", *f.logs)
	}
	line := (*f.logs)[0]
	if line != "slack_sent source=draft channel=C100 ts=1800000000.000001" {
		t.Fatalf("log = %q", line)
	}
}

func TestPermalinkFailureStillReportsThePost(t *testing.T) {
	t.Parallel()
	f := newFixture(t, newWorkspace())
	f.slack.Handle("chat.getPermalink", func(url.Values) any { return map[string]any{"ok": false, "error": "internal_error"} })
	sent, err := f.svc.Send(context.Background(), send.Request{Target: "#general", Text: "hi"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if sent.TS == "" || sent.Permalink != "" {
		t.Fatalf("sent = %+v", sent)
	}
}

func TestErrorsNeverHoldTheToken(t *testing.T) {
	t.Parallel()
	f := newFixture(t, newWorkspace())
	f.slack.Handle("users.conversations", func(url.Values) any { return map[string]any{"ok": false, "error": "invalid_auth"} })
	f.slack.Handle("users.list", func(url.Values) any { return "not json " + fakeToken })
	for _, target := range []string{"#x", "@x", "C100"} {
		_, err := f.svc.Send(context.Background(), send.Request{Target: target, Text: "hi"})
		if err != nil && strings.Contains(err.Error(), fakeToken) {
			t.Fatalf("%s: error holds the token: %v", target, err)
		}
	}
	f.slack.Close()
	_, err := f.svc.Send(context.Background(), send.Request{Target: "C100", Text: "hi"})
	if err == nil || strings.Contains(err.Error(), fakeToken) {
		t.Fatalf("transport failure: %v", err)
	}
	for _, l := range *f.logs {
		if strings.Contains(l, fakeToken) {
			t.Fatalf("log holds the token: %q", l)
		}
	}
}

func TestNameLookupsReuseTheListingUntilAMiss(t *testing.T) {
	t.Parallel()
	ws := newWorkspace()
	f := newFixture(t, ws)
	ctx := context.Background()
	for range 3 {
		if _, err := f.svc.Resolve(ctx, send.Request{Target: "@alice"}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.Resolve(ctx, send.Request{Target: "#general"}); err != nil {
			t.Fatal(err)
		}
	}
	if u, c := len(f.slack.Calls("users.list")), len(f.slack.Calls("users.conversations")); u != 1 || c != 1 {
		t.Fatalf("users.list = %d, users.conversations = %d, want 1 each", u, c)
	}
	ws.users = append(ws.users, map[string]any{"id": "U000000009", "name": "newcomer"})
	ws.dms["U000000009"] = "D509"
	ws.mine = append(ws.mine, map[string]any{"id": "C800", "name": "fresh", "is_channel": true, "is_member": true})
	ws.conversations["C800"] = map[string]any{"id": "C800", "name": "fresh", "is_channel": true, "is_member": true}
	if res, err := f.svc.Resolve(ctx, send.Request{Target: "@newcomer"}); err != nil || res.Channel != "D509" {
		t.Fatalf("newcomer: %+v, %v", res, err)
	}
	if res, err := f.svc.Resolve(ctx, send.Request{Target: "#fresh"}); err != nil || res.Channel != "C800" {
		t.Fatalf("fresh: %+v, %v", res, err)
	}
	if u, c := len(f.slack.Calls("users.list")), len(f.slack.Calls("users.conversations")); u != 2 || c != 2 {
		t.Fatalf("users.list = %d, users.conversations = %d, want 2 each", u, c)
	}
}

func TestNameListingsPage(t *testing.T) {
	t.Parallel()
	f := newFixture(t, newWorkspace())
	f.slack.Handle("users.conversations", func(v url.Values) any {
		if v.Get("cursor") == "" {
			return map[string]any{"channels": []any{map[string]any{"id": "C100", "name": "general"}}, "response_metadata": map[string]any{"next_cursor": "next"}}
		}
		return map[string]any{"channels": []any{map[string]any{"id": "G400", "name": "secret"}}}
	})
	f.slack.Handle("users.list", func(v url.Values) any {
		if v.Get("cursor") == "" {
			return map[string]any{"members": []any{map[string]any{"id": "U000000002", "name": "bob"}}, "response_metadata": map[string]any{"next_cursor": "next"}}
		}
		return map[string]any{"members": []any{map[string]any{"id": "U000000001", "name": "alice"}}}
	})
	if res, err := f.svc.Resolve(context.Background(), send.Request{Target: "#secret"}); err != nil || res.Channel != "G400" {
		t.Fatalf("#secret: %+v, %v", res, err)
	}
	if res, err := f.svc.Resolve(context.Background(), send.Request{Target: "@alice"}); err != nil || res.Channel != "D500" {
		t.Fatalf("@alice: %+v, %v", res, err)
	}
	first := f.slack.Calls("users.conversations")[0]
	if first.Get("types") != "public_channel,private_channel,mpim" || first.Get("exclude_archived") != "true" {
		t.Fatalf("users.conversations args = %v", first)
	}
}

func TestRenamedHandleIsNotReusedFromTheCache(t *testing.T) {
	t.Parallel()
	ws := newWorkspace()
	f := newFixture(t, ws)
	ctx := context.Background()
	if res, err := f.svc.Resolve(ctx, send.Request{Target: "@alice"}); err != nil || res.DMUser != "U000000001" {
		t.Fatalf("first @alice: %+v, %v", res, err)
	}
	ws.users[0] = map[string]any{"id": "U000000001", "name": "alice.old", "profile": map[string]any{"display_name": "alice.old"}}
	ws.users = append(ws.users, map[string]any{"id": "U010", "name": "alice", "profile": map[string]any{"display_name": "alice"}})
	ws.dms["U010"] = "D510"
	sent := mustSend(t, f, send.Request{Target: "@alice", Text: "hi"})
	if sent.Channel != "D510" || sent.Conversation != "@alice" || onlyPost(t, f).Get("channel") != "D510" {
		t.Fatalf("sent %+v, want the new owner's DM", sent)
	}
	if n := len(f.slack.Calls("users.list")); n != 2 {
		t.Fatalf("users.list calls = %d, want 2", n)
	}

	ws.users = ws.users[:len(ws.users)-1]
	ws.users[0] = map[string]any{"id": "U000000001", "name": "alice", "profile": map[string]any{"display_name": "alice"}}
	if _, err := f.svc.Resolve(ctx, send.Request{Target: "@alice"}); err != nil {
		t.Fatal(err)
	}
	ws.users[0] = map[string]any{"id": "U000000001", "name": "alice.old", "profile": map[string]any{"display_name": "alice.old"}}
	if _, err := f.svc.Send(ctx, send.Request{Target: "@alice", Text: "hi"}); !errors.Is(err, send.ErrAmbiguous) {
		t.Fatalf("handle given up: err = %v, want ErrAmbiguous", err)
	}
	if n := len(f.posts()); n != 1 {
		t.Fatalf("posts = %d, want only the first", n)
	}
}

func TestRenamedChannelIsNotReusedFromTheCache(t *testing.T) {
	t.Parallel()
	ws := newWorkspace()
	f := newFixture(t, ws)
	ctx := context.Background()
	if res, err := f.svc.Resolve(ctx, send.Request{Target: "#general"}); err != nil || res.Channel != "C100" {
		t.Fatalf("first #general: %+v, %v", res, err)
	}
	ws.mine[0] = map[string]any{"id": "C100", "name": "general-old", "is_channel": true, "is_member": true}
	ws.conversations["C100"] = ws.mine[0]
	ws.mine = append(ws.mine, map[string]any{"id": "C900", "name": "general", "is_channel": true, "is_member": true})
	ws.conversations["C900"] = ws.mine[len(ws.mine)-1]
	sent := mustSend(t, f, send.Request{Target: "#general", Text: "hi"})
	if sent.Channel != "C900" || sent.Conversation != "#general" || onlyPost(t, f).Get("channel") != "C900" {
		t.Fatalf("sent %+v, want the new #general", sent)
	}

	ws.mine = ws.mine[:len(ws.mine)-1]
	delete(ws.conversations, "C900")
	ws.mine[0] = map[string]any{"id": "C100", "name": "general", "is_channel": true, "is_member": true}
	ws.conversations["C100"] = ws.mine[0]
	if _, err := f.svc.Resolve(ctx, send.Request{Target: "#general"}); err != nil {
		t.Fatal(err)
	}
	ws.mine[0] = map[string]any{"id": "C100", "name": "general-old", "is_channel": true, "is_member": true}
	ws.conversations["C100"] = ws.mine[0]
	if _, err := f.svc.Send(ctx, send.Request{Target: "#general", Text: "hi"}); !errors.Is(err, send.ErrAmbiguous) {
		t.Fatalf("name given up: err = %v, want ErrAmbiguous", err)
	}
	if n := len(f.posts()); n != 1 {
		t.Fatalf("posts = %d, want only the first", n)
	}
}

func TestAKnownConversationIsPlacedWithoutAskingSlack(t *testing.T) {
	t.Parallel()
	f := newFixture(t, newWorkspace())
	f.svc.Known = func(_ context.Context, id string) (send.KnownConversation, bool) {
		switch id {
		case "C100":
			return send.KnownConversation{ID: "C100", Name: "general", Kind: "channel", IsMember: true}, true
		case "C300":
			return send.KnownConversation{ID: "C300", Name: "old", Kind: "channel", IsMember: true, Archived: true}, true
		case "D500":
			return send.KnownConversation{ID: "D500", Kind: "im", DMUser: "U000000001", DMUserName: "alice"}, true
		}
		return send.KnownConversation{}, false
	}
	ctx := context.Background()
	res, err := f.svc.Resolve(ctx, send.Request{Target: "C100"})
	if err != nil || res.Channel != "C100" || res.Conversation != "#general" {
		t.Fatalf("a known channel: %+v, %v", res, err)
	}
	if res, err := f.svc.Resolve(ctx, send.Request{Target: "D500"}); err != nil || res.DMUser != "U000000001" || res.Conversation != "@alice" {
		t.Fatalf("a known DM: %+v, %v", res, err)
	}
	if _, err := f.svc.Resolve(ctx, send.Request{Target: "C300"}); !errors.Is(err, send.ErrCannotPost) {
		t.Fatalf("a known archived channel: %v, want ErrCannotPost", err)
	}
	if n := len(f.slack.Calls("conversations.info")) + len(f.slack.Calls("users.info")); n != 0 {
		t.Fatalf("Slack was asked %d times about conversations the store knows", n)
	}
	if res, err := f.svc.Resolve(ctx, send.Request{Target: "G400"}); err != nil || res.Conversation != "#secret" || len(f.slack.Calls("conversations.info")) != 1 {
		t.Fatalf("a conversation the store does not know: %+v, %v, %d calls", res, err, len(f.slack.Calls("conversations.info")))
	}
}
