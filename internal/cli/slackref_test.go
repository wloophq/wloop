package cli

import (
	"context"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	slacksource "github.com/wloophq/wloop/internal/adapter/source/slack"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/pr"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
)

const (
	refSecond = "1700000000.000200"
	refThird  = "1700000000.000300"
)

// storeRefThread stores a thread of three messages in sessThreaded.
func storeRefThread(t *testing.T, w *sessionWiring) {
	t.Helper()
	facts := dslack.Facts{Team: "T000", Conversations: []dslack.Conversation{{ID: sessThreaded, Name: "random", Kind: dslack.KindChannel, IsMember: true}},
		Threads: []dslack.Thread{{Conversation: sessThreaded, TS: sessParent, Messages: []dslack.Message{
			{TS: sessParent, ThreadTS: sessParent, User: "U000", Text: "who owns the release?"},
			{TS: refSecond, ThreadTS: sessParent, User: sessUser, Text: "I can take it"},
			{TS: refThird, ThreadTS: sessParent, User: "U000", Text: "thanks, ship it friday"},
		}}}}
	if err := (&slacksource.Store{DB: w.db.Slack()}).Save(context.Background(), nil, facts, "", testNow); err != nil {
		t.Fatal(err)
	}
}

var printedRef = regexp.MustCompile(`(?m)^\[m3 (\S+)\]`)

func TestAnAgentReactsToTheThirdMessageOfAThreadByTheRefSlackReadPrints(t *testing.T) {
	t.Parallel()
	w := newSessionWiring(t)
	storeRefThread(t, w)
	w.st.srv.Handle("reactions.add", func(url.Values) any { return map[string]any{} })
	thread := dslack.ThreadUnit("T000", sessThreaded, sessParent)

	read, isErr := w.callTool(t, "", draftHandle, "slack_read", `{"target":"slack:`+thread.Key+`"}`)
	m := printedRef.FindStringSubmatch(read)
	if isErr || m == nil {
		t.Fatalf("slack_read = %q", read)
	}
	if strings.Contains(read, "ts=") {
		t.Fatalf("slack_read still prints a bare ts:\n%s", read)
	}
	got, isErr := w.callTool(t, "", draftHandle, "slack_react", `{"message":"`+m[1]+`","emoji":"white_check_mark"}`)
	if isErr || !strings.Contains(got, "#random") {
		t.Fatalf("slack_react %s = %q", m[1], got)
	}
	adds := w.st.srv.Calls("reactions.add")
	if len(adds) != 1 || adds[0].Get("channel") != sessThreaded || adds[0].Get("timestamp") != refThird || adds[0].Get("name") != "white_check_mark" {
		t.Fatalf("reactions.add %v", adds)
	}
	if ds := w.drafts(t); len(ds) != 1 || ds[0].Unit != thread || ds[0].Purpose != reactedPurpose || ds[0].Target != m[1] ||
		ds[0].Evidence.Ref != m[1] || !strings.Contains(ds[0].Evidence.Permalink, "p"+strings.ReplaceAll(refThird, ".", "")) {
		t.Fatalf("records %+v", ds)
	}
	if n := len(w.st.srv.Calls("conversations.replies")); n != 0 {
		t.Fatalf("the stored thread was read from Slack %d times", n)
	}
}

func TestASessionReplyToARefInAStoredThreadLandsInThatThread(t *testing.T) {
	t.Parallel()
	w := newSessionWiring(t)
	storeRefThread(t, w)
	if calls, want := w.send(t, sessThreaded+"/"+refThird), "conversations.info chat.postMessage chat.getPermalink"; calls != want {
		t.Fatalf("calls %s, want %s: the store places the reply", calls, want)
	}
	if posts := w.st.srv.Calls("chat.postMessage"); len(posts) != 1 || posts[0].Get("channel") != sessThreaded || posts[0].Get("thread_ts") != sessParent {
		t.Fatalf("posts %v", posts)
	}
	if ds := w.drafts(t); len(ds) != 1 || ds[0].Unit != dslack.ThreadUnit("T000", sessThreaded, sessParent) || ds[0].State != draft.Sent {
		t.Fatalf("records %+v", ds)
	}
}

