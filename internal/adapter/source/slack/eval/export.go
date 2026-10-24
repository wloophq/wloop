package eval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/adapter/source/slack/extract"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type ExportOptions struct {
	DB       *sqlite.SlackStore
	Facts    slack.FactsReader
	Segments slack.Segments // nil leaves every DM unsegmented
	// Since and Until bound the activity: a unit with a message in [Since, Until).
	Since, Until time.Time
	Out          string
	Exclude      []string // fixtures directories; a unit sharing any (channel, message ts) with one of their fixtures is skipped
	Now          time.Time
	// Before reads the newest n messages of a conversation anchored before a ts; nil exports no context.
	Before                        func(ctx context.Context, conv, before string, n int) ([]slack.Message, error)
	ContextMessages, ContextChars int
}

type ExportReport struct {
	Written, Excluded, Unsegmented int
}

// Export writes one holdout fixture per Slack unit with activity in the
// window, users.json and viewer.json.
func Export(ctx context.Context, o ExportOptions) (ExportReport, error) {
	var rep ExportReport
	team, viewer, err := o.Facts.Viewer(ctx)
	if err != nil {
		return rep, err
	}
	seen, err := excluded(o.Exclude)
	if err != nil {
		return rep, err
	}
	units, unsegmented, err := activeUnits(ctx, o, team)
	if err != nil {
		return rep, err
	}
	rep.Unsegmented = unsegmented
	names := map[string]string{}
	if _, err := os.Stat(filepath.Join(o.Out, usersFile)); err == nil {
		if names, err = loadUsers(filepath.Join(o.Out, usersFile)); err != nil {
			return rep, err
		}
	}
	for _, u := range units {
		k, _ := slack.ParseUnit(u)
		msgs, err := o.Facts.Messages(ctx, u, "", 0)
		if err != nil {
			return rep, fmt.Errorf("messages of %s: %w", u, err)
		}
		if len(msgs) == 0 {
			continue
		}
		conv, err := o.Facts.Conversation(ctx, k.Conversation)
		if err != nil {
			return rep, err
		}
		users, err := o.Facts.Users(ctx, people(msgs))
		if err != nil {
			return rep, err
		}
		for id, us := range users {
			if n := cmpOr(us.DisplayName, us.Name); n != "" {
				names[id] = n
			}
		}
		var before []slack.Message
		if conv.Kind.DM() && o.Before != nil && o.ContextMessages > 0 {
			got, err := o.Before(ctx, k.Conversation, msgs[0].Anchor(), o.ContextMessages)
			if err != nil {
				return rep, fmt.Errorf("context of %s: %w", u, err)
			}
			before = extract.ContextBefore(got, o.ContextMessages, o.ContextChars)
			users, err := o.Facts.Users(ctx, people(before))
			if err != nil {
				return rep, err
			}
			for id, us := range users {
				if n := cmpOr(us.DisplayName, us.Name); n != "" {
					names[id] = n
				}
			}
		}
		f := fixture(u, conv, msgs, names, viewer, o.Now)
		if conv.Kind.DM() {
			f.FocusTS = msgs[0].TS
			for _, m := range before {
				f.Context = append(f.Context, fixtureMessage(m, names))
			}
		}
		if sharesAMessage(f, seen) {
			rep.Excluded++
			continue
		}
		if err := writeJSON(filepath.Join(o.Out, f.ID+".json"), f); err != nil {
			return rep, err
		}
		rep.Written++
	}
	if err := writeJSON(filepath.Join(o.Out, usersFile), names); err != nil {
		return rep, err
	}
	vu, err := o.Facts.Users(ctx, []string{viewer})
	if err != nil {
		return rep, err
	}
	v := vu[viewer]
	return rep, writeJSON(filepath.Join(o.Out, viewerFile), map[string]string{"id": viewer, "name": cmpOr(v.DisplayName, v.Name)})
}

