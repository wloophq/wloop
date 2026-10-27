package slack

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/wloophq/wloop/internal/config"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/turn"
)

// Payloads reads a Slack unit's turn payload from the stored facts: its
// messages rendered as slack_read renders them, within MaxChars.
type Payloads struct {
	Facts dslack.FactsReader
	// MaxChars zero is the config's default.
	MaxChars int
}

var (
	_ turn.PayloadReader = Payloads{}
	_ turn.Contents      = Payload{}
)

func (p *plugin) payloads(maxChars int) Payloads {
	return Payloads{Facts: p.src.store, MaxChars: maxChars}
}

// Payload is a Slack unit as a turn reads it.
type Payload struct {
	unit     unit.Unit
	wording  turn.Wording
	contents string
}

func (p Payload) Unit() unit.Unit         { return p.unit }
func (p Payload) Arg() string             { return p.unit.String() }
func (p Payload) Wording() turn.Wording   { return p.wording }
func (p Payload) Fact(string) (any, bool) { return nil, false }
func (p Payload) Contents() string        { return p.contents }

func (r Payloads) Payload(ctx context.Context, req turn.Request) (turn.Payload, error) {
	k, err := dslack.ParseUnit(req.Unit)
	if err != nil {
		return nil, err
	}
	msgs, err := r.Facts.Messages(ctx, req.Unit, "", 0)
	if err != nil && !errors.Is(err, dslack.ErrUnknownUnit) {
		return nil, err
	}
	msgs = visible(msgs)
	c, err := r.Facts.Conversation(ctx, k.Conversation)
	if err != nil {
		c = dslack.Conversation{ID: k.Conversation}
	}
	ids := mentioned(msgs)
	if c.DMUser != "" {
		ids = append(ids, c.DMUser)
	}
	users, err := r.Facts.Users(ctx, ids)
	if err != nil {
		return nil, err
	}
	p := Payload{unit: req.Unit, wording: wording(c, k, users)}
	if len(msgs) == 0 {
		p.contents = heading(c, k.TS, users) + ": no message is stored yet."
		return p, nil
	}
	p.contents = renderCapped(c, k.TS, msgs, users, req.Unit, r.maxChars())
	return p, nil
}

func (r Payloads) maxChars() int {
	return config.SlackTurns{MaxChars: r.MaxChars}.Chars()
}

func wording(c dslack.Conversation, k dslack.Key, users map[string]dslack.User) turn.Wording {
	where := c.ID
	switch {
	case c.Kind == dslack.KindIM:
		where = "DM with " + oneLine(displayName(c.DMUser, users), 40)
	case c.Kind == dslack.KindMPIM:
		where = "group DM " + c.ID
	case c.Name != "":
		where = "#" + c.Name
	}
	w := turn.Wording{Noun: "Slack thread", This: "this Slack thread", Ref: where + " thread " + k.TS}
	if k.Segment != "" {
		w = turn.Wording{Noun: "conversation", This: "this conversation", Ref: where + ", segment " + k.Segment}
	}
	w.Short = w.Ref
	return w
}

func displayName(id string, users map[string]dslack.User) string {
	if name := userName(id, users); name != "" {
		return name
	}
	return id
}

// oneLine keeps a name another person chose on one plain line of at most n
// characters: no control characters, backticks or angle brackets.
func oneLine(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '`' || r == '<' || r == '>':
			return -1
		case unicode.IsControl(r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r):
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		s = strings.TrimSpace(string(r[:n]))
	}
	return s
}

// renderCapped renders msgs as slack_read does. Over maxChars it keeps the
// parent and the newest messages that fit, and says how many it left out.
func renderCapped(c dslack.Conversation, anchor string, msgs []dslack.Message, users map[string]dslack.User, u unit.Unit, maxChars int) string {
	link := msgs[0].Permalink
	if link == "" {
		link = fmt.Sprintf("no permalink (channel %s, ts %s)", c.ID, msgs[0].TS)
	}
	head := heading(c, anchor, users) + ": " + link + "\n"
	index := map[string]int{}
	for i, m := range msgs {
		index[m.TS] = i + 1
	}
	blocks := make([]string, len(msgs))
	total := runes(head)
	for i, m := range msgs {
		blocks[i] = "\n" + renderRead(c.ID, m, index, anchor, users)
		total += runes(blocks[i])
	}
	if total <= maxChars {
		return strings.TrimRight(head+strings.Join(blocks, ""), "\n")
	}
	left := func(n int) string {
		return fmt.Sprintf("\n(%d %s left out to fit the turn; `slack_read %s` with a larger limit reads up to the %d newest)\n", n, plural(n, "message"), u, slackReadLimit)
	}
	room := maxChars - runes(head) - runes(left(len(msgs)))
	parent := clip(blocks[0], room/2)
	room -= runes(parent)
	var kept []string
	for i := len(blocks) - 1; i > 0; i-- {
		b := blocks[i]
		if runes(b) > room {
			if len(kept) == 0 && room > 0 {
				kept = append(kept, clip(b, room))
			}
			break
		}
		kept = append(kept, b)
		room -= runes(b)
	}
	var out strings.Builder
	out.WriteString(head + parent)
	if n := len(blocks) - 1 - len(kept); n > 0 {
		out.WriteString(left(n))
	}
	for i := len(kept) - 1; i >= 0; i-- {
		out.WriteString(kept[i])
	}
	return strings.TrimRight(out.String(), "\n")
}

// slackReadLimit is the largest limit slack_read takes (internal/transport/mcp).
const slackReadLimit = 200

const cutMark = " [cut]\n"

// clip cuts s to at most n characters, marking the cut.
func clip(s string, n int) string {
	if runes(s) <= n {
		return s
	}
	keep := n - runes(cutMark)
	if keep <= 0 {
		return ""
	}
	return string([]rune(s)[:keep]) + cutMark
}

func runes(s string) int { return utf8.RuneCountInString(s) }
