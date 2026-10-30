package slack

import (
	"context"
	"net/url"
	"testing"

	"github.com/wloophq/wloop/plugins/slack/store"
)

func channelInfo(name string) func(url.Values) any {
	return func(v url.Values) any {
		return map[string]any{"channel": map[string]any{"id": v.Get("channel"), "name": name, "is_channel": true, "is_member": true}}
	}
}

func storedName(t *testing.T, f *fixture, conv string) string {
	t.Helper()
	rows, err := f.db.Slack().Conversations(context.Background(), []string{conv})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		return ""
	}
	return rows[0].Name
}

func TestIngestNamesAChannelOnce(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	f.api.Handle("conversations.info", channelInfo("team-alpha"))
	w := newWorld(f)
	w.post(chan1, other, ts(5), "<@"+me+"> look")
	f.push(t, event("Ev1", evMsg(chan1, "channel", other, ts(5), "<@"+me+"> look")))
	if got := storedName(t, f, chan1); got != "team-alpha" {
		t.Fatalf("stored name %q", got)
	}
	w.post(chan1, other, ts(6), "<@"+me+"> again")
	f.push(t, event("Ev2", evMsg(chan1, "channel", other, ts(6), "<@"+me+"> again")))
	if n := len(f.api.Calls("conversations.info")); n != 1 {
		t.Fatalf("conversations.info %d calls, want 1", n)
	}
}

func TestIngestNeverAsksTheNameOfADM(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	w := newWorld(f)
	w.post(dm1, other, ts(5), "hi")
	f.push(t, event("Ev1", evMsg(dm1, "im", other, ts(5), "hi")))
	if n := len(f.api.Calls("conversations.info")); n != 0 {
		t.Fatalf("conversations.info %d calls for a DM", n)
	}
}

func TestCatchUpNamesTheChannelsOfWatchedThreads(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	w := newWorld(f)
	watchedThread(t, f, w)
	if got := storedName(t, f, chan1); got != "" {
		t.Fatalf("seeded name %q", got)
	}
	f.api.Handle("conversations.info", channelInfo("team-alpha"))
	f.p.poll.Search = (&reasons{}).search
	if err := f.p.poll.catchUp(context.Background(), ReasonStart); err != nil {
		t.Fatal(err)
	}
	if got := storedName(t, f, chan1); got != "team-alpha" {
		t.Fatalf("stored name %q", got)
	}
	if err := f.p.poll.catchUp(context.Background(), ReasonReconnect); err != nil {
		t.Fatal(err)
	}
	if n := len(f.api.Calls("conversations.info")); n != 1 {
		t.Fatalf("conversations.info %d calls, want 1", n)
	}
}

// namedChannel stores conv as a named channel, so ingesting it asks Slack nothing.
func (f *fixture) namedChannel(t testing.TB, conv string) {
	t.Helper()
	if err := f.db.Slack().PutConversations(context.Background(), []store.ConversationRow{{ID: conv, Team: team, Name: "channel-" + conv, Kind: "channel"}}); err != nil {
		t.Fatal(err)
	}
}
