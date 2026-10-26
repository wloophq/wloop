package slack

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/items"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
	slackapi "github.com/wloophq/wloop/internal/slack"
	"github.com/wloophq/wloop/internal/slack/auth"
	"github.com/wloophq/wloop/internal/slack/send"
	"github.com/wloophq/wloop/internal/slack/slacktest"
)

const (
	pubConv     = "C0000000001"
	pubArchived = "C0000000002"
	pubOutside  = "C0000000003"
	pubDM       = "D0000000001"
	pubParent   = "1700000000.000100"
	pubReply    = "1700000000.000200"
	pubPosted   = "1700000300.000300"
)

type pubSlack struct {
	f *fixture

	mu      sync.Mutex
	order   []string
	history []map[string]any
	reacted []map[string]any
}

// newPubSlack answers a channel the user is in, an archived one, one the
// user is not in, the DM with other, and the posts.
func newPubSlack(t *testing.T) *pubSlack {
	t.Helper()
	ps := &pubSlack{f: newFixture(t)}
	api := ps.f.api
	note := func(m string) {
		ps.mu.Lock()
		ps.order = append(ps.order, m)
		ps.mu.Unlock()
	}
	api.Handle("conversations.info", func(v url.Values) any {
		note("conversations.info")
		switch id := v.Get("channel"); id {
		case pubDM:
			return map[string]any{"channel": map[string]any{"id": pubDM, "is_im": true, "user": other}}
		case pubArchived:
			return map[string]any{"channel": map[string]any{"id": id, "name": "old", "is_channel": true, "is_member": true, "is_archived": true}}
		case pubOutside:
			return map[string]any{"channel": map[string]any{"id": id, "name": "elsewhere", "is_channel": true}}
		default:
			return map[string]any{"channel": map[string]any{"id": id, "name": "general", "is_channel": true, "is_member": true}}
		}
	})
	api.Handle("conversations.replies", func(v url.Values) any {
		note("conversations.replies")
		if v.Get("oldest") != "" {
			ps.mu.Lock()
			defer ps.mu.Unlock()
			return map[string]any{"messages": ps.history}
		}
		return map[string]any{"messages": []map[string]any{{"ts": v.Get("ts"), "thread_ts": pubParent, "text": "the parent", "user": other}}}
	})
	api.Handle("conversations.history", func(url.Values) any {
		note("conversations.history")
		ps.mu.Lock()
		defer ps.mu.Unlock()
		return map[string]any{"messages": ps.history}
	})
	api.Handle("conversations.open", func(v url.Values) any {
		note("conversations.open")
		return map[string]any{"channel": map[string]any{"id": pubDM}}
	})
	api.Handle("chat.postMessage", func(v url.Values) any {
		note("chat.postMessage")
		return map[string]any{"channel": v.Get("channel"), "ts": pubPosted}
	})
	api.Handle("chat.getPermalink", func(v url.Values) any {
		note("chat.getPermalink")
		return map[string]any{"permalink": "https://acme.slack.com/archives/" + v.Get("channel") + "/p" + strings.ReplaceAll(v.Get("message_ts"), ".", "")}
	})
	api.Handle("reactions.add", func(url.Values) any {
		note("reactions.add")
		return nil
	})
	api.Handle("reactions.get", func(url.Values) any {
		note("reactions.get")
		ps.mu.Lock()
		defer ps.mu.Unlock()
		return map[string]any{"message": map[string]any{"reactions": ps.reacted}}
	})
	ps.f.login(t)
	return ps
}

func (ps *pubSlack) publisher(allow ...string) *Publisher {
	return ps.publisherWith(func() (auth.Token, error) {
		return auth.Token{Team: team, User: me, Scopes: strings.Split(auth.UserScopes, ",")}, nil
	}, allow...)
}

func (ps *pubSlack) publisherWith(token func() (auth.Token, error), allow ...string) *Publisher {
	p := PublisherOver(ps.f.db, &send.Service{Client: ps.f.slack.Client, PostAllowlist: allow}, token, func() time.Time { return ps.f.now }, ps.f.logs.logf)
	p.People = func(context.Context, unit.Unit) ([]items.Person, error) {
		return []items.Person{
			{Key: "github:octo", Name: "Octo Cat"},
			{Key: "github:sneaky", Name: "@here <@U0009> & <!channel>"},
		}, nil
	}
	return p
}

