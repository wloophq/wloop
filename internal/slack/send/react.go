package send

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Reaction is an emoji to add to, or remove from, a message, named by its
// ref or its permalink.
type Reaction struct {
	Message, Name string
	Remove        bool
}

// Reacted is where a reaction went; Unchanged says the message already
// had it, or, on a removal, did not.
type Reacted struct {
	Conversation, Channel, TS, Name string
	// Permalink is the message's, "" when Slack gives none.
	Permalink string
	Unchanged bool
}

// React adds or removes r on its message, under the post allowlist.
func (s *Service) React(ctx context.Context, r Reaction) (Reacted, error) {
	m, ok := ParseMessage(r.Message)
	if !ok {
		return Reacted{}, fmt.Errorf("%w: a reaction needs a message ref or permalink, not %q", ErrAmbiguous, r.Message)
	}
	name := strings.Trim(strings.TrimSpace(r.Name), ":")
	if name == "" {
		return Reacted{}, errors.New("a reaction needs an emoji name")
	}
	no := false
	res, err := s.Resolve(ctx, Request{Target: r.Message, Thread: &no})
	if err != nil {
		return Reacted{}, err
	}
	if err := s.Allowed(res); err != nil {
		return Reacted{}, err
	}
	call, unchanged := s.Client.AddReaction, "already_reacted"
	if r.Remove {
		call, unchanged = s.Client.RemoveReaction, "no_reaction"
	}
	out := Reacted{Conversation: res.Conversation, Channel: res.Channel, TS: m.TS, Name: name}
	if err := call(ctx, res.Channel, m.TS, name); err != nil {
		if code(err) != unchanged {
			return Reacted{}, loggedOut(err)
		}
		out.Unchanged = true
	}
	if s.Log != nil {
		s.Log("slack_reacted channel=%s ts=%s remove=%t unchanged=%t", res.Channel, m.TS, r.Remove, out.Unchanged)
	}
	out.Permalink = s.permalinkOf(ctx, r.Message, res.Channel, m.TS)
	return out, nil
}

// permalinkOf is the permalink of the message at ts in channel: named, when
// it is one, else the stored one or Slack's.
func (s *Service) permalinkOf(ctx context.Context, named, channel, ts string) string {
	if IsPermalink(named) {
		return strings.TrimSpace(named)
	}
	return s.Permalink(ctx, channel, ts)
}
