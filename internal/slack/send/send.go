// Package send posts a message to Slack as the user.
package send

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/slack"
	"github.com/wloophq/wloop/internal/slack/auth"
)

type Request struct {
	Target    string // permalink, #name, channel id, @handle, email or user id
	Text      string // Markdown
	Thread    *bool  // nil: true for a permalink, false otherwise
	Broadcast bool
	Source    string // "session", "cli" or "draft"; logged
}

type Resolved struct {
	Channel, ThreadTS string // ThreadTS "" posts to the conversation
	Conversation      string // "#name", "@handle" or "group DM"
	Parent            string // first line of the thread parent, "" when not a reply
	DMUser            string // the other party when Channel is an im
}

type Sent struct {
	Conversation string
	Parent       string
	ThreadTS     string // the thread replied in, "" when not a reply
	Text         string // as posted, mrkdwn
	Permalink    string
	Channel, TS  string
}

type Service struct {
	Client *slack.Client
	Log    func(format string, args ...any)
	// PostAllowlist, when not empty, is the only set of conversations Send may post to:
	// channel ids, or user ids naming the DM with that user. Config slack.post_allowlist.
	PostAllowlist []string

	dir directory
}

// These are wrapped with the detail.
var (
	ErrNotLoggedIn = errors.New("not logged in to Slack")
	ErrAmbiguous   = errors.New("the target is ambiguous")
	ErrCannotPost  = errors.New("cannot post to the target")
	ErrNotAllowed  = errors.New("the target is not in the post allowlist")
)

// Resolve finds the conversation and thread of r without posting.
func (s *Service) Resolve(ctx context.Context, r Request) (Resolved, error) {
	if s.Client == nil {
		return Resolved{}, ErrNotLoggedIn
	}
	res, err := s.resolve(ctx, r)
	return res, loggedOut(err)
}

func (s *Service) resolve(ctx context.Context, r Request) (Resolved, error) {
	target := strings.TrimSpace(r.Target)
	if link, ok := parsePermalink(target); ok {
		return s.permalink(ctx, link, r.Thread == nil || *r.Thread)
	}
	if r.Thread != nil && *r.Thread {
		return Resolved{}, fmt.Errorf("a thread reply needs a permalink target, got %q", target)
	}
	switch {
	case strings.HasPrefix(target, "#"):
		return s.channelByName(ctx, strings.TrimPrefix(target, "#"))
	case strings.HasPrefix(target, "@"):
		if h := strings.TrimPrefix(target, "@"); isUserID(h) {
			return s.dmWithID(ctx, h)
		}
		return s.dmByHandle(ctx, strings.TrimPrefix(target, "@"))
	case strings.Contains(target, "@"):
		return s.dmByEmail(ctx, target)
	case isUserID(target):
		return s.dmWithID(ctx, target)
	case isConversationID(target):
		return s.conversation(ctx, target)
	}
	return Resolved{}, fmt.Errorf("%w: %q is not a permalink, #channel, channel id, @handle, email or user id", ErrAmbiguous, target)
}

// Allowed is nil when PostAllowlist is empty or holds res.Channel or res.DMUser.
func (s *Service) Allowed(res Resolved) error {
	if len(s.PostAllowlist) == 0 || slices.Contains(s.PostAllowlist, res.Channel) ||
		(res.DMUser != "" && slices.Contains(s.PostAllowlist, res.DMUser)) {
		return nil
	}
	return fmt.Errorf("%w: conversation %s (%s)", ErrNotAllowed, res.Channel, res.Conversation)
}

// Send resolves r and the mentions of its text, checks the allowlist and posts. Once chat.postMessage succeeded it never fails:
// a permalink it cannot read is left empty, so a caller never retries a message that went out.
func (s *Service) Send(ctx context.Context, r Request) (Sent, error) {
	if strings.TrimSpace(r.Text) == "" {
		return Sent{}, errors.New("the message is empty")
	}
	if s.Client == nil {
		return Sent{}, ErrNotLoggedIn
	}
	markdown, err := s.Mentions(ctx, r.Text, func(user string) string { return "<@" + user + ">" })
	if err != nil {
		return Sent{}, err
	}
	text := Mrkdwn(markdown)
	res, err := s.Resolve(ctx, r)
	if err != nil {
		return Sent{}, err
	}
	if err := s.Allowed(res); err != nil {
		return Sent{}, err
	}
	posted, err := s.Client.PostMessage(ctx, slack.PostArgs{
		Channel: res.Channel, Text: text, ThreadTS: res.ThreadTS,
		Broadcast: r.Broadcast && res.ThreadTS != "",
	})
	if err != nil {
		return Sent{}, loggedOut(err)
	}
	channel := posted.Channel
	if channel == "" {
		channel = res.Channel
	}
	if s.Log != nil {
		s.Log("slack_sent source=%s channel=%s ts=%s", r.Source, channel, posted.TS)
	}
	sent := Sent{Conversation: res.Conversation, Parent: res.Parent, ThreadTS: res.ThreadTS, Text: text, Channel: channel, TS: posted.TS}
	if link, err := s.Client.Permalink(ctx, channel, posted.TS); err == nil {
		sent.Permalink = link
	} else if s.Log != nil {
		s.Log("slack_sent_permalink channel=%s ts=%s error=%v", channel, posted.TS, err)
	}
	return sent, nil
}

