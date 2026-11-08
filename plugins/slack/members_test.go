package slack

import (
	"context"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/items"
	"github.com/wloophq/wloop/plugins/slack/store"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

func waitingItem(f *fixture, id string) {
	at := monday.Add(-72 * time.Hour)
	f.host.Put(items.Item{ID: id, Unit: factsThread, Kind: items.Request, Summary: "s " + id, Origin: unit.Anchor{Unit: factsThread, Ref: "m:" + id},
		Requester: items.Person{Key: "slack:" + me}, Basis: items.NoBasis, State: items.Open, Confidence: items.High,
		Provenance: items.Extracted, WaitingOn: []items.Person{{Key: "slack:" + other}}, CreatedAt: at, UpdatedAt: at})
}

func TestADMsMembersAreItsTwoPeopleWithNoCall(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFactsFixture(t)
	f.login(t)
	if err := f.db.Slack().PutConversations(ctx, []store.ConversationRow{{ID: "D0001", Team: "T0001", Kind: string(dslack.KindIM), DMUser: other}}); err != nil {
		t.Fatal(err)
	}
	f.listed(t, factsDM)
	got, _ := f.facts(t, factsDM)
	if want := []string{"slack:" + me, "slack:" + other}; !slices.Equal(got["slack.members"].([]string), want) {
		t.Fatalf("members %v, want %v", got["slack.members"], want)
	}
	if calls := f.api.Calls("conversations.members"); len(calls) != 0 {
		t.Errorf("%d conversations.members calls for a DM", len(calls))
	}
}

func TestAChannelsMembersAreReadOnceADay(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFactsFixture(t)
	f.login(t)
	if err := f.db.Slack().PutConversations(ctx, []store.ConversationRow{{ID: "C0001", Team: "T0001", Name: "general", Kind: string(dslack.KindChannel)}}); err != nil {
		t.Fatal(err)
	}
	f.listed(t, factsThread)
	waitingItem(f, "i1")
	pages := map[string]any{"": map[string]any{"members": []string{me, other}, "response_metadata": map[string]any{"next_cursor": "c2"}},
		"c2": map[string]any{"members": []string{"U0000000003"}}}
	f.api.Handle("conversations.members", func(v url.Values) any { return pages[v.Get("cursor")] })
	if got, _ := f.facts(t, factsThread); got["slack.members"] != nil {
		t.Fatalf("members %v before any read, want none", got["slack.members"])
	}
	if err := f.p.refreshMembers(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := f.facts(t, factsThread)
	if want := []string{"slack:" + me, "slack:" + other, "slack:U0000000003"}; !slices.Equal(got["slack.members"].([]string), want) {
		t.Fatalf("members %v, want %v", got["slack.members"], want)
	}
	f.now = f.now.Add(membersTTL - time.Minute)
	if err := f.p.refreshMembers(ctx); err != nil {
		t.Fatal(err)
	}
	if calls := f.api.Calls("conversations.members"); len(calls) != 2 {
		t.Fatalf("%d calls within a day, want the two pages of the first read", len(calls))
	}
	f.now = f.now.Add(2 * time.Minute)
	if err := f.p.refreshMembers(ctx); err != nil {
		t.Fatal(err)
	}
	if calls := f.api.Calls("conversations.members"); len(calls) != 4 {
		t.Fatalf("%d calls after a day, want a second read", len(calls))
	}
}

func TestAFailedMembersReadLeavesThemUnknownAndIsLogged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFactsFixture(t)
	f.login(t)
	if err := f.db.Slack().PutConversations(ctx, []store.ConversationRow{{ID: "C0001", Team: "T0001", Name: "general", Kind: string(dslack.KindChannel)}}); err != nil {
		t.Fatal(err)
	}
	f.listed(t, factsThread)
	waitingItem(f, "i1")
	f.api.Handle("conversations.members", func(url.Values) any { return map[string]any{"ok": false, "error": "channel_not_found"} })
	if err := f.p.refreshMembers(ctx); err != nil {
		t.Fatalf("a failed read failed the refresh: %v", err)
	}
	if got, _ := f.facts(t, factsThread); got["slack.members"] != nil {
		t.Errorf("members %v after a failed read, want none", got["slack.members"])
	}
	if logs := f.logs.String(); !strings.Contains(logs, "reading who is in C0001") {
		t.Errorf("logs %q, want the failed read", logs)
	}
}
