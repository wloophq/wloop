package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
)

func init() {
	before, _ := slackMessagesQuery("D0001", MessageQuery{Before: "1700000009.000100", Newest: true, Limit: 10})
	hotPlans = append(hotPlans, hotPlan{name: "the messages before a slack segment", query: before, args: 3,
		want: []string{"SEARCH slack_messages USING INDEX slack_messages_anchor (conversation=? AND <expr><?)"}})
}

func TestMessagesBeforeNewestFirstReturnedAscending(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "s.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	var msgs []MessageRow
	for i := 1; i <= 6; i++ {
		msgs = append(msgs, MessageRow{TS: fmt.Sprintf("170000000%d.000100", i), User: "U0001", Text: "text"})
	}
	msgs[4].ThreadTS = msgs[1].TS
	if _, err := db.Slack().Save(ctx, FactsRows{Team: "T0001", Objects: []ObjectFacts{{
		Object:   ObjectRow{Object: "D0001", Conversation: "D0001", Kind: SlackDM, Version: "1"},
		Messages: msgs,
	}}}, noteAt); err != nil {
		t.Fatal(err)
	}
	got, err := db.Slack().Messages(ctx, "D0001", MessageQuery{Before: msgs[3].TS, Newest: true, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	var tss []string
	for _, m := range got {
		tss = append(tss, m.TS)
	}
	if want := []string{msgs[2].TS, msgs[4].TS}; !slices.Equal(tss, want) {
		t.Fatalf("got %v, want %v: the newest 2 anchored before, a reply to an older parent included, ascending", tss, want)
	}
}
