package cli

import (
	"cmp"
	"slices"
	"strings"

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
// replaces), wait on, me left out, then each who asks me something left out with the items only they owe,
// unless everyone does.
func (s draftSubject) unitOwed(me task.Me, absorbed []string) ([]items.Person, []items.Item) {
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
			if !isMe(p) && !slices.ContainsFunc(people, func(q items.Person) bool { return q.Key == p.Key }) {
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

// draftFactsOf are the draft slot's facts of a draft on sub to people over the items ids: whether any of
// people is still waited on, the draft's items still held by any of them (unowned ones for no people), and
// whether every one of them asks you.
func draftFactsOf(sub draftSubject, people []string, ids []string, me task.Me) workflow.Facts {
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
		"draft.person_waited":   waited,
		"draft.items_owed":      held + len(owed),
		"draft.items_assigned":  owned,
		"draft.unit_open":       sub.task == nil || sub.task.Open(),
		"draft.person_asks_you": asks,
	}
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