func (ps *pubSlack) calls(method string) []url.Values { return ps.f.api.Calls(method) }

func (ps *pubSlack) seen() []string {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return append([]string(nil), ps.order...)
}

func (ps *pubSlack) posted(t *testing.T) url.Values {
	t.Helper()
	posts := ps.calls("chat.postMessage")
	if len(posts) != 1 {
		t.Fatalf("posts = %d, want 1", len(posts))
	}
	return posts[0]
}

func threadDraft(kind draft.Kind, text string) draft.Draft {
	purpose, delivery := draft.ShapeOf(kind)
	return draft.Draft{ID: 7, Unit: dslack.ThreadUnit(team, pubConv, pubParent), Kind: kind, Purpose: purpose, Delivery: delivery, Text: text}
}

func (ps *pubSlack) segmentUnit(t *testing.T) unit.Unit {
	t.Helper()
	us, err := (Segments{Store: ps.f.db.Segments(), Clock: fixedClock(ps.f.now)}).Apply(context.Background(), team, pubDM,
		[]dslack.Span{{FromTS: pubParent}})
	if err != nil || len(us) != 1 {
		t.Fatalf("segments: %v %v", us, err)
	}
	return us[0]
}

func TestPublishDefaultsToTheUnitThread(t *testing.T) {
	ps := newPubSlack(t)
	ev, err := ps.publisher().Publish(context.Background(), threadDraft(draft.Reply, "on it"))
	if err != nil {
		t.Fatal(err)
	}
	post := ps.posted(t)
	if post.Get("channel") != pubConv || post.Get("thread_ts") != pubParent || post.Get("text") != "on it" {
		t.Fatalf("post %v", post)
	}
	if ev.Ref != pubPosted || !strings.Contains(ev.Permalink, "/p1700000300000300") || !ev.At.Equal(time.Unix(1700000300, 0).UTC()) {
		t.Fatalf("evidence %+v", ev)
	}
}

