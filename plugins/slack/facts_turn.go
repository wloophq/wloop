package slack

import (
	"slices"
	"strings"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/items"
	slackrows "github.com/wloophq/wloop/plugins/slack/rows"
	"github.com/wloophq/wloop/plugins/slack/store"
)

// lastTurn says whose turn the unit's newest person's message leaves: byMe, the viewer wrote it; toMe,
// someone else wrote it to the viewer, mentioning them or in a one-to-one direct conversation; and
// reacted, the viewer's reaction is on the newest person's message someone else wrote.
func lastTurn(in *slackrows.Input) (byMe, toMe, reacted bool) {
	viewer := in.Viewer()
	if viewer == "" {
		return false, false, false
	}
	m, ok := newestHuman(in)
	if !ok {
		return false, false, false
	}
	byMe = m.User == viewer
	toMe = !byMe && (slices.Contains(m.Mentions, viewer) || conversationOf(in.Key(), in.Conversation().Kind) == "dm")
	msgs := in.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if in.Human(msgs[i]) && msgs[i].User != viewer {
			reacted = slices.ContainsFunc(msgs[i].Reactions, func(r store.ReactionRow) bool { return slices.Contains(r.Users, viewer) })
			break
		}
	}
	return byMe, toMe, reacted
}

// askedAt is when it was asked: its anchor's time, else when it was created.
func askedAt(it items.Item) time.Time {
	if !it.Origin.At.IsZero() {
		return it.Origin.At
	}
	return it.CreatedAt
}

// unansweredResolved counts in's resolved items after whose ask no person but its requester posted.
func unansweredResolved(in *slackrows.Input) int {
	n := 0
	for _, it := range in.Items() {
		if it.State != items.Resolved {
			continue
		}
		at := askedAt(it)
		if !slices.ContainsFunc(in.Messages(), func(m store.MessageRow) bool {
			return in.Human(m) && "slack:"+m.User != it.Requester.Key && dslack.TSTime(m.TS).After(at)
		}) {
			n++
		}
	}
	return n
}

// waitedMoves are what the people in's open items wait on, the viewer aside, did after an item's ask: posted,
// "<item id> <person key> <time>" for each one's newest post after it; reactions, "<item id> <person key> <time>
// <emoji>" for each emoji of theirs, skin tone dropped, on the ask or a later message, with that message's time
// for the newest such message. Times are RFC 3339; the host weighs them against a reopen or a nudge.
func waitedMoves(in *slackrows.Input) (posted, reactions []string) {
	for _, it := range in.Items() {
		if it.State != items.Open {
			continue
		}
		at := askedAt(it)
		for _, p := range it.WaitingOn {
			id, ok := strings.CutPrefix(p.Key, "slack:")
			if !ok || id == in.Viewer() {
				continue
			}
			var newest time.Time
			reacted := map[string]time.Time{}
			var emojis []string
			for _, m := range in.Messages() {
				t := dslack.TSTime(m.TS)
				if m.Deleted || t.Before(at) {
					continue
				}
				if m.User == id && t.After(at) && in.Human(m) {
					newest = later(newest, t)
				}
				for _, r := range m.Reactions {
					name, _, _ := strings.Cut(r.Name, "::")
					if !slices.Contains(r.Users, id) {
						continue
					}
					if _, seen := reacted[name]; !seen {
						emojis = append(emojis, name)
					}
					at := t
					if seen, ok := r.Seen[id]; ok && seen.After(at) {
						at = seen
					}
					reacted[name] = later(reacted[name], at)
				}
			}
			prefix := it.ID + " " + p.Key + " "
			if !newest.IsZero() {
				posted = append(posted, prefix+newest.UTC().Format(time.RFC3339Nano))
			}
			for _, e := range emojis {
				reactions = append(reactions, prefix+reacted[e].UTC().Format(time.RFC3339Nano)+" "+e)
			}
		}
	}
	return posted, reactions
}

// unmentionable are the keys of the people in's items name that a message cannot mention, each once: a bot,
// as the user cache knows it, a team named by text rather than a user group, and a key of no Slack person.
func unmentionable(in *slackrows.Input) []string {
	var out []string
	for _, it := range in.Items() {
		for _, p := range slices.Concat([]items.Person{it.Requester}, it.Assignees, it.WaitingOn) {
			if p.Key == "" || slices.Contains(out, p.Key) {
				continue
			}
			id, user := strings.CutPrefix(p.Key, "slack:")
			name, group := strings.CutPrefix(p.Key, "group:")
			if dslack.ValidPersonKey(p.Key) != nil || user && in.Bot(id) || group && !dslack.IsGroupID(name) {
				out = append(out, p.Key)
			}
		}
	}
	return out
}

// extracted says whether in's latest successful extraction pass read every message a person wrote on it as it
// reads now: none is newer than the pass's last message (a DM segment's pass records none, so newer than the
// pass), and none was edited after the pass. A failed or skipped pass writes no state, so it covers nothing.
func extracted(in *slackrows.Input) bool {
	st, ok := in.State()
	if !ok || st.At.IsZero() {
		return false
	}
	return !slices.ContainsFunc(in.Messages(), func(m store.MessageRow) bool {
		if !in.Human(m) {
			return false
		}
		at := dslack.TSTime(m.TS)
		newer := at.After(st.At)
		if st.LastTS != "" {
			newer = at.After(dslack.TSTime(st.LastTS))
		}
		return newer || m.EditedTS != "" && dslack.TSTime(m.EditedTS).After(st.At)
	})
}

// turnFacts are in's facts of who spoke last, what the people its items wait on did since and which of its
// people a message cannot mention.
func turnFacts(in *slackrows.Input) []src.Fact {
	byMe, toMe, reacted := lastTurn(in)
	posted, reactions := waitedMoves(in)
	return []src.Fact{
		{Name: "slack.extracted", Value: src.Bool(extracted(in))},
		{Name: "slack.last_by_me", Value: src.Bool(byMe)},
		{Name: "slack.last_to_me", Value: src.Bool(toMe)},
		{Name: "slack.reacted_to_last", Value: src.Bool(reacted)},
		{Name: "slack.items_resolved_unanswered", Value: src.Int(int64(unansweredResolved(in)))},
		{Name: "slack.waited_posted", Value: src.List(posted...)},
		{Name: "slack.waited_reactions", Value: src.List(reactions...)},
		{Name: "slack.unmentionable", Value: src.List(unmentionable(in)...)},
	}
}
