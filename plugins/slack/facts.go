package slack

import (
	"cmp"
	"slices"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/items"
	slackrows "github.com/wloophq/wloop/plugins/slack/rows"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

// unitFacts are u's workflow facts in slot slack at now, and when the
// next of them changes with time alone; slack.settled_at is the host's.
func unitFacts(u unit.Unit, in *slackrows.Input, tasks []src.Task, endedAfter time.Duration, now time.Time) ([]src.Fact, time.Time) {
	k := in.Key()
	st, _ := in.State()
	newest := ""
	if msgs := in.Messages(); len(msgs) > 0 {
		newest = msgs[len(msgs)-1].TS
	}
	open, orphans, age := 0, 0, 0
	var updated, next time.Time
	for _, it := range in.Items() {
		updated = later(updated, it.UpdatedAt)
		if it.State == items.Open {
			open++
		}
		if !unowned(it) {
			continue
		}
		orphans++
		if it.CreatedAt.IsZero() {
			continue
		}
		n := workdaysSince(it.CreatedAt, now, now.Location())
		age = max(age, n)
		if at := addWorkdays(it.CreatedAt, n+1, now.Location()); next.IsZero() || at.Before(next) {
			next = at
		}
	}
	closure := st.ClosurePosted || st.ClosureMarked
	ended := src.After(dslack.TSTime(newest), endedAfter-time.Nanosecond, src.Calendar)
	if closure {
		ended = src.Bool(true)
	}
	return []src.Fact{
		{Name: "slack.channel", Value: src.String(k.Conversation)},
		{Name: "slack.conversation", Value: src.String(conversationOf(k, in.Conversation().Kind))},
		{Name: "slack.started_by_me", Value: src.Bool(st.StartedByViewer)},
		{Name: "slack.closure_posted", Value: src.Bool(closure)},
		{Name: "slack.ended", Value: ended},
		{Name: "slack.live_task", Value: src.Bool(slices.ContainsFunc(tasks, func(t src.Task) bool { return liveOn(t, u) }))},
		{Name: "slack.items_open", Value: src.Int(int64(open))},
		{Name: "slack.items_unowned", Value: src.Int(int64(orphans))},
		{Name: "slack.items_unowned_age_days", Value: src.Int(int64(age))},
		{Name: "slack.settled_at", Value: src.Time(time.Time{})},
		{Name: "slack.fingerprint", Value: src.String(u.String() + "|" + newest + "|" + updated.UTC().Format(time.RFC3339Nano))},
	}, next
}

// conversationOf is the kind of k's conversation: dm, group (a multi-person
// DM) or channel, public or private.
func conversationOf(k dslack.Key, stored string) string {
	kind := dslack.KindChannel
	if k.Segment != "" {
		kind = dslack.KindIM
	}
	if stored != "" {
		kind = dslack.ConversationKind(stored)
	}
	switch kind {
	case dslack.KindIM:
		return "dm"
	case dslack.KindMPIM:
		return "group"
	}
	return "channel"
}

// liveOn says whether t is an open task attached to u.
func liveOn(t src.Task, u unit.Unit) bool {
	return t.Category != "done" && t.Category != "dropped" && slices.Contains(t.Units, u.Wire())
}

// unowned says whether it is open with no assignee, nobody waited on and no
// basis.
func unowned(it items.Item) bool {
	return it.State == items.Open && len(it.Assignees)+len(it.WaitingOn) == 0 && cmp.Or(it.Basis, items.NoBasis) == items.NoBasis
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// addWorkdays moves t n working days on, Saturday and Sunday skipped, in loc.
func addWorkdays(t time.Time, n int, loc *time.Location) time.Time {
	t = t.In(loc)
	for n > 0 {
		t = t.AddDate(0, 0, 1)
		if wd := t.Weekday(); wd != time.Saturday && wd != time.Sunday {
			n--
		}
	}
	return t
}

// workdaysSince is the most working days n with since moved n working days
// no later than now.
func workdaysSince(since, now time.Time, loc *time.Location) int {
	n := 0
	for !addWorkdays(since, n+1, loc).After(now) {
		n++
	}
	return n
}