func TestPublishOnASegmentPostsToTheDM(t *testing.T) {
	ps := newPubSlack(t)
	d := draft.Draft{ID: 1, Unit: ps.segmentUnit(t), Kind: draft.Reply, Text: "done"}
	if _, err := ps.publisher().Publish(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if post := ps.posted(t); post.Get("channel") != pubDM || post.Has("thread_ts") {
		t.Fatalf("post %v", post)
	}
}

func TestNudgeOpensTheDM(t *testing.T) {
	for _, target := range []string{other, "@" + other, "slack:" + other, "@{slack:" + other + "}"} {
		t.Run(target, func(t *testing.T) {
			ps := newPubSlack(t)
			d := threadDraft(draft.Nudge, "any news?")
			d.Target = target
			if _, err := ps.publisher().Publish(context.Background(), d); err != nil {
				t.Fatal(err)
			}
			opens := ps.calls("conversations.open")
			if len(opens) != 1 || opens[0].Get("users") != other {
				t.Fatalf("opens %v", opens)
			}
			if post := ps.posted(t); post.Get("channel") != pubDM || post.Has("thread_ts") {
				t.Fatalf("post %v", post)
			}
		})
	}
}

func TestReactAddsAReaction(t *testing.T) {
	ps := newPubSlack(t)
	d := threadDraft(draft.React, "")
	d.Reaction = ":white_check_mark:"
	ev, err := ps.publisher().Publish(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	adds := ps.calls("reactions.add")
	if len(adds) != 1 || adds[0].Get("channel") != pubConv || adds[0].Get("timestamp") != pubParent || adds[0].Get("name") != "white_check_mark" {
		t.Fatalf("reactions.add %v", adds)
	}
	if n := len(ps.calls("chat.postMessage")); n != 0 || ev.Ref != pubParent {
		t.Fatalf("posts %d, evidence %+v", n, ev)
	}

	ps = newPubSlack(t)
	d.Target = "https://acme.slack.com/archives/" + pubConv + "/p" + strings.ReplaceAll(pubReply, ".", "")
	if _, err := ps.publisher().Publish(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if adds := ps.calls("reactions.add"); len(adds) != 1 || adds[0].Get("timestamp") != pubReply {
		t.Fatalf("a permalink's reaction %v", adds)
	}
}

func TestPublishConvertsMarkdown(t *testing.T) {
	ps := newPubSlack(t)
	text := "# Done\n**merged** [the PR](https://example.com/pr?a=1&b=2) & a <b> tag, `x < y`\n- one"
	if _, err := ps.publisher().Publish(context.Background(), threadDraft(draft.Reply, text)); err != nil {
		t.Fatal(err)
	}
	if got := ps.posted(t).Get("text"); got != send.Mrkdwn(text) {
		t.Fatalf("posted %q, want %q", got, send.Mrkdwn(text))
	}
}

func TestPublishConvertsMentions(t *testing.T) {
	ps := newPubSlack(t)
	text := "@{slack:U000AAA} and @{github:octo}, **@{github:sneaky}**, @{group:infra}"
	if _, err := ps.publisher().Publish(context.Background(), threadDraft(draft.Reply, text)); err != nil {
		t.Fatal(err)
	}
	got := ps.posted(t).Get("text")
	want := "<@U000AAA> and Octo Cat, *@\u2060here &lt;@\u2060U0009&gt; &amp; &lt;!channel&gt;*, infra"
	if got != want {
		t.Fatalf("posted %q, want %q", got, want)
	}
	if strings.Count(got, "<@") != 1 || strings.Contains(got, "<!") || strings.Contains(got, "@here") {
		t.Fatalf("a name pings: %q", got)
	}

	d := threadDraft(draft.Reply, "@{github:nobody} please")
	if err := ps.publisher().Validate(context.Background(), d); err == nil || !strings.Contains(err.Error(), "nobody") {
		t.Fatalf("unknown person: %v", err)
	}
	if d.Text = "@{slack:not-an-id}"; ps.publisher().Validate(context.Background(), d) == nil {
		t.Fatal("a slack mention with no user id validated")
	}
}

func TestPublishToAReplyUsesTheParent(t *testing.T) {
	ps := newPubSlack(t)
	d := threadDraft(draft.Reply, "following up")
	d.Target = "https://acme.slack.com/archives/" + pubConv + "/p" + strings.ReplaceAll(pubReply, ".", "")
	if _, err := ps.publisher().Publish(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if post := ps.posted(t); post.Get("channel") != pubConv || post.Get("thread_ts") != pubParent {
		t.Fatalf("post %v", post)
	}
}

func TestPublishHonorsThePostAllowlist(t *testing.T) {
	for name, d := range map[string]draft.Draft{
		"thread":         threadDraft(draft.Reply, "hi"),
		"react":          {Unit: dslack.ThreadUnit(team, pubConv, pubParent), Kind: draft.React, Delivery: draft.Reaction, Reaction: "eyes"},
		"nudge by id":    {Unit: dslack.ThreadUnit(team, pubConv, pubParent), Kind: draft.Nudge, Text: "hi", Target: other},
		"nudge by email": {Unit: dslack.ThreadUnit(team, pubConv, pubParent), Kind: draft.Nudge, Text: "hi", Target: "dana@example.com"},
		"nudge handle":   {Unit: dslack.ThreadUnit(team, pubConv, pubParent), Kind: draft.Nudge, Text: "hi", Target: "@dana"},
	} {
		t.Run(name, func(t *testing.T) {
			ps := newPubSlack(t)
			ps.f.api.Handle("users.lookupByEmail", func(url.Values) any { return map[string]any{"user": map[string]any{"id": other, "name": "dana"}} })
			_, err := ps.publisher(pubDM, "U0000000077").Publish(context.Background(), d)
			if !errors.Is(err, send.ErrNotAllowed) {
				t.Fatalf("Publish = %v, want ErrNotAllowed", err)
			}
			for _, m := range []string{"chat.postMessage", "reactions.add", "conversations.open"} {
				if n := len(ps.calls(m)); n != 0 {
					t.Fatalf("%s called %d times", m, n)
				}
			}
		})
	}
	ps := newPubSlack(t)
	d := threadDraft(draft.Nudge, "hi")
	d.Target = other
	if _, err := ps.publisher(other).Publish(context.Background(), d); err != nil {
		t.Fatalf("an allowed DM: %v", err)
	}
	if seen := strings.Join(ps.seen(), " "); !strings.HasSuffix(seen, "conversations.open chat.postMessage chat.getPermalink") {
		t.Fatalf("calls %s", seen)
	}
}

func TestPublishErrorsHoldNoToken(t *testing.T) {
	ps := newPubSlack(t)
	ps.f.api.Handle("chat.postMessage", func(url.Values) any { return map[string]any{"ok": false, "error": "restricted_action"} })
	_, err := ps.publisher().Publish(context.Background(), threadDraft(draft.Reply, "hi"))
	if err == nil || !strings.Contains(err.Error(), "chat.postMessage") || !strings.Contains(err.Error(), "restricted_action") {
		t.Fatalf("err %v", err)
	}
	if strings.HasPrefix(err.Error(), mayHaveBeenPosted) {
		t.Fatalf("a refusal says it may have been posted: %v", err)
	}
	for _, s := range []string{err.Error(), ps.f.logs.String()} {
		if strings.Contains(s, fakeUserToken) || strings.Contains(s, "xoxp-") {
			t.Fatalf("token in %q", s)
		}
	}
}

func TestPublishTimeoutSaysMayHaveBeenPosted(t *testing.T) {
	for name, tc := range map[string]struct {
		answer func(url.Values) any
		short  bool
		fail   string
		may    bool
	}{
		"5xx":      {answer: func(url.Values) any { return slacktest.Response{Status: http.StatusBadGateway, Body: "bad gateway"} }, may: true},
		"timeout":  {answer: func(url.Values) any { time.Sleep(300 * time.Millisecond); return nil }, short: true, may: true},
		"not json": {answer: func(url.Values) any { return "<html>" }, may: true},
		"4xx":      {answer: func(url.Values) any { return slacktest.Response{Status: http.StatusBadRequest, Body: "no"} }},
		"refused":  {answer: func(url.Values) any { return map[string]any{"ok": false, "error": "channel_not_found"} }},
		"ratelimit": {answer: func(url.Values) any {
			return slacktest.Response{Status: http.StatusTooManyRequests, Header: http.Header{"Retry-After": {"0"}}}
		}},
		"no connect":   {fail: "dial"},
		"body timeout": {fail: "body", may: true},
		"body cancel":  {fail: "cancel", may: true},
		"reset":        {fail: "read", may: true},
	} {
		t.Run(name, func(t *testing.T) {
			ps := newPubSlack(t)
			p := ps.publisher()
			client := &slackapi.Client{BaseURL: ps.f.api.URL, Tokens: ps.f.slack.Client.Tokens}
			p.Send = &send.Service{Client: client}
			if tc.answer != nil {
				ps.f.api.Handle("chat.postMessage", tc.answer)
			}
			if tc.short {
				client.HTTP = &http.Client{Timeout: 100 * time.Millisecond}
			}
			if tc.fail != "" {
				client.HTTP = &http.Client{Transport: failPost{op: tc.fail}}
			}
			_, err := p.Publish(context.Background(), threadDraft(draft.Reply, "hi"))
			if err == nil {
				t.Fatal("published")
			}
			if got := strings.HasPrefix(err.Error(), mayHaveBeenPosted); got != tc.may {
				t.Fatalf("err %q, may have been posted %v, want %v", err, got, tc.may)
			}
		})
	}
}

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

// failPost fails chat.postMessage at the transport with op.
type failPost struct{ op string }

func (f failPost) RoundTrip(r *http.Request) (*http.Response, error) {
	if strings.HasSuffix(r.URL.Path, "chat.postMessage") {
		switch f.op {
		case "body":
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(io.MultiReader(strings.NewReader(`{"ok":tr`),
				failingReader{fmt.Errorf("net/http: request canceled (Client.Timeout or context cancellation while reading body): %w", context.DeadlineExceeded)}))}, nil
		case "cancel":
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(failingReader{context.Canceled})}, nil
		}
		return nil, &net.OpError{Op: f.op, Net: "tcp", Err: syscall.ECONNRESET}
	}
	return http.DefaultTransport.RoundTrip(r)
}

func TestValidateRefusesWithoutPosting(t *testing.T) {
	ps := newPubSlack(t)
	ps.f.api.Handle("users.list", func(url.Values) any {
		return map[string]any{"members": []map[string]any{{"id": "U0000000011", "name": "sam"}, {"id": "U0000000012", "name": "sam2", "profile": map[string]any{"display_name": "sam"}}}}
	})
	for name, tc := range map[string]struct {
		d    draft.Draft
		want []string
		is   error
	}{
		"archived":   {d: draft.Draft{Unit: dslack.ThreadUnit(team, pubArchived, pubParent), Kind: draft.Reply, Text: "hi"}, want: []string{"archived"}, is: send.ErrCannotPost},
		"not member": {d: draft.Draft{Unit: dslack.ThreadUnit(team, pubOutside, pubParent), Kind: draft.Reply, Text: "hi"}, want: []string{"not a member", "#elsewhere"}, is: send.ErrCannotPost},
		"two users":  {d: draft.Draft{Unit: dslack.ThreadUnit(team, pubConv, pubParent), Kind: draft.Nudge, Text: "hi", Target: "@sam"}, want: []string{"U0000000011", "U0000000012"}, is: send.ErrAmbiguous},
		"empty":      {d: threadDraft(draft.Reply, "  "), want: []string{"empty"}},
		"other unit": {d: draft.Draft{Unit: unit.Unit{Provider: unit.GitHub, Key: "o/r#1"}, Kind: draft.Reply, Text: "hi"}, want: []string{"slack units"}},
		"react dm":   {d: draft.Draft{Unit: ps.segmentUnit(t), Kind: draft.React, Delivery: draft.Reaction, Reaction: "eyes"}, want: []string{"permalink"}},
		"escapes":    {d: threadDraft(draft.Reply, "a & b <c> <@U0001|x> <https://x.y|z>"), want: nil},
	} {
		t.Run(name, func(t *testing.T) {
			err := ps.publisher().Validate(context.Background(), tc.d)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("Validate = %v", err)
				}
				return
			}
			if err == nil || (tc.is != nil && !errors.Is(err, tc.is)) {
				t.Fatalf("Validate = %v, want %v", err, tc.is)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Fatalf("%q lacks %q", err, w)
				}
			}
		})
	}
	if n := len(ps.calls("chat.postMessage")) + len(ps.calls("reactions.add")); n != 0 {
		t.Fatalf("validation posted %d times", n)
	}
}