func (s *Service) permalink(ctx context.Context, link permalink, thread bool) (Resolved, error) {
	res, err := s.conversation(ctx, link.channel)
	if err != nil || !thread {
		return res, err
	}
	at := link.ts
	if link.threadTS != "" {
		at = link.threadTS
	}
	msg, err := s.firstMessage(ctx, link.channel, at)
	if err != nil {
		return Resolved{}, err
	}
	res.ThreadTS = at
	if msg.ThreadTS != "" {
		res.ThreadTS = msg.ThreadTS
	}
	if msg.TS != res.ThreadTS {
		if msg, err = s.firstMessage(ctx, link.channel, res.ThreadTS); err != nil {
			return Resolved{}, err
		}
	}
	res.Parent = firstLine(msg.Text)
	return res, nil
}

type wireMessage struct {
	TS       string `json:"ts"`
	ThreadTS string `json:"thread_ts"`
	Text     string `json:"text"`
	User     string `json:"user"`
}

// firstMessage reads conversations.replies ts=<ts> limit=1: the message at ts, or the parent of its thread.
func (s *Service) firstMessage(ctx context.Context, channel, ts string) (wireMessage, error) {
	page, err := s.Client.Replies(ctx, channel, ts, slack.PageArgs{Limit: 1})
	if err != nil {
		if c := code(err); c == "thread_not_found" || c == "message_not_found" {
			return wireMessage{}, fmt.Errorf("%w: no message %s in %s", ErrCannotPost, ts, channel)
		}
		return wireMessage{}, err
	}
	if len(page.Messages) == 0 {
		return wireMessage{}, fmt.Errorf("%w: no message %s in %s", ErrCannotPost, ts, channel)
	}
	var m wireMessage
	if err := json.Unmarshal(page.Messages[0], &m); err != nil {
		return wireMessage{}, fmt.Errorf("decode message %s in %s: %w", ts, channel, err)
	}
	return m, nil
}

func (s *Service) conversation(ctx context.Context, id string) (Resolved, error) {
	res, _, err := s.conversationInfo(ctx, id)
	return res, err
}

func (s *Service) conversationInfo(ctx context.Context, id string) (Resolved, slack.ConversationInfo, error) {
	info, err := s.Client.ConversationInfo(ctx, id)
	if err != nil {
		if code(err) == "channel_not_found" {
			return Resolved{}, info, fmt.Errorf("%w: no conversation %s", ErrCannotPost, id)
		}
		return Resolved{}, info, err
	}
	if info.IsArchived {
		return Resolved{}, info, fmt.Errorf("%w: %s is archived", ErrCannotPost, id)
	}
	res := Resolved{Channel: info.ID}
	if res.Channel == "" {
		res.Channel = id
	}
	switch {
	case info.IsIM:
		res.DMUser = info.User
		res.Conversation = "@" + info.User
		if u, err := s.Client.UserInfo(ctx, info.User); err == nil {
			if u.Deleted {
				return Resolved{}, info, fmt.Errorf("%w: user %s is deactivated", ErrCannotPost, u.ID)
			}
			if u.Name != "" {
				res.Conversation = "@" + u.Name
			}
		}
	case info.IsMPIM:
		res.Conversation = "group DM"
	default:
		if !info.IsMember {
			return Resolved{}, info, fmt.Errorf("%w: not a member of #%s (%s)", ErrCannotPost, info.Name, id)
		}
		res.Conversation = "#" + info.Name
	}
	return res, info, nil
}

func conversationName(c slack.ConversationInfo) string { return c.Name }

func userName(u slack.UserInfo) string { return u.Name }

// channelByName matches name in the member listing; a match from the cached listing is checked
// against conversations.info, and a name that moved sends the lookup to a fresh listing.
func (s *Service) channelByName(ctx context.Context, name string) (Resolved, error) {
	if name == "" {
		return Resolved{}, fmt.Errorf("%w: empty channel name", ErrAmbiguous)
	}
	for refresh := false; ; refresh = true {
		convs, fromCache, err := s.dir.conversations(ctx, s.Client, refresh)
		if err != nil {
			return Resolved{}, err
		}
		found := matching(convs, func(c slack.ConversationInfo) bool { return strings.EqualFold(c.Name, name) })
		if len(found) == 0 && fromCache {
			continue
		}
		if len(found) != 1 {
			return Resolved{}, candidates("#"+name, " among the channels you are a member of", found, near(convs, conversationName, name),
				func(c slack.ConversationInfo) string { return "#" + c.Name + " (" + c.ID + ")" })
		}
		res, info, err := s.conversationInfo(ctx, found[0].ID)
		if err == nil && strings.EqualFold(info.Name, name) {
			return res, nil
		}
		if !fromCache {
			if err != nil {
				return Resolved{}, err
			}
			return Resolved{}, fmt.Errorf("%w: #%s changed while resolving it; try again", ErrAmbiguous, name)
		}
	}
}

