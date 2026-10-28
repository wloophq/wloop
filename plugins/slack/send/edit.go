package send

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/wloophq/wloop/plugins/slack/slackapi"
)

// Edit is new text, in Markdown, for a message User posted, named by its
// ref or its permalink.
type Edit struct {
	Message, Text, User string
}

// Edited is where an edit went: Text as posted, in mrkdwn, and Replaced,
// the text it replaced.
type Edited struct {
	Conversation, Channel, TS string
	// Permalink is the message's, "" when Slack gives none.
	Permalink      string
	Text, Replaced string
}

// Edit replaces the text of e's message, under the post allowlist, with its
// mentions resolved as Send resolves them; it refuses a message another
// user posted.
func (s *Service) Edit(ctx context.Context, e Edit) (Edited, error) {
	link, ok := ParseMessage(e.Message)
	switch {
	case !ok:
		return Edited{}, fmt.Errorf("%w: an edit needs a message ref or permalink, not %q", ErrAmbiguous, e.Message)
	case strings.TrimSpace(e.Text) == "":
		return Edited{}, errors.New("the message is empty")
	case e.User == "":
		return Edited{}, ErrNotLoggedIn
	}
	no := false
	res, err := s.Resolve(ctx, Request{Target: e.Message, Thread: &no})
	if err != nil {
		return Edited{}, err
	}
	if err := s.Allowed(res); err != nil {
		return Edited{}, err
	}
	link = s.Stored.InThread(ctx, link)
	m, err := s.message(ctx, res.Channel, link.TS, link.ThreadTS)
	if err != nil {
		return Edited{}, loggedOut(err)
	}
	if m.User != e.User {
		return Edited{}, fmt.Errorf("%w: %s ts %s is not the user's; an edit replaces only the user's own messages", ErrCannotPost, res.Channel, link.TS)
	}
	markdown, err := s.Mentions(ctx, e.Text, func(user string) string { return "<@" + user + ">" })
	if err != nil {
		return Edited{}, err
	}
	text := Mrkdwn(markdown)
	if err := s.Client.UpdateMessage(ctx, res.Channel, link.TS, text); err != nil {
		return Edited{}, loggedOut(err)
	}
	if s.Log != nil {
		s.Log("slack_edited channel=%s ts=%s", res.Channel, link.TS)
	}
	return Edited{Conversation: res.Conversation, Channel: res.Channel, TS: link.TS, Permalink: s.permalinkOf(ctx, e.Message, res.Channel, link.TS),
		Text: text, Replaced: m.Text}, nil
}

// message is the message at ts in channel, in the thread threadTS when it
// is a reply.
func (s *Service) message(ctx context.Context, channel, ts, threadTS string) (wireMessage, error) {
	args := slack.PageArgs{Oldest: ts, Latest: ts, Inclusive: true, Limit: 2}
	reads := []func() (slack.MessagesPage, error){
		func() (slack.MessagesPage, error) { return s.Client.History(ctx, channel, args) },
		func() (slack.MessagesPage, error) { return s.Client.Replies(ctx, channel, ts, args) },
	}
	if threadTS != "" && threadTS != ts {
		reads = []func() (slack.MessagesPage, error){func() (slack.MessagesPage, error) { return s.Client.Replies(ctx, channel, threadTS, args) }}
	}
	for _, read := range reads {
		page, err := read()
		if err != nil {
			return wireMessage{}, err
		}
		for _, raw := range page.Messages {
			var m wireMessage
			if json.Unmarshal(raw, &m) == nil && m.TS == ts {
				return m, nil
			}
		}
	}
	return wireMessage{}, fmt.Errorf("%w: no message %s in %s", ErrCannotPost, ts, channel)
}