func TestValidateReactNeedsTheScope(t *testing.T) {
	ps := newPubSlack(t)
	d := threadDraft(draft.React, "")
	d.Reaction = "eyes"
	p := ps.publisherWith(func() (auth.Token, error) { return auth.Token{User: me, Scopes: []string{"chat:write"}}, nil })
	if err := p.Validate(context.Background(), d); err == nil || !strings.Contains(err.Error(), "reactions:write") {
		t.Fatalf("Validate = %v", err)
	}
	if _, err := p.Publish(context.Background(), d); err == nil || len(ps.calls("reactions.add")) != 0 {
		t.Fatalf("Publish = %v", err)
	}
}

func (ps *pubSlack) stored(msgs ...map[string]any) {
	ps.mu.Lock()
	ps.history = msgs
	ps.mu.Unlock()
}

func TestVerifyFindsThePost(t *testing.T) {
	started := time.Unix(1700000300, 0)
	ps := newPubSlack(t)
	ps.stored(map[string]any{"ts": pubPosted, "thread_ts": pubParent, "user": me, "text": "on it"})
	ev, ok, err := ps.publisher().Verify(context.Background(), threadDraft(draft.Reply, "on it"), started)
	if err != nil || !ok || ev.Ref != pubPosted || ev.Permalink == "" {
		t.Fatalf("Verify = %+v %v %v", ev, ok, err)
	}
	replies := ps.calls("conversations.replies")
	if last := replies[len(replies)-1]; last.Get("ts") != pubParent || last.Get("oldest") != "1700000270.000000" {
		t.Fatalf("replies %v", last)
	}

	seg := ps.segmentUnit(t)
	ps.stored(map[string]any{"ts": pubPosted, "user": me, "text": "done"})
	if _, ok, err := ps.publisher().Verify(context.Background(), draft.Draft{Unit: seg, Kind: draft.Reply, Text: "done"}, started); err != nil || !ok {
		t.Fatalf("DM Verify = %v %v", ok, err)
	}
	if hist := ps.calls("conversations.history"); len(hist) != 1 || hist[0].Get("channel") != pubDM || hist[0].Get("oldest") == "" {
		t.Fatalf("history %v", hist)
	}
}