func TestASessionReplyToARefNotStoredFindsItsThreadOnSlack(t *testing.T) {
	t.Parallel()
	w := newSessionWiring(t)
	calls := w.send(t, sessThreaded+"/"+sessReply)
	if !strings.Contains(calls, "conversations.replies") {
		t.Fatalf("calls %s: Slack was not asked for the thread", calls)
	}
	if posts := w.st.srv.Calls("chat.postMessage"); len(posts) != 1 || posts[0].Get("channel") != sessThreaded || posts[0].Get("thread_ts") != sessParent {
		t.Fatalf("posts %v", posts)
	}
	if ds := w.drafts(t); len(ds) != 1 || ds[0].Unit != dslack.ThreadUnit("T000", sessThreaded, sessParent) {
		t.Fatalf("records %+v", ds)
	}
}

func TestSlackDraftOnARefPostsInItsThreadOnceApproved(t *testing.T) {
	t.Parallel()
	w := newSessionWiring(t)
	storeRefThread(t, w)
	ctx := context.Background()
	if _, err := w.te.repo.Save(ctx, dturn.Book{Unit: pr.Ref{Owner: "acme", Repo: "widgets", Number: 7}.Unit(), Turns: []dturn.Turn{
		{Intent: "address", Trigger: dturn.ByHand, Session: draftHandle, State: dturn.Started, SentAt: testNow},
	}}); err != nil {
		t.Fatal(err)
	}
	ref := sessThreaded + "/" + refThird
	if got, isErr := w.draftTool(t, draftHandle, `{"text":"release 1.2 is out","target":"`+ref+`"}`); isErr || !strings.Contains(got, "#random") {
		t.Fatalf("slack_draft = %q", got)
	}
	ds := w.drafts(t)
	if len(ds) != 1 || ds[0].Unit != dslack.ThreadUnit("T000", sessThreaded, sessParent) || ds[0].Target != ref {
		t.Fatalf("drafts %+v", ds)
	}
	if _, err := w.deps.Outbox.Approve(ctx, ds[0].ID, nil); err != nil {
		t.Fatal(err)
	}
	if sent, _, err := w.deps.Outbox.Send(ctx); sent != 1 || err != nil {
		t.Fatalf("Send = %d, %v; draft %+v", sent, err, w.drafts(t)[0])
	}
	if posts := w.st.srv.Calls("chat.postMessage"); len(posts) != 1 || posts[0].Get("channel") != sessThreaded || posts[0].Get("thread_ts") != sessParent {
		t.Fatalf("posts %v", posts)
	}
	if n := len(w.st.srv.Calls("conversations.replies")); n != 0 {
		t.Fatalf("the stored thread was read from Slack %d times", n)
	}
}

func TestOutboxNewReactsToTheMessageItNames(t *testing.T) {
	w := newSessionWiring(t)
	storeRefThread(t, w)
	ctx := context.Background()
	d := w.deps
	d.Startup = nil
	w.te.Socket = serveSocket(t, daemon.New(d).Handler())
	wait := postWait
	postWait = 10 * time.Millisecond
	t.Cleanup(func() { postWait = wait })
	w.st.srv.Handle("reactions.add", func(url.Values) any { return map[string]any{} })
	tok := fakeToken()
	tok.Scopes = append(tok.Scopes, "reactions:write")
	w.st.saveToken(t, tok)

	ref := sessThreaded + "/" + refThird
	if code := w.te.run("outbox", "new", ref, "--react", ":eyes:", "--send", "--yes"); code != 0 {
		t.Fatalf("exit %d: %s %s", code, w.te.out, w.te.err)
	}
	ds := w.drafts(t)
	if len(ds) != 1 || ds[0].Unit != dslack.ThreadUnit("T000", sessThreaded, sessParent) || ds[0].Kind != draft.React || ds[0].Target != ref {
		t.Fatalf("drafts %+v", ds)
	}
	if sent, _, err := w.deps.Outbox.Send(ctx); sent != 1 || err != nil {
		t.Fatalf("Send = %d, %v; drafts %+v", sent, err, w.drafts(t))
	}
	if adds := w.st.srv.Calls("reactions.add"); len(adds) != 1 || adds[0].Get("channel") != sessThreaded || adds[0].Get("timestamp") != refThird {
		t.Fatalf("reactions.add %v, want one on the message named", adds)
	}
}
