package slack

import (
	"context"
	"regexp"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/config"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
)

// Admission rules (Slack spec 5.1), named in logs and tests.
const (
	RuleMention   = "mention"
	RuleTerm      = "term"
	RuleRepliedTo = "replied-to-viewer"
	RuleWroteIn   = "viewer-in-thread"
	RuleDM        = "dm"
	RuleFollowed  = "followed"
	RuleWatched   = "watched"
)

// admitter decides which deltas are kept; a dropped message is never stored.
type admitter struct {
	db     *sqlite.SlackStore
	users  *userCache
	viewer func() string
	terms  []*regexp.Regexp
	allow  map[string]bool
	deny   map[string]bool
	// pending finds a message waiting in the inbox.
	pending func(conv, ts string) (dslack.Message, bool)
	log     func(format string, args ...any)
}

func newAdmitter(cfg config.SlackIngest) *admitter {
	a := &admitter{allow: set(cfg.AllowApps), deny: set(cfg.DenyApps)}
	for _, t := range cfg.Terms {
		if t = strings.TrimSpace(t); t != "" {
			a.terms = append(a.terms, regexp.MustCompile(`(?i)(^|[^\pL\pN_])`+regexp.QuoteMeta(t)+`($|[^\pL\pN_])`))
		}
	}
	return a
}

func set(ids []string) map[string]bool {
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id != "" {
			out[id] = true
		}
	}
	return out
}

// verdict is an admitted delta's object and the rule that admitted it; a
// zero verdict drops the delta.
type verdict struct {
	object string
	kind   dslack.ConversationKind
	rule   string
}

// admit judges d. A dropped message or edit moves its conversation's
// high-water mark only.
func (a *admitter) admit(ctx context.Context, d dslack.Delta) (verdict, error) {
	kind, err := a.kindOf(ctx, d)
	if err != nil {
		return verdict{}, err
	}
	v, err := a.judge(ctx, d, kind)
	if err != nil || v.rule != "" {
		return v, err
	}
	if d.Kind == dslack.KindMessage || d.Kind == dslack.KindEdit {
		return verdict{}, a.db.MarkSeen(ctx, d.Conversation, d.TS)
	}
	return verdict{}, nil
}

func (a *admitter) judge(ctx context.Context, d dslack.Delta, kind dslack.ConversationKind) (verdict, error) {
	switch d.Kind {
	case dslack.KindReactionAdded, dslack.KindReactionRemoved, dslack.KindDelete:
		m, ok, err := a.message(ctx, d.Conversation, d.TS)
		if err != nil || !ok {
			return verdict{}, err
		}
		return verdict{object: dslack.ObjectOf(d.Conversation, kind, m), kind: kind, rule: RuleWatched}, nil
	case dslack.KindEdit:
		m, ok, err := a.message(ctx, d.Conversation, d.TS)
		if err != nil {
			return verdict{}, err
		}
		if ok {
			if d.Message == nil || m.Text == d.Message.Text {
				return verdict{}, nil
			}
			return verdict{object: dslack.ObjectOf(d.Conversation, kind, m), kind: kind, rule: RuleWatched}, nil
		}
	}
	if d.Message == nil {
		return verdict{}, nil
	}
	return a.judgeMessage(ctx, d, kind)
}

func (a *admitter) judgeMessage(ctx context.Context, d dslack.Delta, kind dslack.ConversationKind) (verdict, error) {
	m := *d.Message
	if a.deny[m.BotID] || a.deny[m.AppID] {
		return verdict{}, nil
	}
	allowed := a.allow[m.BotID] || a.allow[m.AppID]
	if !allowed && (m.User == "" || m.User == slackbot) {
		return verdict{}, nil
	}
	object := dslack.ObjectOf(d.Conversation, kind, m)
	rule, err := a.rule(ctx, m, kind, object)
	if err != nil || rule == "" {
		return verdict{}, err
	}
	if !allowed {
		if bot, err := a.bot(ctx, m.User); err != nil || bot {
			return verdict{}, err
		}
	}
	return verdict{object: object, kind: kind, rule: rule}, nil
}

