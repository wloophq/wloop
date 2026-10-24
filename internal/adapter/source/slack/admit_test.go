package slack

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/config"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
)

func TestAdmission(t *testing.T) {
	parent := ts(1)
	thread := func(user, parentUser, text string) map[string]any {
		return evMsg(chan1, "channel", user, ts(2), text, "thread_ts", parent, "parent_user_id", parentUser)
	}
	cases := []struct {
		name  string
		watch dslack.WatchReason
		ev    map[string]any
		rule  string
	}{
		{name: RuleMention, ev: evMsg(chan1, "channel", other, ts(1), "hi <@"+me+">"), rule: RuleMention},
		{name: RuleTerm, ev: evMsg(chan1, "channel", other, ts(1), "the Deploy-Bot broke"), rule: RuleTerm},
		{name: "term inside a word", ev: evMsg(chan1, "channel", other, ts(1), "redeploying now")},
		{name: RuleRepliedTo, ev: thread(other, me, "done"), rule: RuleRepliedTo},
		{name: "own reply to own thread", ev: thread(me, me, "bump"), rule: RuleWroteIn},
		{name: RuleWroteIn, ev: thread(me, other, "looking"), rule: RuleWroteIn},
		{name: RuleDM, ev: evMsg(dm1, "im", other, ts(1), "ping"), rule: RuleDM},
		{name: "group dm", ev: evMsg("G0001", "mpim", other, ts(1), "ping"), rule: RuleDM},
		{name: RuleFollowed, watch: dslack.ReasonUserFollow, ev: thread(other, other, "news"), rule: RuleFollowed},
		{name: RuleWatched, watch: dslack.ReasonExtraction, ev: thread(other, other, "news"), rule: RuleWatched},
		{name: "none", ev: thread(other, other, "news")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t, withConfig(func(i *config.SlackIngest) { i.Terms = []string{"deploy"} }))
			f.login(t)
			ctx := context.Background()
			if c.watch != "" {
				if err := (Watches{f.db.Slack()}).Watch(ctx, dslack.ObjectRef{Conversation: chan1, TS: parent}, c.watch); err != nil {
					t.Fatal(err)
				}
			}
			v, err := f.p.src.admit.admit(ctx, delta(t, event("Ev1", c.ev)))
			if err != nil {
				t.Fatal(err)
			}
			if v.rule != c.rule {
				t.Fatalf("rule %q, want %q", v.rule, c.rule)
			}
		})
	}
}

func TestAgentAppPostIsTheHuman(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.push(t, event("Ev1", evMsg(chan1, "channel", other, ts(1), "<@"+me+"> can you look", "bot_id", "B0001", "app_id", "A0001",
		"username", "agent")))
	ms := f.stored(t, chan1)
	if len(ms) != 1 {
		t.Fatalf("stored %d messages", len(ms))
	}
	if m := ms[0]; m.User != other || m.BotID != "B0001" || m.AppID != "A0001" || m.Username != "agent" {
		t.Fatalf("stored %+v", m)
	}
}

func TestIntegrationPosts(t *testing.T) {
	cases := []struct {
		name  string
		allow []string
		deny  []string
		bot   bool
		ev    map[string]any
		kept  bool
	}{
		{name: "bot_message with no user", ev: evMsg(dm1, "im", "", ts(1), "alert", "subtype", "bot_message", "bot_id", "B0001")},
		{name: "allowed bot", allow: []string{"B0001"}, ev: evMsg(dm1, "im", "", ts(1), "alert", "subtype", "bot_message", "bot_id", "B0001"), kept: true},
		{name: "allowed app", allow: []string{"A0001"}, ev: evMsg(dm1, "im", "", ts(1), "alert", "subtype", "bot_message", "bot_id", "B0001", "app_id", "A0001"), kept: true},
		{name: "denied app with a human", deny: []string{"A0002"}, ev: evMsg(dm1, "im", other, ts(1), "<@"+me+">", "bot_id", "B0002", "app_id", "A0002")},
		{name: "slackbot", ev: evMsg(dm1, "im", "USLACKBOT", ts(1), "reminder")},
		{name: "bot user", bot: true, ev: evMsg(dm1, "im", other, ts(1), "hello")},
		{name: "human", ev: evMsg(dm1, "im", other, ts(1), "hello"), kept: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t, withConfig(func(i *config.SlackIngest) { i.AllowApps, i.DenyApps = c.allow, c.deny }))
			f.login(t)
			f.knownUser(t, other, c.bot)
			f.push(t, event("Ev1", c.ev))
			if got := len(f.stored(t, dm1)) == 1; got != c.kept {
				t.Fatalf("kept %v, want %v", got, c.kept)
			}
		})
	}
}