// dmByHandle matches handle in the user listing; a match from the cached listing is checked
// against users.info, and a handle that moved sends the lookup to a fresh listing.
func (s *Service) dmByHandle(ctx context.Context, handle string) (Resolved, error) {
	holds := func(u slack.UserInfo) bool {
		return !u.Deleted && (strings.EqualFold(u.Name, handle) || strings.EqualFold(u.DisplayName, handle))
	}
	for refresh := false; ; refresh = true {
		users, fromCache, err := s.dir.users(ctx, s.Client, refresh)
		if err != nil {
			return Resolved{}, err
		}
		found := matching(users, holds)
		if len(found) == 0 && fromCache {
			continue
		}
		if len(found) != 1 {
			live := matching(users, func(u slack.UserInfo) bool { return !u.Deleted })
			return Resolved{}, candidates("@"+handle, "", found, near(live, userName, handle),
				func(u slack.UserInfo) string { return "@" + u.Name + " (" + u.ID + ")" })
		}
		if !fromCache {
			return s.openDM(ctx, found[0])
		}
		fresh, err := s.Client.UserInfo(ctx, found[0].ID)
		if err == nil && holds(fresh) {
			return s.openDM(ctx, fresh)
		}
	}
}

func (s *Service) dmByEmail(ctx context.Context, email string) (Resolved, error) {
	u, err := s.Client.UserByEmail(ctx, email)
	if err != nil {
		if code(err) == "users_not_found" {
			return Resolved{}, fmt.Errorf("%w: no user with email %s", ErrCannotPost, email)
		}
		return Resolved{}, err
	}
	return s.openDM(ctx, u)
}

func (s *Service) dmWithID(ctx context.Context, id string) (Resolved, error) {
	u, err := s.Client.UserInfo(ctx, id)
	if err != nil {
		if code(err) == "user_not_found" {
			return Resolved{}, fmt.Errorf("%w: no user %s", ErrCannotPost, id)
		}
		return Resolved{}, err
	}
	return s.openDM(ctx, u)
}

func (s *Service) openDM(ctx context.Context, u slack.UserInfo) (Resolved, error) {
	if u.Deleted {
		return Resolved{}, fmt.Errorf("%w: user %s is deactivated", ErrCannotPost, u.ID)
	}
	channel, err := s.Client.OpenDM(ctx, u.ID)
	if err != nil {
		return Resolved{}, err
	}
	return Resolved{Channel: channel, Conversation: "@" + u.Name, DMUser: u.ID}, nil
}

func matching[T any](items []T, keep func(T) bool) []T {
	var out []T
	for _, it := range items {
		if keep(it) {
			out = append(out, it)
		}
	}
	return out
}

const maxCandidates = 10

func near[T any](items []T, name func(T) string, want string) []T {
	want = strings.ToLower(want)
	var out []T
	for _, it := range items {
		if strings.Contains(strings.ToLower(name(it)), want) {
			out = append(out, it)
			if len(out) == maxCandidates {
				break
			}
		}
	}
	return out
}

func candidates[T any](target, scope string, exact, close []T, label func(T) string) error {
	list := exact
	if len(list) == 0 {
		list = close
	}
	if len(list) == 0 {
		return fmt.Errorf("%w: nothing matches %s%s", ErrAmbiguous, target, scope)
	}
	names := make([]string, 0, len(list))
	for _, it := range list {
		names = append(names, label(it))
	}
	if len(exact) > 1 {
		return fmt.Errorf("%w: %s matches %s", ErrAmbiguous, target, strings.Join(names, ", "))
	}
	return fmt.Errorf("%w: nothing matches %s%s; did you mean %s", ErrAmbiguous, target, scope, strings.Join(names, ", "))
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return strings.TrimSpace(line)
}

func code(err error) string {
	var api *slack.APIError
	if errors.As(err, &api) {
		return api.Code
	}
	return ""
}

var loggedOutCodes = []string{"not_authed", "invalid_auth", "token_revoked", "token_expired", "account_inactive"}

func loggedOut(err error) error {
	if err == nil || errors.Is(err, ErrNotLoggedIn) {
		return err
	}
	if errors.Is(err, auth.ErrNotLoggedIn) || slices.Contains(loggedOutCodes, code(err)) {
		return fmt.Errorf("%w: %w", ErrNotLoggedIn, err)
	}
	return err
}

func isUserID(s string) bool { return isID(s, "UW") }

func isConversationID(s string) bool { return isID(s, "CGD") }

func isID(s, prefixes string) bool {
	if len(s) < 3 || !strings.ContainsRune(prefixes, rune(s[0])) {
		return false
	}
	for _, r := range s[1:] {
		if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}
