package slack

import (
	"context"
	"slices"
	"strings"

	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/store"
)

// names are the conversations among ids that conversations.info names
// because the store has no name for them; a DM is never asked, and each
// conversation is asked at most once per process.
func (s *Source) names(ctx context.Context, ids []string) ([]dslack.Conversation, error) {
	if s.client == nil || len(ids) == 0 {
		return nil, nil
	}
	ids = slices.Compact(slices.Sorted(slices.Values(ids)))
	rows, err := s.db.Conversations(ctx, ids)
	if err != nil {
		return nil, err
	}
	stored := make(map[string]store.ConversationRow, len(rows))
	for _, r := range rows {
		stored[r.ID] = r
	}
	team, _ := s.viewer()
	var out []dslack.Conversation
	for _, id := range ids {
		r := stored[id]
		if r.Name != "" || dslack.ConversationKind(r.Kind).DM() || strings.HasPrefix(id, "D") || !s.askName(id) {
			continue
		}
		info, err := s.client.ConversationInfo(ctx, id)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			s.logf("slack: naming conversation %s: %v", id, err)
			continue
		}
		c := conversationRow(team, info)
		if c.ID == "" {
			c.ID = id
		}
		out = append(out, dslack.Conversation{ID: c.ID, Name: c.Name, Kind: dslack.ConversationKind(c.Kind),
			IsMember: c.IsMember, Archived: c.Archived, DMUser: c.DMUser})
	}
	return out, nil
}

// askName says whether id is still to be asked, and marks it asked.
func (s *Source) askName(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.asked[id] {
		return false
	}
	if s.asked == nil {
		s.asked = map[string]bool{}
	}
	s.asked[id] = true
	return true
}

// nameConversations stores the names conversations.info gives the
// conversations among ids the store has none for.
func (s *Source) nameConversations(ctx context.Context, ids []string) error {
	named, err := s.names(ctx, ids)
	if err != nil || len(named) == 0 {
		return err
	}
	team, _ := s.viewer()
	rows := make([]store.ConversationRow, len(named))
	for i, c := range named {
		rows[i] = store.ConversationRow{ID: c.ID, Team: team, Name: c.Name, Kind: string(c.Kind), IsMember: c.IsMember,
			Archived: c.Archived, DMUser: c.DMUser}
	}
	return s.db.PutConversations(ctx, rows)
}
