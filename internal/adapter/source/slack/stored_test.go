package slack

import (
	"context"
	"strings"
	"testing"

	dslack "github.com/wloophq/wloop/internal/domain/slack"
)

func TestStoredOverLeavesToSlackWhatTheStoreCannotPlace(t *testing.T) {
	f := newFixture(t)
	const (
		lone     = "1700000000.000300"
		searched = "1700000000.000400"
		deleted  = "1700000000.000500"
	)
	fs := dslack.Facts{Team: team,
		Conversations: []dslack.Conversation{{ID: readConv, Name: "general", Kind: dslack.KindChannel, IsMember: true}},
		Threads: []dslack.Thread{
			{Conversation: readConv, TS: readParent, LatestReply: readReply, ReplyCount: 1, Messages: []dslack.Message{
				{TS: readParent, ThreadTS: readParent, User: other, Text: "parent", Permalink: readPermalink(readParent, "")},
				{TS: readReply, ThreadTS: readParent, User: me, Text: "reply"},
			}},
			{Conversation: readConv, TS: lone, Messages: []dslack.Message{{TS: lone, User: other, Text: "lone"}}},
			{Conversation: readConv, TS: searched, Messages: []dslack.Message{{TS: searched, User: other, Text: "searched"}}},
			{Conversation: readConv, TS: deleted, Messages: []dslack.Message{{TS: deleted, User: other, Deleted: true}}},
		}}
	via := map[string]dslack.Via{readConv + "/" + searched: dslack.ViaSearch}
	if err := f.p.src.store.Save(context.Background(), nil, facts{Facts: fs, via: via}, "", f.now); err != nil {
		t.Fatal(err)
	}
	var logged []string
	stored := StoredOver(f.db, func(format string, _ ...any) { logged = append(logged, format) })
	ctx := context.Background()
	for ts, want := range map[string]bool{readParent: true, readReply: true, lone: true, searched: false, deleted: false, "1700000000.000900": false} {
		got, ok := stored(ctx, readConv, ts)
		if ok != want || (ok && got.TS != ts) {
			t.Errorf("%s: %+v %v, want ok %v", ts, got, ok, want)
		}
	}
	if got, _ := stored(ctx, readConv, readParent); got.Permalink != readPermalink(readParent, "") {
		t.Errorf("permalink %q", got.Permalink)
	}
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := stored(ctx, readConv, readParent); ok || len(logged) != 1 || !strings.Contains(logged[0], "error") {
		t.Fatalf("a store error: ok %v, logged %v", ok, logged)
	}
}
