package slack

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/wloophq/wloop/plugins/slack/store"
)

func TestKnownOverReadsTheStoresConversations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	st := db.Slack()
	if err := st.PutConversations(ctx, []store.ConversationRow{
		{ID: "C000AAA", Team: "T000AAA", Name: "general", Kind: "channel", IsMember: true},
		{ID: "C000BBB", Team: "T000AAA", Kind: "channel", IsMember: true},
		{ID: "D000AAA", Team: "T000AAA", Kind: "im", DMUser: "U000AAA"},
		{ID: "G000AAA", Team: "T000AAA", Kind: "mpim", IsMember: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutUsers(ctx, []store.UserRow{{ID: "U000AAA", Name: "alice", DisplayName: "Alice"}}); err != nil {
		t.Fatal(err)
	}
	known := KnownOver(db, t.Logf)
	if c, ok := known(ctx, "C000AAA"); !ok || c.Name != "general" || c.Kind != "channel" || !c.IsMember {
		t.Fatalf("a channel: %+v %v", c, ok)
	}
	if c, ok := known(ctx, "D000AAA"); !ok || c.Kind != "im" || c.DMUser != "U000AAA" || c.DMUserName != "alice" {
		t.Fatalf("a DM: %+v %v", c, ok)
	}
	if c, ok := known(ctx, "G000AAA"); !ok || c.Kind != "mpim" {
		t.Fatalf("a group DM: %+v %v", c, ok)
	}
	for _, id := range []string{"C000BBB", "C000ZZZ"} {
		if c, ok := known(ctx, id); ok {
			t.Fatalf("%s is left to Slack, got %+v", id, c)
		}
	}
}