func TestVerifyMatchesNormalizedText(t *testing.T) {
	started := time.Unix(1700000300, 0)
	for name, tc := range map[string]struct{ text, stored string }{
		"autolink":        {"see https://example.com/a", "see <https://example.com/a>"},
		"autolink label":  {"see example.com", "see <http://example.com|example.com>"},
		"labeled mention": {"hey @{slack:U000AAA}", "hey <@U000AAA|dana>"},
		"whitespace":      {"one\n\ntwo  three ", "one\ntwo three"},
		"escapes":         {"a & b < c > d", "a &amp; b &lt; c &gt; d"},
		"markdown link":   {"[the PR](https://x.y/1)", "<https://x.y/1|the PR>"},
		"mailto":          {"mail dana@example.com", "mail <mailto:dana@example.com|dana@example.com>"},
	} {
		t.Run(name, func(t *testing.T) {
			ps := newPubSlack(t)
			ps.stored(map[string]any{"ts": pubPosted, "thread_ts": pubParent, "user": me, "text": tc.stored})
			if _, ok, err := ps.publisher().Verify(context.Background(), threadDraft(draft.Reply, tc.text), started); err != nil || !ok {
				t.Fatalf("Verify = %v %v; want a match of %q with %q", ok, err, send.Mrkdwn(tc.text), tc.stored)
			}
		})
	}
	ps := newPubSlack(t)
	ps.stored(map[string]any{"ts": pubPosted, "thread_ts": pubParent, "user": me, "text": "<https://x.y/1|another label>"})
	if _, ok, _ := ps.publisher().Verify(context.Background(), threadDraft(draft.Reply, "[the PR](https://x.y/1)"), started); ok {
		t.Fatal("a different label matched")
	}
}

