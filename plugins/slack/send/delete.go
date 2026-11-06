package send

import (
	"cmp"
	"context"
	"fmt"
)

// Delete names a message User posted, by its ref or its permalink.
type Delete struct {
	Message, User string
}

// Deleted is the message a deletion removed and Deleted, its text.
type Deleted struct {
	Conversation, Channel, TS string
	// ThreadTS is the thread the message was in, "" when it was a top-level
	// message without replies.
	ThreadTS string
	// Permalink is the message's, "" when Slack gave none.
	Permalink string
	Deleted   string
}

// Delete removes d's message, under the post allowlist; it refuses a
// message another user posted.
func (s *Service) Delete(ctx context.Context, d Delete) (Deleted, error) {
	link, ok := ParseMessage(d.Message)
	switch {
	case !ok:
		return Deleted{}, fmt.Errorf("%w: a deletion needs a message ref or permalink, not %q", ErrAmbiguous, d.Message)
	case d.User == "":
		return Deleted{}, ErrNotLoggedIn
	}
	no := false
	res, err := s.Resolve(ctx, Request{Target: d.Message, Thread: &no})
	if err != nil {
		return Deleted{}, err
	}
	if err := s.Allowed(res); err != nil {
		return Deleted{}, err
	}
	link = s.Stored.InThread(ctx, link)
	m, err := s.message(ctx, res.Channel, link.TS, link.ThreadTS)
	if err != nil {
		return Deleted{}, loggedOut(err)
	}
	if m.User != d.User {
		return Deleted{}, fmt.Errorf("%w: %s ts %s is not the user's; a deletion removes only the user's own messages", ErrCannotPost, res.Channel, link.TS)
	}
	// Slack has no permalink for a message once it is gone.
	permalink := s.permalinkOf(ctx, d.Message, res.Channel, link.TS)
	if err := s.Client.DeleteMessage(ctx, res.Channel, link.TS); err != nil {
		return Deleted{}, loggedOut(err)
	}
	if s.Log != nil {
		s.Log("slack_deleted channel=%s ts=%s", res.Channel, link.TS)
	}
	return Deleted{Conversation: res.Conversation, Channel: res.Channel, TS: link.TS, ThreadTS: cmp.Or(m.ThreadTS, link.ThreadTS), Permalink: permalink,
		Deleted: m.Text}, nil
}
