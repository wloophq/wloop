package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var slackNoteUnit = unit.Unit{Provider: "slack", Key: "T0001/C0001/1700000000.000100"}

func slackNoteFacts() FactsRows {
	return FactsRows{Team: "T0001", Objects: []ObjectFacts{{
		Object:   ObjectRow{Object: "C0001/1700000000.000100", Conversation: "C0001", TS: "1700000000.000100", Kind: SlackThread, Version: "1700000000.000100"},
		Messages: []MessageRow{{TS: "1700000000.000100", User: "U0001", Text: "text-1", Units: []unit.Unit{slackNoteUnit}}},
	}}}
}

func init() {
	reads = append(reads, "DB.Slack", "SlackStore.Watched", "SlackStore.Object", "SlackStore.Versions", "SlackStore.Conversations",
		"SlackStore.AdmittedConversations", "SlackStore.Users", "SlackStore.UserByEmail", "SlackStore.Messages", "SlackStore.Changes",
		"SlackStore.Cursor", "SlackStore.Permalink")
	quiet := func(method string, call func(ctx context.Context, e *noteEnv) error) writeCase {
		return writeCase{method: method, call: call, seed: func(t *testing.T, e *noteEnv) {
			if _, err := e.db.Slack().Save(context.Background(), slackNoteFacts(), noteAt); err != nil {
				t.Fatal(err)
			}
		}}
	}
	writes = append(writes,
		writeCase{
			method: "SlackStore.Save",
			call: func(ctx context.Context, e *noteEnv) error {
				_, err := e.db.Slack().Save(ctx, slackNoteFacts(), noteAt)
				return err
			},
			topics: port.TopicSourceEvents, units: []unit.Unit{slackNoteUnit},
		},
		quiet("SlackStore.MarkSeen", func(ctx context.Context, e *noteEnv) error {
			return e.db.Slack().MarkSeen(ctx, "C0002", "1700000000.000900")
		}),
		quiet("SlackStore.See", func(ctx context.Context, e *noteEnv) error {
			return e.db.Slack().See(ctx, []string{"C0001/1700000000.000100"}, nil, noteAt)
		}),
		quiet("SlackStore.Schedule", func(ctx context.Context, e *noteEnv) error {
			return e.db.Slack().Schedule(ctx, "C0001/1700000000.000100", noteAt)
		}),
		quiet("SlackStore.SetWatched", func(ctx context.Context, e *noteEnv) error {
			return e.db.Slack().SetWatched(ctx, "C0001/1700000000.000100", false, "")
		}),
		writeCase{
			method: "SlackStore.PutConversations", name: "/named",
			seed: func(t *testing.T, e *noteEnv) {
				if _, err := e.db.Slack().Save(context.Background(), slackNoteFacts(), noteAt); err != nil {
					t.Fatal(err)
				}
			},
			call: func(ctx context.Context, e *noteEnv) error {
				return e.db.Slack().PutConversations(ctx, []ConversationRow{{ID: "C0001", Team: "T0001", Name: "name-1", Kind: "channel"}})
			},
			topics: port.TopicSourceEvents, units: []unit.Unit{slackNoteUnit},
		},
		writeCase{
			method: "SlackStore.PutConversations", name: "/same name",
			seed: func(t *testing.T, e *noteEnv) {
				if _, err := e.db.Slack().Save(context.Background(), slackNoteFacts(), noteAt); err != nil {
					t.Fatal(err)
				}
				if err := e.db.Slack().PutConversations(context.Background(), []ConversationRow{{ID: "C0001", Team: "T0001", Name: "name-1", Kind: "channel"}}); err != nil {
					t.Fatal(err)
				}
			},
			call: func(ctx context.Context, e *noteEnv) error {
				return e.db.Slack().PutConversations(ctx, []ConversationRow{{ID: "C0001", Kind: "channel"}})
			},
		},
		quiet("SlackStore.PutUsers", func(ctx context.Context, e *noteEnv) error {
			return e.db.Slack().PutUsers(ctx, []UserRow{{ID: "U0001", Name: "name-1"}})
		}),
		quiet("SlackStore.SetCursor", func(ctx context.Context, e *noteEnv) error {
			return e.db.Slack().SetCursor(ctx, "search", "1700000000.000100")
		}),
	)
	thread, _ := slackMessagesQuery("C0001", MessageQuery{Thread: "1700000000.000100", After: "1700000000.000100"})
	segment, _ := slackMessagesQuery("C0001", MessageQuery{From: "1700000000.000100", To: "1700000009.000100"})
	hotPlans = append(hotPlans,
		hotPlan{name: "messages of a slack thread", query: thread, args: 3,
			want: []string{"SEARCH slack_messages USING INDEX slack_messages_anchor (conversation=? AND <expr>=?)"}},
		hotPlan{name: "messages of a slack segment", query: segment, args: 3,
			want:     []string{"SEARCH slack_messages USING INDEX slack_messages_anchor (conversation=? AND <expr>>? AND <expr><?)"},
			tempSort: "USE TEMP B-TREE FOR ORDER BY"},
		hotPlan{name: "due slack objects", query: slackWatchedSQL, args: 1,
			want: []string{"SEARCH slack_objects USING INDEX slack_objects_due (next_check_at<?)"}},
		hotPlan{name: "a slack user by email", query: slackUserByEmail, args: 1,
			want: []string{"SEARCH slack_users USING INDEX slack_users_email (email=?)"}, tempSort: "USE TEMP B-TREE FOR ORDER BY"},
		hotPlan{name: "reactions of slack messages", query: slackReactionsOfSQL, args: 2,
			want: []string{"SEARCH slack_reactions USING PRIMARY KEY (conversation=? AND ts=?)"}},
	)
}

func TestSlackTimesSortAsText(t *testing.T) {
	t.Parallel()
	a := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	b := a.Add(500 * time.Millisecond)
	c := a.Add(time.Second)
	if !(slackTime(a) <= slackTime(b) && slackTime(b) < slackTime(c)) {
		t.Errorf("%s %s %s do not sort", slackTime(a), slackTime(b), slackTime(c))
	}
}

func TestCompareSlackTS(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"1700000000.000100", "1700000000.000100", 0},
		{"999999999.900000", "1700000000.000000", -1},
		{"1700000000.1", "1700000000.000100", 1},
		{"", "1.0", -1},
	} {
		if got := compareSlackTS(c.a, c.b); got != c.want {
			t.Errorf("compareSlackTS(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestPutConversationsKeepsAKnownName(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	ctx := context.Background()
	s := db.Slack()
	if err := s.PutConversations(ctx, []ConversationRow{{ID: "C0001", Team: "T0001", Name: "name-1", Kind: "channel"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutConversations(ctx, []ConversationRow{{ID: "C0001", Kind: "channel"}}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Conversations(ctx, []string{"C0001"})
	if err != nil || len(rows) != 1 || rows[0].Name != "name-1" || rows[0].Team != "T0001" {
		t.Fatalf("rows %+v, %v; want name-1 kept", rows, err)
	}
}