func TestVerifyIgnoresOlderAndForeignPosts(t *testing.T) {
	ps := newPubSlack(t)
	ps.stored(
		map[string]any{"ts": "1700000200.000100", "thread_ts": pubParent, "user": me, "text": "on it"},
		map[string]any{"ts": pubPosted, "thread_ts": pubParent, "user": other, "text": "on it"},
	)
	if _, ok, err := ps.publisher().Verify(context.Background(), threadDraft(draft.Reply, "on it"), time.Unix(1700000300, 0)); err != nil || ok {
		t.Fatalf("Verify = %v %v", ok, err)
	}
}

func TestVerifyReact(t *testing.T) {
	d := threadDraft(draft.React, "")
	d.Reaction = "eyes"
	for _, tc := range []struct {
		users []string
		want  bool
	}{{[]string{other}, false}, {[]string{other, me}, true}} {
		ps := newPubSlack(t)
		ps.mu.Lock()
		ps.reacted = []map[string]any{{"name": "eyes", "users": tc.users, "count": len(tc.users)}}
		ps.mu.Unlock()
		_, ok, err := ps.publisher().Verify(context.Background(), d, time.Unix(1700000300, 0))
		if err != nil || ok != tc.want {
			t.Fatalf("users %v: Verify = %v %v", tc.users, ok, err)
		}
		if gets := ps.calls("reactions.get"); len(gets) != 1 || gets[0].Get("timestamp") != pubParent {
			t.Fatalf("reactions.get %v", gets)
		}
	}
}

