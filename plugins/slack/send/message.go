package send

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"

	"github.com/wloophq/wloop/plugins/slack/slackapi"
)

// message is the message at ts in channel, in the thread threadTS when it
// is a reply; ok is false when Slack holds none, or only the tombstone a
// deleted thread parent leaves.
func (s *Service) message(ctx context.Context, channel, ts, threadTS string) (wireMessage, bool, error) {
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
		if c := code(err); c == "thread_not_found" || c == "message_not_found" {
			continue
		}
		if err != nil {
			return wireMessage{}, false, err
		}
		for _, raw := range page.Messages {
			var m wireMessage
			if json.Unmarshal(raw, &m) == nil && m.TS == ts {
				return m, m.Subtype != tombstone, nil
			}
		}
	}
	return wireMessage{}, false, nil
}

const tombstone = "tombstone"

// Posted is a message as Slack holds it, in the conversation Resolved names.
type Posted struct {
	Resolved
	TS, ThreadTS, User, Text string
}

// Read reads the message ref names, under the post allowlist; ok is false
// when Slack holds no such message.
func (s *Service) Read(ctx context.Context, ref string) (p Posted, ok bool, err error) {
	link, named := ParseMessage(ref)
	if !named {
		return Posted{}, false, fmt.Errorf("%w: %q is not a message ref or permalink", ErrAmbiguous, ref)
	}
	no := false
	res, err := s.Resolve(ctx, Request{Target: ref, Thread: &no})
	if err != nil {
		return Posted{}, false, err
	}
	if err := s.Allowed(res); err != nil {
		return Posted{}, false, err
	}
	link = s.Stored.InThread(ctx, link)
	m, ok, err := s.message(ctx, res.Channel, link.TS, link.ThreadTS)
	if err != nil || !ok {
		return Posted{}, false, loggedOut(err)
	}
	return Posted{Resolved: res, TS: m.TS, ThreadTS: cmp.Or(m.ThreadTS, link.ThreadTS), User: m.User, Text: m.Text}, true, nil
}
