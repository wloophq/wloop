package slack

import (
	"context"
	"fmt"

	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/send"
	slackapi "github.com/wloophq/wloop/plugins/slack/slackapi"
	"github.com/wloophq/wloop/plugins/slack/store"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

// FollowUnit is the thread unit a message ref or permalink names in team:
// the parent's thread when it names a reply. A name that does not say the
// thread (a ref, a permalink without thread_ts) costs one
// conversations.replies call to find the parent.
func FollowUnit(ctx context.Context, c *slackapi.Client, team, message string) (unit.Unit, error) {
	conv, ts, threadTS, err := parseMessage(message)
	if err != nil {
		return unit.Unit{}, err
	}
	return followUnit(ctx, c, team, conv, ts, threadTS)
}

func followUnit(ctx context.Context, c *slackapi.Client, team, conv, ts, threadTS string) (unit.Unit, error) {
	if threadTS == "" {
		page, err := c.Replies(ctx, conv, ts, slackapi.PageArgs{Limit: 1})
		if err != nil {
			return unit.Unit{}, err
		}
		if len(page.Messages) == 0 {
			return unit.Unit{}, fmt.Errorf("slack: no message %s in %s", ts, conv)
		}
		m, err := dslack.MessageFrom(page.Messages[0])
		if err != nil {
			return unit.Unit{}, err
		}
		threadTS = m.Anchor()
	}
	u := dslack.ThreadUnit(team, conv, threadTS)
	if err := dslack.ValidKey(u.Key); err != nil {
		return unit.Unit{}, err
	}
	return u, nil
}

// StoredOver is the send.Stored over the messages the Slack module stores in
// db. A deleted message, or one search stored with no thread (its permalink
// may just not say it), is left to Slack; a store error is logged and left to
// Slack too.
func StoredOver(db *store.DB, log func(string, ...any)) send.Stored {
	st := db.Slack()
	return func(ctx context.Context, conv, ts string) (send.StoredMessage, bool) {
		rows, err := st.Messages(ctx, conv, store.MessageQuery{TS: ts})
		if err != nil {
			if log != nil {
				log("slack_stored conversation=%s ts=%s error=%v", conv, ts, err)
			}
			return send.StoredMessage{}, false
		}
		if len(rows) == 0 || rows[0].Deleted || (rows[0].ThreadTS == "" && rows[0].Via == string(dslack.ViaSearch)) {
			return send.StoredMessage{}, false
		}
		r := rows[0]
		return send.StoredMessage{TS: r.TS, ThreadTS: r.ThreadTS, Text: r.Text, Permalink: r.Permalink}, true
	}
}

// KnownOver finds a conversation as db holds it: a channel with its name and membership, a DM with the other
// party's handle; one db does not hold, or a channel it holds without a name, is left to Slack.
func KnownOver(db *store.DB, log func(string, ...any)) send.Known {
	st := db.Slack()
	return func(ctx context.Context, id string) (send.KnownConversation, bool) {
		rows, err := st.Conversations(ctx, []string{id})
		if err != nil {
			if log != nil {
				log("slack_known conversation=%s error=%v", id, err)
			}
			return send.KnownConversation{}, false
		}
		if len(rows) == 0 {
			return send.KnownConversation{}, false
		}
		r := rows[0]
		c := send.KnownConversation{ID: r.ID, Name: r.Name, Kind: r.Kind, DMUser: r.DMUser, IsMember: r.IsMember, Archived: r.Archived}
		switch dslack.ConversationKind(r.Kind) {
		case dslack.KindIM:
			if r.DMUser == "" {
				return send.KnownConversation{}, false
			}
			if us, err := st.Users(ctx, []string{r.DMUser}); err == nil && len(us) == 1 {
				c.DMUserName = us[0].Name
			}
		case dslack.KindMPIM:
		default:
			if r.Name == "" {
				return send.KnownConversation{}, false
			}
		}
		return c, true
	}
}

// parseMessage reads s as a message ref or permalink.
func parseMessage(s string) (conv, ts, threadTS string, err error) {
	m, ok := send.ParseMessage(s)
	if !ok {
		return "", "", "", fmt.Errorf("not a Slack message ref (<conversation>/<ts>) or permalink: %s", s)
	}
	return m.Conversation, m.TS, m.ThreadTS, nil
}

// MessageUnit is the unit holding the message a ref or permalink names in
// team: the tracked DM segment or thread it lands in, else its thread, which
// the store says when it holds the message and Slack otherwise.
func (l Landing) MessageUnit(ctx context.Context, c *slackapi.Client, team, message string) (unit.Unit, error) {
	conv, ts, threadTS, err := parseMessage(message)
	if err != nil {
		return unit.Unit{}, err
	}
	if threadTS == "" {
		threadTS = l.Messages.InThread(ctx, send.Message{Conversation: conv, TS: ts}).ThreadTS
	}
	if u, ok, err := l.UnitOf(ctx, team, conv, ts, threadTS); err != nil || ok {
		return u, err
	}
	return followUnit(ctx, c, team, conv, ts, threadTS)
}