func TestVerifyUnsureSaysCheck(t *testing.T) {
	ps := newPubSlack(t)
	ps.f.api.Handle("conversations.replies", func(url.Values) any { return slacktest.Response{Status: http.StatusServiceUnavailable, Body: "x"} })
	_, ok, err := ps.publisher().Verify(context.Background(), threadDraft(draft.Reply, "on it"), time.Unix(1700000300, 0))
	want := mayHaveBeenPosted + "; check https://slack.com/archives/" + pubConv + "/p" + strings.ReplaceAll(pubParent, ".", "")
	if ok || err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("Verify = %v %v, want %q", ok, err, want)
	}

	ps = newPubSlack(t)
	ps.f.api.Handle("conversations.info", func(url.Values) any { return map[string]any{"ok": false, "error": "invalid_auth"} })
	seg := ps.segmentUnit(t)
	_, _, err = ps.publisher().Verify(context.Background(), draft.Draft{Unit: seg, Kind: draft.Reply, Text: "x"}, time.Unix(1700000300, 0))
	if err == nil || !strings.HasPrefix(err.Error(), mayHaveBeenPosted+"; check https://slack.com/archives/"+pubDM) {
		t.Fatalf("logged-out Verify = %v", err)
	}
}

// refreshFails answers the first n token reads, then fails the refresh at the transport.
type refreshFails struct {
	n, calls int
}

func (r *refreshFails) Token(context.Context) (string, error) {
	r.calls++
	if r.calls > r.n {
		return "", fmt.Errorf("refresh: %w", &url.Error{Op: "Post", URL: "https://slack.example/api/oauth.v2.access", Err: context.DeadlineExceeded})
	}
	return fakeUserToken, nil
}

func TestPublishRefreshFailureIsNotPosted(t *testing.T) {
	ps := newPubSlack(t)
	p := ps.publisher()
	p.Send = &send.Service{Client: &slackapi.Client{BaseURL: ps.f.api.URL, Tokens: &refreshFails{n: 1}}}
	_, err := p.Publish(context.Background(), threadDraft(draft.Reply, "hi"))
	if err == nil || strings.HasPrefix(err.Error(), mayHaveBeenPosted) || !strings.Contains(err.Error(), "token") {
		t.Fatalf("err %v", err)
	}
	if n := len(ps.calls("chat.postMessage")); n != 0 {
		t.Fatalf("posted %d", n)
	}
}

func TestBroadcastMentionsAreRefused(t *testing.T) {
	for _, text := range []string{"heads up <!channel>", "<!here>", "<!everyone|everyone>", "ping <!subteam^S0001|@infra>", "`<!channel>`"} {
		ps := newPubSlack(t)
		p := ps.publisher()
		d := threadDraft(draft.Reply, text)
		if err := p.Validate(context.Background(), d); err == nil || !strings.Contains(err.Error(), "broadcast") {
			t.Fatalf("Validate(%q) = %v", text, err)
		}
		if _, err := p.Publish(context.Background(), d); err == nil || len(ps.calls("chat.postMessage")) != 0 {
			t.Fatalf("Publish(%q) = %v", text, err)
		}
	}
}

func TestTypedSlackTokensAreNeutralized(t *testing.T) {
	ps := newPubSlack(t)
	text := "cc <@U0000000042> in <#C0000000009|ops>, <!date^1700000000^{date}|x>, @channel @here and @{slack:U000AAA}; see <https://x.y/1|docs>"
	if _, err := ps.publisher().Publish(context.Background(), threadDraft(draft.Reply, text)); err != nil {
		t.Fatal(err)
	}
	got := ps.posted(t).Get("text")
	want := "cc &lt;@\u2060U0000000042&gt; in &lt;#C0000000009|ops&gt;, &lt;!date^1700000000^{date}|x&gt;, @\u2060channel @\u2060here and <@U000AAA>; see <https://x.y/1|docs>"
	if got != want {
		t.Fatalf("posted %q\nwant   %q", got, want)
	}
}

func TestLinkTargetsNeverBecomeTokens(t *testing.T) {
	for _, text := range []string{"[hi](!here)", "[hi](!channel)", "[x](!subteam^S0001)", "[x](@U0000000042)", "[x](#C0000000001)"} {
		ps := newPubSlack(t)
		if _, err := ps.publisher().Publish(context.Background(), threadDraft(draft.Reply, text)); err != nil {
			t.Fatalf("Publish(%q) = %v", text, err)
		}
		if got := ps.posted(t).Get("text"); strings.ContainsAny(got, "<>") {
			t.Fatalf("Publish(%q) posted %q", text, got)
		}
	}
}

