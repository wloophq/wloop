package slack

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
	slackapi "github.com/wloophq/wloop/internal/slack"
)

// Reader renders a unit's or a permalink's messages for slack_read. It reads
// the stored facts and calls Slack only for a channel thread that is not
// stored; a direct message is read from its stored segments only. It never
// stores or admits what it reads.
type Reader struct {
	Facts    dslack.FactsReader
	Segments interface {
		UnitsFor(ctx context.Context, conv, ts string) ([]unit.Unit, error)
	}
	Client *slackapi.Client
	// Location is the time zone Search shows times in; nil is the local one.
	Location *time.Location
}

// ReaderOver reads the facts the Slack module stores in db.
func ReaderOver(db *sqlite.DB, c *slackapi.Client) *Reader {
	segs := Segments{Store: db.Segments()}
	return &Reader{Facts: &Store{DB: db.Slack(), Segments: segs}, Segments: segs, Client: c}
}

const unitPrefix = string(dslack.Provider) + ":"

// Read renders target, a unit ("slack:<key>") or a message permalink, keeping
// the first message and the newest up to limit in all.
func (r *Reader) Read(ctx context.Context, target string, limit int) (string, error) {
	target = strings.TrimSpace(target)
	if key, ok := strings.CutPrefix(target, unitPrefix); ok {
		return r.readUnit(ctx, unit.Unit{Provider: dslack.Provider, Key: key}, limit)
	}
	conv, ts, threadTS, err := parsePermalink(target)
	if err != nil {
		return "", fmt.Errorf("%w; the target is a unit (slack:<key>) or a message permalink", err)
	}
	anchor := ts
	if threadTS != "" {
		anchor = threadTS
	}
	c, err := r.conversation(ctx, conv)
	if err != nil {
		return "", err
	}
	if c.Kind.DM() {
		units, err := r.Segments.UnitsFor(ctx, conv, anchor)
		if err != nil {
			return "", err
		}
		if len(units) == 0 {
			return "", fmt.Errorf("slack: %s ts %s is a direct message that is not tracked; slack_read reads only tracked direct messages", conv, anchor)
		}
		return r.readUnit(ctx, units[0], limit)
	}
	if team, _, err := r.Facts.Viewer(ctx); err == nil {
		msgs, err := r.Facts.Messages(ctx, dslack.ThreadUnit(team, conv, anchor), "", 0)
		if err != nil && !errors.Is(err, dslack.ErrUnknownUnit) {
			return "", err
		}
		if len(msgs) > 0 {
			return r.render(ctx, c, anchor, msgs, target, limit)
		}
	}
	msgs, cutOff, err := r.replies(ctx, conv, anchor)
	if err != nil {
		return "", err
	}
	out, err := r.render(ctx, c, anchor, msgs, target, limit)
	if cutOff {
		out += fmt.Sprintf("\n\n(thread longer than %d messages; newer ones not read, open the permalink)", readPage*readMaxPages)
	}
	return out, err
}

func (r *Reader) readUnit(ctx context.Context, u unit.Unit, limit int) (string, error) {
	k, err := dslack.ParseUnit(u)
	if err != nil {
		return "", err
	}
	msgs, err := r.Facts.Messages(ctx, u, "", 0)
	if err != nil {
		return "", err
	}
	if len(msgs) == 0 {
		return "", fmt.Errorf("%w: %s has no stored messages", dslack.ErrUnknownUnit, u)
	}
	c, err := r.Facts.Conversation(ctx, k.Conversation)
	if err != nil {
		c = dslack.Conversation{ID: k.Conversation}
	}
	return r.render(ctx, c, k.TS, msgs, "", limit)
}

// conversation is the stored conversation, else Slack's conversations.info.
func (r *Reader) conversation(ctx context.Context, id string) (dslack.Conversation, error) {
	if c, err := r.Facts.Conversation(ctx, id); err == nil {
		return c, nil
	}
	info, err := r.Client.ConversationInfo(ctx, id)
	if err != nil {
		return dslack.Conversation{}, err
	}
	c := dslack.Conversation{ID: id, Name: info.Name, Kind: dslack.KindChannel, IsMember: info.IsMember, Archived: info.IsArchived, DMUser: info.User}
	switch {
	case info.IsIM:
		c.Kind, c.Name = dslack.KindIM, ""
	case info.IsMPIM:
		c.Kind = dslack.KindMPIM
	case info.IsPrivate || info.IsGroup:
		c.Kind = dslack.KindPrivate
	}
	return c, nil
}

// replies reads a thread live; cutOff says Slack had more after the last page read.
func (r *Reader) replies(ctx context.Context, conv, ts string) (msgs []dslack.Message, cutOff bool, err error) {
	cursor := ""
	for range readMaxPages {
		p, err := r.Client.Replies(ctx, conv, ts, slackapi.PageArgs{Cursor: cursor, Limit: readPage})
		if err != nil {
			return nil, false, err
		}
		if msgs, err = appendPage(msgs, p.Messages); err != nil {
			return nil, false, err
		}
		cutOff = p.HasMore && p.NextCursor != ""
		if !cutOff {
			break
		}
		cursor = p.NextCursor
	}
	if len(msgs) == 0 {
		return nil, false, fmt.Errorf("slack: no message %s in %s", ts, conv)
	}
	return msgs, cutOff, nil
}

