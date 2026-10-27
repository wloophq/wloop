package slack

import (
	"context"
	"fmt"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
	slackapi "github.com/wloophq/wloop/internal/slack"
	"github.com/wloophq/wloop/internal/slack/send"
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
	if err := dslack.Codec().Valid(u.Key); err != nil {
		return unit.Unit{}, err
	}
	return u, nil
}

// StoredOver is the send.Stored over the messages the Slack module stores in
// db. A deleted message, or one search stored with no thread (its permalink
// may just not say it), is left to Slack; a store error is logged and left to
// Slack too.
func StoredOver(db *sqlite.DB, log func(string, ...any)) send.Stored {
	st := db.Slack()
	return func(ctx context.Context, conv, ts string) (send.StoredMessage, bool) {
		rows, err := st.Messages(ctx, conv, sqlite.MessageQuery{TS: ts})
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
