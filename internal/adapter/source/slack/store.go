package slack

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/port"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Cursor names of slack_cursors.
const (
	CursorViewerTeam = "viewer_team"
	CursorViewerUser = "viewer_user"
)

// DefaultPoll is the default Scheduler's interval.
const DefaultPoll = 5 * time.Minute

// Every is a Scheduler that checks every object again after a fixed interval.
type Every time.Duration

func (e Every) Next(_ context.Context, objects []string, now time.Time) (map[string]time.Time, error) {
	out := make(map[string]time.Time, len(objects))
	for _, o := range objects {
		out[o] = now.Add(time.Duration(e))
	}
	return out, nil
}

// Store is the Slack source's port.SourceStore and FactsReader over the
// slack_* tables. Segments may be nil until the segment map exists; a nil
// Scheduler is Every(DefaultPoll), a nil Now is time.Now.
type Store struct {
	DB        *sqlite.SlackStore
	Segments  dslack.Segments
	Scheduler dslack.Scheduler
	Now       func() time.Time
}

var (
	_ port.SourceStore   = (*Store)(nil)
	_ dslack.FactsReader = (*Store)(nil)
)

func (s *Store) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}

func (s *Store) scheduler() dslack.Scheduler {
	if s.Scheduler == nil {
		return Every(DefaultPoll)
	}
	return s.Scheduler
}

// Watched are the watched objects due now, with their stored versions.
func (s *Store) Watched(ctx context.Context) ([]port.Stub, error) {
	rows, err := s.DB.Watched(ctx, s.now())
	if err != nil {
		return nil, err
	}
	out := make([]port.Stub, len(rows))
	for i, r := range rows {
		out[i] = port.Stub{Object: r.Object, Version: r.Version}
	}
	return out, nil
}

func (s *Store) Versions(ctx context.Context, objects []string) (map[string]string, error) {
	return s.DB.Versions(ctx, objects)
}

func (s *Store) Cursor(ctx context.Context) (string, error) {
	return s.DB.Cursor(ctx, sqlite.SlackDiscoverCursor)
}

// Save stores the light facts, then the read facts (the read wins for one
// object), then the cursor; every stored object gets its next check from
// the Scheduler.
func (s *Store) Save(ctx context.Context, light []port.Facts, read port.Facts, cursor string, at time.Time) error {
	var all []dslack.Facts
	via := map[string]dslack.Via{}
	for _, f := range slices.Concat(light, []port.Facts{read}) {
		if f == nil {
			continue
		}
		if w, ok := f.(facts); ok {
			maps.Copy(via, w.via)
		}
		sf, err := slackFacts(f)
		if err != nil {
			return err
		}
		all = append(all, sf)
	}
	rows, err := s.rows(ctx, all, at)
	if err != nil {
		return err
	}
	for _, o := range rows.Objects {
		for i := range o.Messages {
			o.Messages[i].Via = string(via[o.Messages[i].Conversation+"/"+o.Messages[i].TS])
		}
	}
	rows.Cursor = cursor
	_, err = s.DB.Save(ctx, rows, at)
	return err
}

func slackFacts(f port.Facts) (dslack.Facts, error) {
	switch f := f.(type) {
	case facts:
		return f.Facts, nil
	case dslack.Facts:
		return f, nil
	case *dslack.Facts:
		if f != nil {
			return *f, nil
		}
		return dslack.Facts{}, nil
	}
	return dslack.Facts{}, fmt.Errorf("slack store: facts of type %T", f)
}

