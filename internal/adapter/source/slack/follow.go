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

// IsPermalink says whether s is a Slack message permalink.
func IsPermalink(s string) bool { return send.IsPermalink(s) }

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

// StoredMessages finds one stored message; the module's Store is one.
type StoredMessages interface {
	Message(ctx context.Context, conv, ts string) (dslack.Message, bool, error)
}

// storedThread is the anchor of the thread the store holds conv's message ts
// in; "" when stored is nil or holds no such message.
func storedThread(ctx context.Context, stored StoredMessages, conv, ts string) (string, error) {
	if stored == nil {
		return "", nil
	}
	m, ok, err := stored.Message(ctx, conv, ts)
	if err != nil || !ok {
		return "", err
	}
	return m.Anchor(), nil
}

// StoredOver is the send.Stored over the messages the Slack module stores in db.
func StoredOver(db *sqlite.DB) send.Stored {
	st := &Store{DB: db.Slack()}
	return func(ctx context.Context, conv, ts string) (send.StoredMessage, bool, error) {
		m, ok, err := st.Message(ctx, conv, ts)
		return send.StoredMessage{TS: m.TS, ThreadTS: m.ThreadTS, Text: m.Text}, ok, err
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
		if threadTS, err = storedThread(ctx, l.Messages, conv, ts); err != nil {
			return unit.Unit{}, err
		}
	}
	if u, ok, err := l.UnitOf(ctx, team, conv, ts, threadTS); err != nil || ok {
		return u, err
	}
	return followUnit(ctx, c, team, conv, ts, threadTS)
}
