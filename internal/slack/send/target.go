package send

import (
	"context"
	"net/url"
	"regexp"
	"strings"
)

// Message names one Slack message: by its ref, "<conversation>/<ts>", or by
// its permalink. ThreadTS is the thread it is in when the name says so (a
// permalink's thread_ts); a ref never does.
type Message struct{ Conversation, TS, ThreadTS string }

// Ref is the message's ref, "<conversation>/<ts>".
func (m Message) Ref() string { return Ref(m.Conversation, m.TS) }

// Ref is the ref of the message at ts in conversation.
func Ref(conversation, ts string) string { return conversation + "/" + ts }

var (
	refRE           = regexp.MustCompile(`^([CGD][A-Z0-9]+)/(\d{10}\.\d{6})$`)
	permalinkPathRE = regexp.MustCompile(`^/archives/([CGD][A-Z0-9]+)/p(\d{10})(\d{6})/?$`)
	tsRE            = regexp.MustCompile(`^\d{10}\.\d{6}$`)
)

// ParseMessage reads s as a message ref or a message permalink.
func ParseMessage(s string) (Message, bool) {
	s = strings.TrimSpace(s)
	if m := refRE.FindStringSubmatch(s); m != nil {
		return Message{Conversation: m[1], TS: m[2]}, true
	}
	return parsePermalink(s)
}

// IsMessage says whether s names one message: a ref or a permalink.
func IsMessage(s string) bool {
	_, ok := ParseMessage(s)
	return ok
}

// IsPermalink says whether s is a Slack message permalink.
func IsPermalink(s string) bool {
	_, ok := parsePermalink(strings.TrimSpace(s))
	return ok
}

func parsePermalink(s string) (Message, bool) {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return Message{}, false
	}
	if host := strings.ToLower(u.Hostname()); host != "slack.com" && !strings.HasSuffix(host, ".slack.com") {
		return Message{}, false
	}
	m := permalinkPathRE.FindStringSubmatch(u.Path)
	if m == nil {
		return Message{}, false
	}
	msg := Message{Conversation: m[1], TS: m[2] + "." + m[3]}
	if t := u.Query().Get("thread_ts"); tsRE.MatchString(t) {
		msg.ThreadTS = t
	}
	return msg, true
}

// Stored finds the message at ts in conversation as the store holds it; ok
// is false when the store holds none.
type Stored func(ctx context.Context, conversation, ts string) (m StoredMessage, ok bool, err error)

// StoredMessage is what placing a reply needs of a stored message. ThreadTS
// is "" or TS for a message that starts no thread or is its parent.
type StoredMessage struct{ TS, ThreadTS, Text string }

// thread is the parent of the thread m is in, m itself when it is in none:
// read from the store, else from Slack.
func (s *Service) thread(ctx context.Context, m Message) (wireMessage, error) {
	at := m.TS
	if m.ThreadTS != "" {
		at = m.ThreadTS
	}
	if s.Stored != nil {
		for range 2 {
			got, ok, err := s.Stored(ctx, m.Conversation, at)
			if err != nil {
				return wireMessage{}, err
			}
			if !ok {
				break
			}
			if got.ThreadTS == "" || got.ThreadTS == got.TS {
				return wireMessage{TS: got.TS, Text: got.Text}, nil
			}
			at = got.ThreadTS
		}
	}
	msg, err := s.firstMessage(ctx, m.Conversation, at)
	if err != nil {
		return wireMessage{}, err
	}
	if msg.ThreadTS != "" && msg.ThreadTS != msg.TS {
		return s.firstMessage(ctx, m.Conversation, msg.ThreadTS)
	}
	return msg, nil
}

// storedThread is the thread the store holds m in, its own TS when it is in
// none; "" when the store does not hold it.
func (s *Service) storedThread(ctx context.Context, m Message) (string, error) {
	if s.Stored == nil {
		return "", nil
	}
	got, ok, err := s.Stored(ctx, m.Conversation, m.TS)
	if err != nil || !ok {
		return "", err
	}
	if got.ThreadTS != "" {
		return got.ThreadTS, nil
	}
	return got.TS, nil
}