func (s *Store) rows(ctx context.Context, all []dslack.Facts, at time.Time) (sqlite.FactsRows, error) {
	var out sqlite.FactsRows
	at0 := map[string]int{}
	put := func(o sqlite.ObjectFacts) {
		if i, ok := at0[o.Object.Object]; ok {
			out.Objects[i] = o
			return
		}
		at0[o.Object.Object] = len(out.Objects)
		out.Objects = append(out.Objects, o)
	}
	for _, f := range all {
		team := f.Team
		if team == "" && len(f.Threads) > 0 {
			var err error
			if team, err = s.DB.Cursor(ctx, CursorViewerTeam); err != nil {
				return out, err
			}
			if team == "" {
				return out, errors.New("slack store: thread facts with no team")
			}
		}
		out.Team = cmp.Or(out.Team, team)
		for _, c := range f.Conversations {
			out.Conversations = append(out.Conversations, sqlite.ConversationRow{ID: c.ID, Team: team, Name: c.Name, Kind: string(c.Kind),
				IsMember: c.IsMember, Archived: c.Archived, DMUser: c.DMUser})
		}
		for _, u := range f.Users {
			out.Users = append(out.Users, sqlite.UserRow{ID: u.ID, Name: u.Name, DisplayName: u.DisplayName, Email: u.Email,
				IsBot: u.IsBot || u.ID == "USLACKBOT", Deleted: u.Deleted, FetchedAt: u.Fetched})
		}
		for _, t := range f.Threads {
			o := sqlite.ObjectFacts{Object: sqlite.ObjectRow{Object: dslack.ThreadObject(t.Conversation, t.TS), Conversation: t.Conversation,
				TS: t.TS, Kind: sqlite.SlackThread, Version: t.Version(), LatestReply: t.LatestReply, ReplyCount: t.ReplyCount}}
			units := []unit.Unit{dslack.ThreadUnit(team, t.Conversation, t.TS)}
			for _, m := range t.Messages {
				o.Messages = append(o.Messages, messageRow(t.Conversation, m, units, at))
			}
			o.Object.LastActivityAt = lastActivity(o.Messages, t.LatestReply)
			put(o)
		}
		for _, w := range f.DMs {
			o := sqlite.ObjectFacts{Object: sqlite.ObjectRow{Object: dslack.DMObject(w.Conversation), Conversation: w.Conversation,
				Kind: sqlite.SlackDM, Version: w.Version()}}
			for _, m := range w.Messages {
				units, err := s.unitsFor(ctx, w, m)
				if err != nil {
					return out, err
				}
				o.Messages = append(o.Messages, messageRow(w.Conversation, m, units, at))
			}
			o.Object.LastActivityAt = lastActivity(o.Messages, w.Latest)
			put(o)
		}
	}
	objects := make([]string, len(out.Objects))
	for i, o := range out.Objects {
		objects[i] = o.Object.Object
	}
	if len(objects) == 0 {
		return out, nil
	}
	next, err := s.scheduler().Next(ctx, objects, at)
	if err != nil {
		return out, fmt.Errorf("slack store: schedule: %w", err)
	}
	for i := range out.Objects {
		out.Objects[i].Object.NextCheckAt = next[out.Objects[i].Object.Object]
	}
	return out, nil
}

// unitsFor are the DM segments covering m's anchor, so a reply follows its
// parent's segment; without a segment map, the window's units.
func (s *Store) unitsFor(ctx context.Context, w dslack.DMWindow, m dslack.Message) ([]unit.Unit, error) {
	if s.Segments == nil {
		return w.Units, nil
	}
	us, err := s.Segments.UnitsFor(ctx, w.Conversation, m.Anchor())
	if err != nil {
		return nil, fmt.Errorf("slack store: segments of %s: %w", w.Conversation, err)
	}
	return us, nil
}

func messageRow(conv string, m dslack.Message, units []unit.Unit, at time.Time) sqlite.MessageRow {
	r := sqlite.MessageRow{Conversation: conv, TS: m.TS, ThreadTS: m.ThreadTS, ParentUser: m.ParentUser,
		User: m.User, BotID: m.BotID, AppID: m.AppID, Username: m.Username, Subtype: m.Subtype, Text: m.Text,
		EditedTS: m.EditedTS, Deleted: m.Deleted, Links: m.Links, Mentions: m.Mentions, Permalink: m.Permalink,
		ReceivedAt: at, Units: units}
	for _, f := range m.Files {
		r.Files = append(r.Files, sqlite.FileRow{ID: f.ID, Title: f.Title, Mimetype: f.Mimetype})
	}
	if m.Reactions != nil {
		r.Reactions = make([]sqlite.ReactionRow, 0, len(m.Reactions))
		for _, x := range m.Reactions {
			r.Reactions = append(r.Reactions, sqlite.ReactionRow{Name: x.Name, Users: x.Users})
		}
	}
	return r
}

// lastActivity is the time of the latest ts among the messages and latest.
func lastActivity(ms []sqlite.MessageRow, latest string) time.Time {
	ts := latest
	for _, m := range ms {
		if dslack.CompareTS(m.TS, ts) > 0 {
			ts = m.TS
		}
	}
	return tsTime(ts)
}

func tsTime(ts string) time.Time {
	sec, _, _ := strings.Cut(ts, ".")
	n, err := strconv.ParseInt(sec, 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}
	}
	return time.Unix(n, 0).UTC()
}

// See stamps every object the pass returned and moves each to the
// Scheduler's next check, changed or not.
func (s *Store) See(ctx context.Context, objects []string, at time.Time) error {
	if len(objects) == 0 {
		return nil
	}
	next, err := s.scheduler().Next(ctx, objects, at)
	if err != nil {
		return fmt.Errorf("slack store: schedule: %w", err)
	}
	return s.DB.See(ctx, objects, next, at)
}

