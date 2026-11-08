package store

import (
	"context"
	"slices"
	"testing"
	"time"
)

func init() {
	reads = append(reads, "SlackStore.Members")
	writes = append(writes, writeCase{method: "SlackStore.PutMembers", call: func(ctx context.Context, e *noteEnv) error {
		return e.db.Slack().PutMembers(ctx, MembersRow{Conversation: "C0001", Members: []string{"U0000000001"}, Fetched: noteAt})
	}})
}

func TestAConversationsMembersRoundTripAndAreReplaced(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	for _, r := range []MembersRow{
		{Conversation: "C0001", Members: []string{"U0000000001", "U0000000002"}, Fetched: at},
		{Conversation: "C0001", Members: []string{"U0000000003"}, Fetched: at.Add(time.Hour)},
	} {
		if err := db.Slack().PutMembers(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.Slack().Members(ctx, []string{"C0001", "C0002"})
	if err != nil {
		t.Fatal(err)
	}
	m, ok := got["C0001"]
	if len(got) != 1 || !ok || !slices.Equal(m.Members, []string{"U0000000003"}) || !m.Fetched.Equal(at.Add(time.Hour)) {
		t.Fatalf("members %+v, want the second read of C0001 alone", got)
	}
	for _, r := range []MembersRow{{Conversation: "C0002", Fetched: at}, {Conversation: "C0003", Fetched: at, Failed: true}} {
		if err := db.Slack().PutMembers(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	got, err = db.Slack().Members(ctx, []string{"C0002", "C0003"})
	if err != nil {
		t.Fatal(err)
	}
	if got["C0002"].Failed || got["C0002"].Members == nil || !got["C0003"].Failed {
		t.Fatalf("members %+v, want C0002 read with nobody and C0003's read failed", got)
	}
}