// keep says whether a message read from Slack in an admitted object is
// stored: integration posts are dropped by the same rules as deltas.
func (a *admitter) keep(ctx context.Context, m dslack.Message) (bool, error) {
	if a.deny[m.BotID] || a.deny[m.AppID] {
		return false, nil
	}
	if a.allow[m.BotID] || a.allow[m.AppID] {
		return true, nil
	}
	if m.User == "" || m.User == slackbot {
		return false, nil
	}
	bot, err := a.bot(ctx, m.User)
	return !bot, err
}

// bot asks the user cache; a user it cannot read counts as a human, so a
// failed users.info never loses a person's message.
func (a *admitter) bot(ctx context.Context, id string) (bool, error) {
	u, err := a.users.get(ctx, id)
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		a.logf("slack admit: user %s: %v", id, err)
		return false, nil
	}
	return u.IsBot, nil
}

func (a *admitter) rule(ctx context.Context, m dslack.Message, kind dslack.ConversationKind, object string) (string, error) {
	me := a.viewer()
	reply := m.ThreadTS != "" && m.ThreadTS != m.TS
	switch {
	case me != "" && slices.Contains(m.Mentions, me):
		return RuleMention, nil
	case a.term(m.Text):
		return RuleTerm, nil
	case me != "" && reply && m.ParentUser == me && m.User != me:
		return RuleRepliedTo, nil
	case me != "" && reply && m.User == me:
		return RuleWroteIn, nil
	case kind.DM():
		return RuleDM, nil
	}
	o, ok, err := a.db.Object(ctx, object)
	if err != nil || !ok || !o.Watched {
		return "", err
	}
	if slices.Contains(o.Reasons, string(dslack.ReasonUserFollow)) {
		return RuleFollowed, nil
	}
	return RuleWatched, nil
}

func (a *admitter) term(text string) bool {
	for _, re := range a.terms {
		if re.MatchString(text) {
			return true
		}
	}
	return false
}

// message is the message at ts, waiting in the inbox or stored.
func (a *admitter) message(ctx context.Context, conv, ts string) (dslack.Message, bool, error) {
	if a.pending != nil {
		if m, ok := a.pending(conv, ts); ok {
			return m, true, nil
		}
	}
	return storedMessage(ctx, a.db, conv, ts)
}

// kindOf is the delta's conversation kind: its channel_type, else the
// stored conversation's, else im for a D conversation.
func (a *admitter) kindOf(ctx context.Context, d dslack.Delta) (dslack.ConversationKind, error) {
	if k := kindOfChannelType(d.ChannelType); k != "" {
		return k, nil
	}
	rows, err := a.db.Conversations(ctx, []string{d.Conversation})
	if err != nil {
		return "", err
	}
	if len(rows) == 1 && rows[0].Kind != "" {
		return dslack.ConversationKind(rows[0].Kind), nil
	}
	if strings.HasPrefix(d.Conversation, "D") {
		return dslack.KindIM, nil
	}
	return dslack.KindChannel, nil
}

func kindOfChannelType(t string) dslack.ConversationKind {
	switch t {
	case "channel":
		return dslack.KindChannel
	case "group":
		return dslack.KindPrivate
	case "im":
		return dslack.KindIM
	case "mpim":
		return dslack.KindMPIM
	}
	return ""
}

func (a *admitter) logf(format string, args ...any) {
	if a.log != nil {
		a.log(format, args...)
	}
}

// storedMessage is the stored message at ts in conv, with its reactions.
func storedMessage(ctx context.Context, db *sqlite.SlackStore, conv, ts string) (dslack.Message, bool, error) {
	before, ok := precedingTS(ts)
	if !ok {
		return dslack.Message{}, false, nil
	}
	rows, err := db.Messages(ctx, conv, sqlite.MessageQuery{After: before, Limit: 1})
	if err != nil || len(rows) == 0 || rows[0].TS != ts {
		return dslack.Message{}, false, err
	}
	return message(rows[0]), true, nil
}

// precedingTS is the ts one microsecond before ts, so "after it, limit 1"
// selects ts itself when it is stored.
func precedingTS(ts string) (string, bool) {
	b := []byte(ts)
	for i := len(b) - 1; i >= 0; i-- {
		switch {
		case b[i] == '.':
			continue
		case b[i] < '0' || b[i] > '9':
			return "", false
		case b[i] > '0':
			b[i]--
			return string(b), true
		}
		b[i] = '9'
	}
	return "", false
}