// Messages are u's messages after the ts after, ascending, at most limit.
func (s *Store) Messages(ctx context.Context, u unit.Unit, after string, limit int) ([]dslack.Message, error) {
	k, err := dslack.ParseUnit(u)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", dslack.ErrUnknownUnit, err)
	}
	conv, q := k.Conversation, sqlite.MessageQuery{After: after, Limit: limit}
	if k.Segment == "" {
		if _, ok, err := s.DB.Object(ctx, dslack.ThreadObject(conv, k.TS)); err != nil {
			return nil, err
		} else if !ok {
			return nil, fmt.Errorf("%w: %s", dslack.ErrUnknownUnit, u)
		}
		q.Thread = k.TS
	} else {
		if s.Segments == nil {
			return nil, fmt.Errorf("%w: %s: no segment map", dslack.ErrUnknownUnit, u)
		}
		rc, from, to, err := s.Segments.Range(ctx, u)
		if err != nil {
			return nil, err
		}
		if rc == "" || rc != conv {
			return nil, fmt.Errorf("%w: %s", dslack.ErrUnknownUnit, u)
		}
		q.From, q.To = from, to
	}
	rows, err := s.DB.Messages(ctx, conv, q)
	if err != nil {
		return nil, err
	}
	out := make([]dslack.Message, len(rows))
	for i, r := range rows {
		out[i] = message(r)
	}
	return out, nil
}

// Message is conv's stored message at ts; false when none is stored.
func (s *Store) Message(ctx context.Context, conv, ts string) (dslack.Message, bool, error) {
	rows, err := s.DB.Messages(ctx, conv, sqlite.MessageQuery{TS: ts})
	if err != nil || len(rows) == 0 {
		return dslack.Message{}, false, err
	}
	return message(rows[0]), true, nil
}

func message(r sqlite.MessageRow) dslack.Message {
	m := dslack.Message{TS: r.TS, ThreadTS: r.ThreadTS, ParentUser: r.ParentUser, User: r.User, BotID: r.BotID, AppID: r.AppID,
		Username: r.Username, Subtype: r.Subtype, Text: r.Text, EditedTS: r.EditedTS, Deleted: r.Deleted, Links: r.Links,
		Mentions: r.Mentions, Permalink: r.Permalink}
	for _, f := range r.Files {
		m.Files = append(m.Files, dslack.File{ID: f.ID, Title: f.Title, Mimetype: f.Mimetype})
	}
	for _, x := range r.Reactions {
		m.Reactions = append(m.Reactions, dslack.Reaction{Name: x.Name, Users: x.Users})
	}
	return m
}

// MessagesBefore are the newest n messages of conv anchored before ts, ascending.
func (s *Store) MessagesBefore(ctx context.Context, conv, before string, n int) ([]dslack.Message, error) {
	rows, err := s.DB.Messages(ctx, conv, sqlite.MessageQuery{Before: before, Newest: true, Limit: n})
	out := make([]dslack.Message, len(rows))
	for i, r := range rows {
		out[i] = message(r)
	}
	return out, err
}

func (s *Store) Conversation(ctx context.Context, id string) (dslack.Conversation, error) {
	rows, err := s.DB.Conversations(ctx, []string{id})
	if err != nil {
		return dslack.Conversation{}, err
	}
	if len(rows) == 0 {
		return dslack.Conversation{}, fmt.Errorf("slack: unknown conversation %s", id)
	}
	c := rows[0]
	return dslack.Conversation{ID: c.ID, Name: c.Name, Kind: dslack.ConversationKind(c.Kind), IsMember: c.IsMember,
		Archived: c.Archived, DMUser: c.DMUser}, nil
}

func (s *Store) Users(ctx context.Context, ids []string) (map[string]dslack.User, error) {
	rows, err := s.DB.Users(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string]dslack.User, len(rows))
	for _, r := range rows {
		out[r.ID] = user(r)
	}
	return out, nil
}

func (s *Store) UserByEmail(ctx context.Context, email string) (dslack.User, bool, error) {
	r, ok, err := s.DB.UserByEmail(ctx, email)
	if err != nil || !ok {
		return dslack.User{}, false, err
	}
	return user(r), true, nil
}

func user(r sqlite.UserRow) dslack.User {
	return dslack.User{ID: r.ID, Name: r.Name, DisplayName: r.DisplayName, Email: r.Email, IsBot: r.IsBot || r.ID == "USLACKBOT",
		Deleted: r.Deleted, Fetched: r.FetchedAt}
}

func (s *Store) Changes(ctx context.Context, afterSeq int64, limit int) ([]dslack.Change, error) {
	rows, err := s.DB.Changes(ctx, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	out := make([]dslack.Change, len(rows))
	for i, r := range rows {
		out[i] = dslack.Change{Seq: r.Seq, Conversation: r.Conversation, TS: r.TS, ThreadTS: r.ThreadTS,
			Kind: dslack.ChangeKind(r.Kind), At: r.At}
	}
	return out, nil
}

// Viewer is the team and user of the stored login, from the viewer cursors.
func (s *Store) Viewer(ctx context.Context) (team, user string, err error) {
	if team, err = s.DB.Cursor(ctx, CursorViewerTeam); err != nil {
		return "", "", err
	}
	if user, err = s.DB.Cursor(ctx, CursorViewerUser); err != nil {
		return "", "", err
	}
	if team == "" || user == "" {
		return "", "", errors.New("slack: viewer unknown")
	}
	return team, user, nil
}
