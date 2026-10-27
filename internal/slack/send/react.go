package send

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Reaction is an emoji to add to, or remove from, the message of a permalink.
type Reaction struct {
	Permalink, Name string
	Remove          bool
}

// Reacted is where a reaction went; Unchanged says the message already
// had it, or, on a removal, did not.
type Reacted struct {
	Conversation, Channel, TS, Name string
	Unchanged                       bool
}

// React adds or removes r on its message, under the post allowlist.
func (s *Service) React(ctx context.Context, r Reaction) (Reacted, error) {
	link, ok := parsePermalink(strings.TrimSpace(r.Permalink))
	if !ok {
		return Reacted{}, fmt.Errorf("%w: a reaction needs a message permalink, not %q", ErrAmbiguous, r.Permalink)
	}
	name := strings.Trim(strings.TrimSpace(r.Name), ":")
	if name == "" {
		return Reacted{}, errors.New("a reaction needs an emoji name")
	}
	no := false
	res, err := s.Resolve(ctx, Request{Target: r.Permalink, Thread: &no})
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
	out := Reacted{Conversation: res.Conversation, Channel: res.Channel, TS: link.ts, Name: name}
	if err := call(ctx, res.Channel, link.ts, name); err != nil {
		if code(err) != unchanged {
			return Reacted{}, loggedOut(err)
		}
		out.Unchanged = true
	}
	if s.Log != nil {
		s.Log("slack_reacted channel=%s ts=%s remove=%t unchanged=%t", res.Channel, link.ts, r.Remove, out.Unchanged)
	}
	return out, nil
}
