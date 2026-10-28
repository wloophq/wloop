package slack

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/wloophq/wloop/plugins/slack/unit"
)

// ThreadObject is "<conv>/<ts>": a thread, or a lone channel message keyed by its own ts.
func ThreadObject(conv, ts string) string { return conv + "/" + ts }

// DMObject is "<conv>": a DM or group DM conversation.
func DMObject(conv string) string { return conv }

// ParseObject splits an object id; ts is "" for a DM object.
func ParseObject(s string) (conv, ts string, err error) {
	conv, ts, thread := strings.Cut(s, "/")
	if !convRE.MatchString(conv) || thread && !tsRE.MatchString(ts) {
		return "", "", fmt.Errorf("invalid slack object %q", s)
	}
	return conv, ts, nil
}

// Object is the ref's object id.
func (r ObjectRef) Object() string {
	if r.TS == "" {
		return DMObject(r.Conversation)
	}
	return ThreadObject(r.Conversation, r.TS)
}

// ThreadUnit has the key "<team>/<conv>/<ts>", valid for ValidKey.
func ThreadUnit(team, conv, ts string) unit.Unit {
	return Key{Team: team, Conversation: conv, TS: ts}.Unit()
}

// ObjectOf: in an im or mpim every message, thread replies included, belongs to DMObject(conv);
// elsewhere a message belongs to ThreadObject(conv, ThreadTS or TS).
func ObjectOf(conv string, kind ConversationKind, m Message) string {
	if kind.DM() {
		return DMObject(conv)
	}
	return ThreadObject(conv, m.Anchor())
}

// Anchor is the ts that places the message: its thread's parent, or its own.
func (m Message) Anchor() string { return cmp.Or(m.ThreadTS, m.TS) }

// Version is the thread's Stub.Version: latest_reply, the parent's ts when it
// has no replies, and a lone message's ts with its edited ts.
func (t Thread) Version() string {
	if t.LatestReply != "" {
		return t.LatestReply
	}
	for _, m := range t.Messages {
		if m.TS == t.TS && m.EditedTS != "" {
			return t.TS + "+" + m.EditedTS
		}
	}
	return t.TS
}

// Version is the DM conversation's Stub.Version: its latest message ts.
func (w DMWindow) Version() string {
	v := w.Latest
	for _, m := range w.Messages {
		if CompareTS(m.TS, v) > 0 {
			v = m.TS
		}
	}
	return v
}

// CompareTS orders two Slack timestamps numerically; "" sorts first.
func CompareTS(a, b string) int {
	as, af, _ := strings.Cut(a, ".")
	bs, bf, _ := strings.Cut(b, ".")
	if c := cmp.Compare(len(as), len(bs)); c != 0 {
		return c
	}
	if c := cmp.Compare(as, bs); c != 0 {
		return c
	}
	n := max(len(af), len(bf))
	return cmp.Compare(af+strings.Repeat("0", n-len(af)), bf+strings.Repeat("0", n-len(bf)))
}
