package cli

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// people are the persons a per-person step proposes to: those the open
// items of the subject wait on, a task's those it holds; one empty person
// for a per-subject step.
func (s draftSubject) people(per string) []items.Person {
	if per != "person" {
		return []items.Person{{}}
	}
	var out []items.Person
	its := s.items
	if s.task != nil {
		its = s.held()
	}
	for _, it := range its {
		for _, p := range it.WaitingOn {
			if it.State == items.Open && !slices.ContainsFunc(out, func(q items.Person) bool { return q.Key == p.Key }) {
				out = append(out, p)
			}
		}
	}
	return out
}

// owedPool are the open items a draft to person may list as owed: those
// the subject holds, and of a task's unit those waiting on person that
// other tasks hold, so one nudge covers what person owes there.
func (s draftSubject) owedPool(person items.Person) []items.Item {
	out := s.held()
	if s.task == nil || person.Key == "" {
		return out
	}
	for _, it := range s.items {
		if it.State == items.Open && it.Unit == s.unit && !slices.ContainsFunc(out, func(h items.Item) bool { return h.ID == it.ID }) &&
			slices.ContainsFunc(it.WaitingOn, func(w items.Person) bool { return w.Key == person.Key }) {
			out = append(out, it)
		}
	}
	return out
}

// asks says whether person requested an open item of the subject's unit that waits on me, or that I am
// assigned and waits on nobody else.
func (s draftSubject) asks(person string, me task.Me) bool {
	if person == "" {
		return false
	}
	them := items.Identity{Keys: []string{person}, Codecs: me.Codecs}
	isMe := func(p items.Person) bool { return p.Is(me) }
	return slices.ContainsFunc(s.items, func(it items.Item) bool {
		if it.State != items.Open || it.Unit != s.unit || !it.Requester.Is(them) {
			return false
		}
		return slices.ContainsFunc(it.WaitingOn, isMe) ||
			slices.ContainsFunc(it.Assignees, isMe) && !slices.ContainsFunc(it.WaitingOn, func(p items.Person) bool { return !isMe(p) })
	})
}

// held are the open items of the subject: those its task holds, or every
// open item of its unit.
func (s draftSubject) held() []items.Item {
	var out []items.Item
	for _, it := range s.items {
		if it.State == items.Open && it.Unit == s.unit && (s.task == nil || slices.Contains(s.task.Items, task.ItemRef{Unit: it.Unit, Item: it.ID})) {
			out = append(out, it)
		}
	}
	return out
}

// unitOwed are, for one draft on the subject's conversation, the people it is to and the items it lists:
// those the subject's open items, and the open items of absorbed (the items of a draft the new one
// replaces), wait on, me and those keep refuses left out, then each who asks me something left out with the
// items only they owe, unless everyone does.
func (s draftSubject) unitOwed(me task.Me, absorbed []string, keep func(items.Person) bool) ([]items.Person, []items.Item) {
	isMe := func(p items.Person) bool { return p.Is(me) }
	pool := s.held()
	for _, it := range s.items {
		if it.State == items.Open && it.Unit == s.unit && slices.Contains(absorbed, it.ID) &&
			!slices.ContainsFunc(pool, func(h items.Item) bool { return h.ID == it.ID }) {
			pool = append(pool, it)
		}
	}
	slices.SortStableFunc(pool, func(a, b items.Item) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), strings.Compare(a.ID, b.ID))
	})
	var people []items.Person
	for _, it := range pool {
		for _, p := range it.WaitingOn {
			if !isMe(p) && !slices.ContainsFunc(people, func(q items.Person) bool { return q.Key == p.Key }) && keep(p) {
				people = append(people, p)
			}
		}
	}
	if asked := slices.DeleteFunc(slices.Clone(people), func(p items.Person) bool { return s.asks(p.Key, me) }); len(asked) > 0 {
		people = asked
	}
	its := slices.DeleteFunc(pool, func(it items.Item) bool {
		return !slices.ContainsFunc(it.WaitingOn, func(w items.Person) bool {
			return slices.ContainsFunc(people, func(p items.Person) bool { return p.Key == w.Key })
		})
	})
	return people, its
}

// draftFactsOf are the draft slot's facts at now of a draft on sub to people over the items ids, posting at
// target, whose unit's plugin tells rd: whether any of people is still waited on, the draft's items still held
// by any of them (unowned ones for no people), whether every one of them asks you, how many cannot read where
// it posts, whether the person it was written to in their own conversation now reads the one it came from,
// when they last moved on every one of its items, how many a message cannot mention, and how old its items are.
func draftFactsOf(sub draftSubject, people []string, ids []string, me task.Me, rd readers, target string, codecs unit.KeyComparer, now time.Time,
	ignored []string) workflow.Facts {
	var mine []items.Item
	for _, it := range sub.items {
		if slices.Contains(ids, it.ID) {
			mine = append(mine, it)
		}
	}
	owned, held := 0, 0
	for _, it := range mine {
		if len(it.Assignees)+len(it.WaitingOn) > 0 || it.State != items.Open {
			owned++
		}
		if len(people) == 0 && unownedItem(it, sub.unit) {
			held++
		}
	}
	waited, asks := len(people) == 0, len(people) > 0
	owed := map[string]bool{}
	for _, person := range people {
		waited = waited || sub.waitsOn(person, mine)
		asks = asks && sub.asks(person, me)
		heldNow := sub.owedPool(items.Person{Key: person})
		for _, it := range mine {
			if slices.ContainsFunc(heldNow, func(h items.Item) bool { return h.ID == it.ID }) {
				owed[it.ID] = true
			}
		}
	}
	return workflow.Facts{
		"draft.person_waited":        waited,
		"draft.items_owed":           held + len(owed),
		"draft.items_assigned":       owned,
		"draft.unit_open":            sub.task == nil || sub.task.Open(),
		"draft.person_asks_you":      asks,
		"draft.people_outside":       rd.outside(people, target, codecs),
		"draft.target_joined":        rd.joined(people, target, codecs),
		"draft.people_posted_at":     movedAt(rd.posted, mine, people, sub.lastNudge(), codecs, nil),
		"draft.people_reacted_at":    movedAt(rd.reactions, mine, people, sub.lastNudge(), codecs, ignored),
		"draft.people_unmentionable": named(people, rd.unmentionable, codecs),
		"draft.items_age_days":       itemsAgeDays(mine, now),
	}
}