func (r *Reader) render(ctx context.Context, c dslack.Conversation, anchor string, msgs []dslack.Message, link string, limit int) (string, error) {
	msgs = visible(msgs)
	users, err := r.Facts.Users(ctx, mentioned(msgs))
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(heading(c, anchor, users))
	if len(msgs) > 0 && msgs[0].Permalink != "" {
		link = msgs[0].Permalink
	}
	if link == "" && len(msgs) > 0 {
		link = fmt.Sprintf("no permalink (channel %s, ts %s)", c.ID, msgs[0].TS)
	}
	b.WriteString(": " + link + "\n")
	index := map[string]int{}
	for i, m := range msgs {
		index[m.TS] = i + 1
	}
	cut := 0
	if limit > 0 && len(msgs) > limit {
		cut = len(msgs) - limit
	}
	for i, m := range msgs {
		if i > 0 && i <= cut {
			if i == 1 {
				fmt.Fprintf(&b, "\n(%d %s left out)\n", cut, plural(cut, "message"))
			}
			continue
		}
		b.WriteString("\n" + renderRead(m, index, anchor, users))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func heading(c dslack.Conversation, anchor string, users map[string]dslack.User) string {
	switch {
	case c.Kind == dslack.KindIM && anchor == "":
		return "Direct message with " + label(c.DMUser, users)
	case c.Kind == dslack.KindMPIM && anchor == "":
		return "Group direct message " + c.ID
	case c.Name != "":
		return "Slack thread in #" + c.Name
	}
	return "Slack thread in " + c.ID
}

func plural(n int, noun string) string {
	if n == 1 {
		return noun
	}
	return noun + "s"
}

func visible(msgs []dslack.Message) []dslack.Message {
	out := make([]dslack.Message, 0, len(msgs))
	for _, m := range msgs {
		if !slices.Contains(dslack.HiddenSubtypes, m.Subtype) {
			out = append(out, m)
		}
	}
	return out
}

// mentioned are the users a rendering names: authors, mentions and reactors.
func mentioned(msgs []dslack.Message) []string {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, m := range msgs {
		add(m.User)
		add(m.ParentUser)
		for _, id := range m.Mentions {
			add(id)
		}
		for _, x := range m.Reactions {
			for _, id := range x.Users {
				add(id)
			}
		}
	}
	return out
}

var readMention = regexp.MustCompile(`<@([UW][A-Z0-9]+)>`)

// renderRead is one message as "[m<N> ts=<ts>] <author>[ reply to m<K>]",
// its text, its files and its reactions; a reply is marked only when it
// answers another message than the thread being read.
func renderRead(m dslack.Message, index map[string]int, anchor string, users map[string]dslack.User) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[m%d ts=%s] %s", index[m.TS], m.TS, readAuthor(m, users))
	if t := m.ThreadTS; t != "" && t != m.TS && t != anchor {
		if k, ok := index[t]; ok {
			fmt.Fprintf(&b, " reply to m%d", k)
		} else {
			fmt.Fprintf(&b, " reply to ts=%s", t)
		}
	}
	text := m.Text
	switch {
	case m.Deleted:
		text = "(deleted)"
	case m.EditedTS != "":
		text += " (edited)"
	}
	text = readMention.ReplaceAllStringFunc(text, func(s string) string {
		id := s[2 : len(s)-1]
		if name := userName(id, users); name != "" {
			return "<@" + id + "|" + name + ">"
		}
		return s
	})
	b.WriteString("\n" + text + "\n")
	if len(m.Files) > 0 {
		titles := make([]string, len(m.Files))
		for i, f := range m.Files {
			titles[i] = f.Title
		}
		b.WriteString("files: " + strings.Join(titles, ", ") + "\n")
	}
	if len(m.Reactions) > 0 {
		parts := make([]string, len(m.Reactions))
		for i, x := range m.Reactions {
			who := make([]string, len(x.Users))
			for j, id := range x.Users {
				who[j] = label(id, users)
			}
			parts[i] = x.Name + " by " + strings.Join(who, ", ")
		}
		b.WriteString("reactions: " + strings.Join(parts, "; ") + "\n")
	}
	return b.String()
}

func readAuthor(m dslack.Message, users map[string]dslack.User) string {
	switch {
	case m.User != "":
		return label(m.User, users)
	case m.Username != "":
		return m.Username + " (bot " + m.BotID + ")"
	}
	return "bot " + m.BotID
}

func userName(id string, users map[string]dslack.User) string {
	u := users[id]
	if u.DisplayName != "" {
		return u.DisplayName
	}
	return u.Name
}

func label(id string, users map[string]dslack.User) string {
	if name := userName(id, users); name != "" {
		return name + " (" + id + ")"
	}
	return id
}
