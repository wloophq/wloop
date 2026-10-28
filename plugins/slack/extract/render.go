package extract

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	slack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/host"
)

// Markers Render writes around the context and a segment.
const (
	ContextHeading = "-- context, read only: earlier messages to understand what follows; extract nothing from them --"
	ContextEnds    = "-- end of context --"
	SegmentStarts  = "-- segment starts: extract only from here --"
	SegmentEnds    = "-- segment ends --"
)

// Render is the conversation block; the eval uses it for every stage.
func Render(in Input) string {
	msgs := window(in)
	var b strings.Builder
	b.WriteString(header(in))
	index := map[string]string{}
	for i, m := range in.Context {
		index[m.TS] = fmt.Sprintf("c%d", i+1)
	}
	for i, m := range msgs {
		index[m.TS] = fmt.Sprintf("m%d", i+1)
	}
	if len(in.Context) > 0 {
		b.WriteString(ContextHeading + "\n")
		for _, m := range in.Context {
			b.WriteString(renderMessage(in, m, index[m.TS], false, index))
		}
		b.WriteString(ContextEnds + "\n")
	}
	if cut := len(in.Messages) - len(msgs); cut > 0 {
		fmt.Fprintf(&b, "(%d older messages left out)\n", cut)
	}
	seg := in.Segment
	started, ended := false, false
	for _, m := range msgs {
		inSeg := seg.FromTS != "" && !tsLess(m.TS, seg.FromTS) && (seg.ToTS == "" || !tsLess(seg.ToTS, m.TS))
		if started && !ended && !inSeg {
			b.WriteString(SegmentEnds + "\n")
			ended = true
		}
		if inSeg && !started {
			b.WriteString(SegmentStarts + "\n")
			started = true
		}
		b.WriteString(renderMessage(in, m, index[m.TS], isNew(in, m), index))
	}
	if started && !ended {
		b.WriteString(SegmentEnds + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func header(in Input) string {
	c := in.Conversation
	var h string
	switch c.Kind {
	case "im":
		h = "Direct message with " + userLabel(in, c.DMUser)
	case "mpim":
		h = "Group direct message"
	case "private":
		h = "Private channel #" + c.Name
	default:
		h = "Channel #" + c.Name
	}
	h += "\n"
	if len(in.Segments) > 0 {
		h += "Existing segments:"
		for i, s := range in.Segments {
			to := s.ToTS
			if to == "" {
				to = "open"
			}
			h += fmt.Sprintf(" s%d ts=%s to %s;", i+1, s.FromTS, to)
		}
		h = strings.TrimSuffix(h, ";") + "\n"
	}
	return h
}

var (
	mentionPattern = regexp.MustCompile(`<@([UW][A-Z0-9]+)>`)
	// markerLike is the dash run that opens a marker, in text that is not one.
	markerLike = regexp.MustCompile(`(?i)-{2,}(\s*(?:end of )?(?:context|segment)\b)`)
)

// renderMessage writes m with the dashes of any marker look-alike split, so no text a
// message carries reads as a marker.
func renderMessage(in Input, m slack.Message, label string, isNew bool, index map[string]string) string {
	return markerLike.ReplaceAllString(renderRaw(in, m, label, isNew, index), "- -$1")
}

func renderRaw(in Input, m slack.Message, label string, isNew bool, index map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s ts=%s %s] ", label, m.TS, slack.TSTime(m.TS).In(zone(in)).Format("2006-01-02 15:04 MST"))
	if in.Focus != "" && m.TS == in.Focus {
		b.WriteString("focus ")
	}
	if isNew {
		b.WriteString("new ")
	}
	b.WriteString(author(in, m))
	if m.ThreadTS != "" && m.ThreadTS != m.TS {
		if k, ok := index[m.ThreadTS]; ok {
			b.WriteString(" reply to " + k)
		} else {
			fmt.Fprintf(&b, " reply to ts=%s", m.ThreadTS)
		}
	}
	text := m.Text
	switch {
	case m.Deleted:
		text = "(deleted)"
	case m.EditedTS != "":
		text += " (edited)"
	}
	text = mentionPattern.ReplaceAllStringFunc(text, func(s string) string {
		id := s[2 : len(s)-1]
		if name := userName(in, id); name != id {
			return "<@" + id + "|" + name + ">"
		}
		return s
	})
	b.WriteString(": " + strings.ReplaceAll(text, "\n", "\n  ") + "\n")
	if len(m.Files) > 0 {
		titles := make([]string, len(m.Files))
		for i, f := range m.Files {
			titles[i] = f.Title
		}
		b.WriteString("  files: " + strings.Join(titles, ", ") + "\n")
	}
	if len(m.Reactions) > 0 {
		parts := make([]string, len(m.Reactions))
		for i, r := range m.Reactions {
			who := make([]string, len(r.Users))
			for j, u := range r.Users {
				who[j] = userLabel(in, u)
			}
			parts[i] = r.Name + " by " + strings.Join(who, ", ")
		}
		b.WriteString("  reactions: " + strings.Join(parts, "; ") + "\n")
	}
	return b.String()
}

func author(in Input, m slack.Message) string {
	switch {
	case m.User != "":
		return userLabel(in, m.User)
	case m.Username != "":
		return m.Username + " (bot " + m.BotID + ")"
	default:
		return "bot " + m.BotID
	}
}

func userName(in Input, id string) string {
	u, ok := in.Users[id]
	switch {
	case !ok:
		return id
	case u.DisplayName != "":
		return u.DisplayName
	case u.Name != "":
		return u.Name
	}
	return id
}

func userLabel(in Input, id string) string {
	if name := userName(in, id); name != id {
		return name + " (" + id + ")"
	}
	return id
}

func zone(in Input) *time.Location { return in.Now.Location() }

func isNew(in Input, m slack.Message) bool { return in.NewFrom == "" || !tsLess(m.TS, in.NewFrom) }

// window drops the oldest messages before NewFrom until the rendering fits in.WindowChars,
// keeping every new message and every parent a new message replies to.
func window(in Input) []slack.Message {
	if in.WindowChars <= 0 {
		return in.Messages
	}
	keep := make([]bool, len(in.Messages))
	sizes := make([]int, len(in.Messages))
	parents := map[string]bool{}
	total := 0
	for i, m := range in.Messages {
		keep[i] = true
		sizes[i] = len(renderMessage(in, m, fmt.Sprintf("m%d", i+1), isNew(in, m), nil))
		total += sizes[i]
		if isNew(in, m) && m.ThreadTS != "" && m.ThreadTS != m.TS {
			parents[m.ThreadTS] = true
		}
	}
	for i, m := range in.Messages {
		if total <= in.WindowChars {
			break
		}
		if isNew(in, m) || parents[m.TS] {
			continue
		}
		keep[i] = false
		total -= sizes[i]
	}
	out := make([]slack.Message, 0, len(in.Messages))
	for i, m := range in.Messages {
		if keep[i] {
			out = append(out, m)
		}
	}
	return out
}

// tsLess compares Slack timestamps ("<seconds>.<micros>") numerically.
func tsLess(a, b string) bool {
	as, af, _ := strings.Cut(a, ".")
	bs, bf, _ := strings.Cut(b, ".")
	if len(as) != len(bs) {
		return len(as) < len(bs)
	}
	if as != bs {
		return as < bs
	}
	return af < bf
}

// ContextBefore is the newest of msgs, ascending, within n messages and chars of text.
func ContextBefore(msgs []slack.Message, n, chars int) []slack.Message {
	from, total := len(msgs), 0
	for from > 0 && len(msgs)-from < n {
		total += len(msgs[from-1].Text)
		if total > chars {
			break
		}
		from--
	}
	return msgs[from:]
}

// TierFor is the frontier tier when in renders longer than over, else base; over 0 never escalates.
func TierFor(in Input, base host.Tier, over int) host.Tier {
	if over > 0 && len(Render(in)) > over {
		return host.TierFrontier
	}
	return base
}