// lastNudge is when the subject's task was last nudged; zero for none.
func (s draftSubject) lastNudge() time.Time {
	if s.task == nil {
		return time.Time{}
	}
	return s.task.Prop(task.PropLastNudge).Time
}

// movedAt reads moves, "<item id> <person key> <time>[ <emoji>]" as a plugin's posted_fact or reactions_fact
// holds them: for each of its, the newest move by one of people since the latest of its ask, its reopening and
// nudged, an emoji among ignored left out; then the oldest of those, so every item moved since; zero when one
// did not.
func movedAt(moves []string, its []items.Item, people []string, nudged time.Time, codecs unit.KeyComparer, ignored []string) time.Time {
	var oldest time.Time
	for _, it := range its {
		since := slices.MaxFunc([]time.Time{itemAskedAt(it), it.ReopenedAt, nudged}, time.Time.Compare)
		var newest time.Time
		for _, m := range moves {
			f := strings.Fields(m)
			if len(f) < 3 || f[0] != it.ID || named(people, f[1:2], codecs) == 0 || len(f) > 3 && slices.Contains(ignored, f[3]) {
				continue
			}
			if at, err := time.Parse(time.RFC3339Nano, f[2]); err == nil && !at.Before(since) && at.After(newest) {
				newest = at
			}
		}
		if newest.IsZero() {
			return time.Time{}
		}
		if oldest.IsZero() || newest.Before(oldest) {
			oldest = newest
		}
	}
	return oldest
}

// itemAskedAt is when it was asked: its anchor's time, else when it was created.
func itemAskedAt(it items.Item) time.Time {
	if !it.Origin.At.IsZero() {
		return it.Origin.At
	}
	return it.CreatedAt
}

// itemsAgeDays are the working days, in now's location, since the newest of its was asked or last reopened;
// 0 for none.
func itemsAgeDays(its []items.Item, now time.Time) int {
	var newest time.Time
	for _, it := range its {
		at := itemAskedAt(it)
		if it.ReopenedAt.After(at) {
			at = it.ReopenedAt
		}
		if at.After(newest) {
			newest = at
		}
	}
	if newest.IsZero() {
		return 0
	}
	return workdaysBetween(newest, now)
}

// workdaysBetween is the most working days n, Monday to Friday, with from moved n working days at the same
// wall-clock time no later than to, in to's location, counted in constant time.
func workdaysBetween(from, to time.Time) int {
	from = from.In(to.Location())
	if !from.Before(to) {
		return 0
	}
	date := func(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC) }
	days := int(date(to).Sub(date(from)).Hours() / 24)
	clock := func(t time.Time) time.Duration {
		h, m, sec := t.Clock()
		return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(sec)*time.Second + time.Duration(t.Nanosecond())
	}
	if clock(to) < clock(from) {
		days--
	}
	n := days / 7 * 5
	wd := from.Weekday()
	for range days % 7 {
		if wd = (wd + 1) % 7; wd != time.Saturday && wd != time.Sunday {
			n++
		}
	}
	return n
}

// waitsOn says whether person is still waited on: by an item a task's draft may list as owed, or by an open
// item of mine.
func (s draftSubject) waitsOn(person string, mine []items.Item) bool {
	its := mine
	if s.task != nil {
		its = s.owedPool(items.Person{Key: person})
	}
	return slices.ContainsFunc(its, func(it items.Item) bool {
		return it.State == items.Open && slices.ContainsFunc(it.WaitingOn, func(p items.Person) bool { return p.Key == person })
	})
}

// draftPeople are the people stored draft d on subject is to: the person a per-person key names, else
// those its proposed text mentions, in order.
func draftPeople(d draft.Draft, subject unit.Unit) []string {
	if p := draftPerson(d.Origin.Key, d.Unit, subject); p != "" {
		return []string{p}
	}
	var out []string
	for _, m := range mentionToken.FindAllStringSubmatch(d.Original, -1) {
		if !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	return out
}

// draftPerson is the person a per-person draft's key names after its unit, or, as drafts proposed before the
// key named the unit, after its subject; "" for a draft of no person.
func draftPerson(key string, u, subject unit.Unit) string {
	for _, prefix := range []string{u.String() + ":", subject.Key + ":"} {
		if p, ok := strings.CutPrefix(key, prefix); ok {
			return p
		}
	}
	return ""
}