func TestFinalTextKeepsOnlyConvertedMentions(t *testing.T) {
	if err := liveTokens("a " + holdOpen + "0" + holdClose + " <https://x.y|z> <mailto:a@b.c|m>"); err != nil {
		t.Fatalf("converted mention and links refused: %v", err)
	}
	for _, s := range []string{"<!here|hi>", "x <@U0000000042|x>", "<#C0000000001|x>", "<!channel>"} {
		if err := liveTokens(s); err == nil {
			t.Fatalf("liveTokens(%q) = nil", s)
		}
	}
}

func editDraft(text string) draft.Draft {
	d := threadDraft(draft.Edit, text)
	d.Target = "https://acme.slack.com/archives/" + pubConv + "/p" + strings.ReplaceAll(pubReply, ".", "") + "?thread_ts=" + pubParent + "&cid=" + pubConv
	return d
}

func (ps *pubSlack) updates() []url.Values { return ps.calls("chat.update") }

func newEditSlack(t *testing.T, author string) *pubSlack {
	t.Helper()
	ps := newPubSlack(t)
	ps.f.api.Handle("chat.update", func(v url.Values) any {
		return map[string]any{"channel": v.Get("channel"), "ts": v.Get("ts")}
	})
	ps.stored(map[string]any{"ts": pubReply, "thread_ts": pubParent, "user": author, "text": "release 1.2 :hourglass:"})
	return ps
}

func TestEditUpdatesTheUsersMessage(t *testing.T) {
	ps := newEditSlack(t, me)
	ev, err := ps.publisher().Publish(context.Background(), editDraft("release 1.2 **done**"))
	if err != nil {
		t.Fatal(err)
	}
	ups := ps.updates()
	if len(ups) != 1 || ups[0].Get("channel") != pubConv || ups[0].Get("ts") != pubReply || ups[0].Get("text") != send.Mrkdwn("release 1.2 **done**") {
		t.Fatalf("chat.update %v", ups)
	}
	if n := len(ps.calls("chat.postMessage")); n != 0 || ev.Ref != pubReply || ev.Permalink == "" {
		t.Fatalf("posts %d, evidence %+v", n, ev)
	}
}

func TestValidateRefusesAnEditOfAnotherOrNoMessage(t *testing.T) {
	ps := newEditSlack(t, other)
	if err := ps.publisher().Validate(context.Background(), editDraft("done")); err == nil || !strings.Contains(err.Error(), "only the user's own") {
		t.Fatalf("another's message: %v", err)
	}
	ps.stored()
	if err := ps.publisher().Validate(context.Background(), editDraft("done")); err == nil || !errors.Is(err, send.ErrCannotPost) {
		t.Fatalf("no message: %v", err)
	}
	d := editDraft("done")
	d.Target = ""
	if err := ps.publisher().Validate(context.Background(), d); err == nil || !strings.Contains(err.Error(), "permalink") {
		t.Fatalf("no permalink: %v", err)
	}
	if n := len(ps.updates()); n != 0 {
		t.Fatalf("validation edited %d times", n)
	}
}

func TestVerifyEdit(t *testing.T) {
	ps := newEditSlack(t, me)
	if _, ok, err := ps.publisher().Verify(context.Background(), editDraft("release 1.2 done"), time.Unix(1700000300, 0)); err != nil || ok {
		t.Fatalf("before the edit: %v %v", ok, err)
	}
	ps.stored(map[string]any{"ts": pubReply, "thread_ts": pubParent, "user": me, "text": "release 1.2 done", "edited": map[string]any{"ts": "1700000400.000000"}})
	ev, ok, err := ps.publisher().Verify(context.Background(), editDraft("release 1.2 done"), time.Unix(1700000300, 0))
	if err != nil || !ok || ev.Ref != pubReply {
		t.Fatalf("after the edit: %+v %v %v", ev, ok, err)
	}
}