func TestUnfurlEditIsDropped(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	text := "see <https://example.com/x> <@" + me + ">"
	f.push(t, event("Ev1", evMsg(chan1, "channel", other, ts(1), text)))
	edit := func(id, text string) dslack.Delta {
		return delta(t, event(id, map[string]any{"type": "message", "subtype": "message_changed", "channel": chan1, "channel_type": "channel",
			"ts": ts(3), "message": map[string]any{"type": "message", "user": other, "ts": ts(1), "text": text,
				"attachments": []any{map[string]any{"from_url": "https://example.com/x"}}}}))
	}
	ctx := context.Background()
	v, err := f.p.src.admit.admit(ctx, edit("Ev2", text))
	if err != nil || v.rule != "" {
		t.Fatalf("unfurl admitted: %+v %v", v, err)
	}
	if v, err = f.p.src.admit.admit(ctx, edit("Ev3", text+" edited")); err != nil || v.object != dslack.ThreadObject(chan1, ts(1)) {
		t.Fatalf("a real edit: %+v %v", v, err)
	}
}

func TestDroppedIsNeverStored(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.push(t,
		event("Ev1", evMsg(chan1, "channel", other, ts(5), "text-secret")),
		event("Ev2", evMsg(chan1, "channel", "", ts(6), "text-secret", "subtype", "bot_message", "bot_id", "B0001")),
		event("Ev3", map[string]any{"type": "message", "subtype": "message_changed", "channel": chan1, "channel_type": "channel", "ts": ts(7),
			"message": map[string]any{"type": "message", "user": other, "ts": ts(4), "text": "text-secret"}}),
		event("Ev4", map[string]any{"type": "reaction_added", "user": other, "reaction": "eyes",
			"item": map[string]any{"type": "message", "channel": chan1, "ts": ts(5)}}),
		event("Ev5", map[string]any{"type": "reaction_removed", "user": other, "reaction": "eyes",
			"item": map[string]any{"type": "message", "channel": chan1, "ts": ts(5)}}),
		event("Ev6", map[string]any{"type": "message", "subtype": "message_deleted", "channel": chan1, "channel_type": "channel",
			"ts": ts(8), "deleted_ts": ts(5), "previous_message": map[string]any{"type": "message", "user": other, "ts": ts(5), "text": "text-secret"}}),
		event("Ev7", map[string]any{"type": "message", "subtype": "message_changed", "channel": chan1, "channel_type": "channel", "ts": ts(9),
			"message": map[string]any{"type": "message", "user": other, "ts": ts(5), "text": "text-secret v2"}}))
	if n := len(f.pendingObjects()); n != 0 {
		t.Fatalf("%d objects wait in the inbox", n)
	}
	ctx := context.Background()
	rows, err := f.db.R().QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name LIKE 'slack_%'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, n)
	}
	rows.Close()
	if len(tables) < 7 {
		t.Fatalf("tables %v", tables)
	}
	for _, table := range tables {
		if hits := rowsHolding(t, f, table, "text-secret"); hits > 0 {
			t.Errorf("%s: %d rows hold the dropped text", table, hits)
		}
	}
	for _, table := range []string{"slack_objects", "slack_messages", "slack_changes", "slack_reactions"} {
		var n int
		if err := f.db.R().QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s: %d rows, %v", table, n, err)
		}
	}
	convs, err := f.db.Slack().Conversations(ctx, []string{chan1})
	if err != nil || len(convs) != 1 || convs[0].HWM != ts(6) || convs[0].Admitted {
		t.Fatalf("conversation %+v %v", convs, err)
	}
	if n := len(f.api.Calls("users.info")); n != 0 {
		t.Fatalf("a dropped message cost %d users.info calls", n)
	}
}

func rowsHolding(t *testing.T, f *fixture, table, text string) int {
	t.Helper()
	rows, err := f.db.R().QueryContext(context.Background(), `SELECT * FROM `+table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	hits := 0
	for rows.Next() {
		vals := make([]sql.RawBytes, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		if slices.ContainsFunc(vals, func(v sql.RawBytes) bool { return strings.Contains(string(v), text) }) {
			hits++
		}
	}
	return hits
}

func TestDMRepliesBelongToTheDM(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	f.push(t, event("Ev1", evMsg(dm1, "im", other, ts(1), "question")))
	v, err := f.p.src.admit.admit(context.Background(), delta(t, event("Ev2", evMsg(dm1, "im", other, ts(2), "detail", "thread_ts", ts(1)))))
	if err != nil || v.object != dslack.DMObject(dm1) {
		t.Fatalf("object %q, %v", v.object, err)
	}
	f.p.deliver(context.Background(), delta(t, event("Ev2", evMsg(dm1, "im", other, ts(2), "detail", "thread_ts", ts(1)))))
	f.flush(t)
	if ms := f.stored(t, dm1); len(ms) != 2 || ms[1].ThreadTS != ts(1) {
		t.Fatalf("stored %+v", ms)
	}
	if n := len(f.api.Calls("conversations.replies")); n != 0 {
		t.Fatalf("a reply to a stored parent cost %d replies calls", n)
	}
}

func TestPrecedingTS(t *testing.T) {
	for in, want := range map[string]string{"1700000001.000100": "1700000001.000099", "1700000001.000000": "1700000000.999999"} {
		if got, ok := precedingTS(in); !ok || got != want {
			t.Errorf("precedingTS(%s) = %s, want %s", in, got, want)
		}
	}
	if _, ok := precedingTS("0000000000.000000"); ok {
		t.Error("no ts precedes zero")
	}
}