// activeUnits are the units with a stored message in the window, in
// conversation and ts order, and how many DM messages had no segment.
func activeUnits(ctx context.Context, o ExportOptions, team string) ([]unit.Unit, int, error) {
	convs, err := o.DB.AdmittedConversations(ctx)
	if err != nil {
		return nil, 0, err
	}
	var out []unit.Unit
	have := map[unit.Unit]bool{}
	unsegmented := 0
	for _, c := range convs {
		rows, err := o.DB.Messages(ctx, c.ID, sqlite.MessageQuery{})
		if err != nil {
			return nil, 0, err
		}
		for _, m := range rows {
			at, ok := tsTime(m.TS)
			if !ok || at.Before(o.Since) || !at.Before(o.Until) {
				continue
			}
			var us []unit.Unit
			if slack.ConversationKind(c.Kind).DM() {
				if o.Segments != nil {
					if us, err = o.Segments.UnitsFor(ctx, c.ID, m.Anchor()); err != nil {
						return nil, 0, err
					}
				}
				if len(us) == 0 {
					unsegmented++
				}
			} else {
				us = []unit.Unit{slack.ThreadUnit(team, c.ID, m.Anchor())}
			}
			for _, u := range us {
				if !have[u] {
					have[u] = true
					out = append(out, u)
				}
			}
		}
	}
	return out, unsegmented, nil
}

func excluded(dirs []string) (map[string]bool, error) {
	seen := map[string]bool{}
	for _, d := range dirs {
		s, err := Load(d)
		if err != nil {
			return nil, fmt.Errorf("--exclude %s: %w", d, err)
		}
		for _, f := range s.Fixtures {
			for _, m := range f.Messages {
				seen[f.ChannelID+"/"+m.TS] = true
			}
		}
	}
	return seen, nil
}

// sharesAMessage says whether f holds a message of an excluded fixture, so a
// conversation seen under another span or root is still skipped.
func sharesAMessage(f Fixture, seen map[string]bool) bool {
	for _, m := range f.Messages {
		if seen[f.ChannelID+"/"+m.TS] {
			return true
		}
	}
	return false
}

func people(msgs []slack.Message) []string {
	var ids []string
	for _, m := range msgs {
		ids = append(ids, m.User)
		ids = append(ids, m.Mentions...)
		for _, r := range m.Reactions {
			ids = append(ids, r.Users...)
		}
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if len(ids) > 0 && ids[0] == "" {
		ids = ids[1:]
	}
	return ids
}

// fixtureID names a unit without its ids: a hash of its key.
func fixtureID(u unit.Unit) string {
	h := sha256.Sum256([]byte(u.String()))
	return "x" + hex.EncodeToString(h[:])[:10]
}

func fixture(u unit.Unit, c slack.Conversation, msgs []slack.Message, names map[string]string, viewer string, now time.Time) Fixture {
	f := Fixture{ID: fixtureID(u), Kind: "thread", Split: SplitHoldout, Now: now.Format(time.RFC3339),
		ChannelID: c.ID, ChannelName: c.Name, ChannelType: string(c.Kind), Permalink: msgs[0].Permalink}
	authored, mentioned := false, false
	for _, m := range msgs {
		authored = authored || m.User == viewer
		mentioned = mentioned || slices.Contains(m.Mentions, viewer) || strings.Contains(m.Text, "<@"+viewer)
		f.Messages = append(f.Messages, fixtureMessage(m, names))
	}
	switch {
	case c.Kind.DM():
		f.Kind = "dm_segment"
	case mentioned && !authored:
		f.Kind = "mention"
	}
	return f
}

func fixtureMessage(m slack.Message, names map[string]string) Message {
	fm := Message{TS: m.TS, ThreadTS: m.ThreadTS, UserID: m.User, UserName: names[m.User], IsBot: m.BotID != "",
		Text: m.Text, ReplyCount: m.ReplyCount, Edited: m.EditedTS != ""}
	if m.BotID != "" {
		fm.AppName = m.Username
	}
	if m.Deleted {
		fm.Text = "(deleted)"
	}
	for _, r := range m.Reactions {
		fm.Reactions = append(fm.Reactions, Reaction{Name: r.Name, Users: r.Users})
	}
	for _, fl := range m.Files {
		fm.Files = append(fm.Files, cmpOr(fl.Title, fl.ID))
	}
	return fm
}
