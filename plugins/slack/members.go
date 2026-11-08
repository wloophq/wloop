package slack

import (
	"context"
	"fmt"
	"slices"

	"time"

	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/items"
	slackrows "github.com/wloophq/wloop/plugins/slack/rows"
	"github.com/wloophq/wloop/plugins/slack/store"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

const (
	// membersTTL is how long a conversation's members read by conversations.members stand.
	membersTTL = 24 * time.Hour
	// membersPerRun caps the conversations whose members one ingest job reads.
	membersPerRun = 10
	// membersPages caps the pages of one conversation's members, 1000 a page; a larger one stays unknown.
	membersPages = 10
)

// unitMembers are the person keys of the members of in's conversation and whether they are known: a DM's
// viewer and its peer, a group DM's or a channel's as conversations.members last read them.
func unitMembers(in *slackrows.Input) ([]string, bool) {
	keyOf := func(user string) string { return string(dslack.Provider) + ":" + user }
	conv := in.Conversation()
	if conversationOf(in.Key(), conv.Kind) == "dm" {
		if in.Viewer() == "" || conv.DMUser == "" {
			return nil, false
		}
		return []string{keyOf(in.Viewer()), keyOf(conv.DMUser)}, true
	}
	m, ok := in.Members()
	if !ok || m.Failed {
		return nil, false
	}
	out := make([]string, len(m.Members))
	for i, u := range m.Members {
		out[i] = keyOf(u)
	}
	return out, true
}

// refreshMembers reads the members of the group DMs and channels whose units hold an open item waiting on
// someone, those never read or read more than membersTTL ago, the least recently read first, at most
// membersPerRun of them, and marks their units to be reported again. A read that fails is logged and stored as
// failed, so its conversation waits membersTTL like a read one and stays unknown.
func (p *plugin) refreshMembers(ctx context.Context) error {
	if p.slack.Client == nil {
		return nil
	}
	open, err := p.host.Open(ctx, nil)
	if err != nil {
		return err
	}
	units := map[string][]unit.Unit{}
	keys := map[string]dslack.Key{}
	for u, its := range open {
		k, err := dslack.ParseUnit(u)
		if err != nil || !slices.ContainsFunc(its, func(it items.Item) bool { return len(it.WaitingOn) > 0 }) {
			continue
		}
		units[k.Conversation] = append(units[k.Conversation], u)
		keys[k.Conversation] = k
	}
	ids := make([]string, 0, len(units))
	for id := range units {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	db := p.db.Slack()
	convs, err := db.Conversations(ctx, ids)
	if err != nil {
		return err
	}
	kinds := map[string]string{}
	for _, c := range convs {
		kinds[c.ID] = c.Kind
	}
	stored, err := db.Members(ctx, ids)
	if err != nil {
		return err
	}
	slices.SortStableFunc(ids, func(a, b string) int { return stored[a].Fetched.Compare(stored[b].Fetched) })
	now, read := p.now(), 0
	for _, id := range ids {
		if read == membersPerRun {
			break
		}
		if conversationOf(keys[id], kinds[id]) == "dm" {
			continue
		}
		if m, ok := stored[id]; ok && now.Sub(m.Fetched) < membersTTL {
			continue
		}
		read++
		row := store.MembersRow{Conversation: id, Fetched: now}
		if row.Members, err = p.conversationMembers(ctx, id); err != nil {
			p.logf("slack: reading who is in %s: %v", id, err)
			row.Failed = true
		}
		if err := db.PutMembers(ctx, row); err != nil {
			return err
		}
		if !row.Failed {
			p.reports.mark(units[id]...)
		}
	}
	return nil
}

func (p *plugin) conversationMembers(ctx context.Context, id string) ([]string, error) {
	var out []string
	cursor := ""
	for range membersPages {
		page, err := p.slack.Client.ConversationMembers(ctx, id, cursor)
		if err != nil {
			return nil, err
		}
		out = append(out, page.Members...)
		if cursor = page.NextCursor; cursor == "" {
			return out, nil
		}
	}
	return nil, fmt.Errorf("more than %d pages of members", membersPages)
}
