package slack

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/link"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
	slackapi "github.com/wloophq/wloop/internal/slack"
	"github.com/wloophq/wloop/internal/slack/send"
)

// Page sizes of the reads the source makes when the inbox cannot answer.
const (
	readPage     = 200
	readMaxPages = 5
	historyLimit = 100
)

// Source is the Slack port.Source. Read builds facts from the inbox and
// calls Slack only for an object the store cannot complete: a thread whose
// parent is unknown, or an object with no waiting delta.
type Source struct {
	db       *sqlite.SlackStore
	store    *Store
	client   *slackapi.Client
	admit    *admitter
	inbox    *inbox
	search   *searcher
	segments dslack.Segments
	links    bool
	// units are the codecs a link's unit is read through.
	units func() *unit.Registry
	log   func(format string, args ...any)

	mu         sync.Mutex
	team, user string
	asked      map[string]bool
}

var _ port.Source = (*Source)(nil)

func (s *Source) Provider() unit.ProviderID { return dslack.Provider }

// Stubs are the objects at their stored versions, so only an object never
// read (version "") moves.
func (s *Source) Stubs(ctx context.Context, objects []string) ([]port.Stub, error) {
	vs, err := s.db.Versions(ctx, objects)
	if err != nil {
		return nil, err
	}
	out := make([]port.Stub, len(objects))
	for i, o := range objects {
		out[i] = port.Stub{Object: o, Version: vs[o]}
	}
	return out, nil
}

func (s *Source) Viewer(context.Context) (string, error) {
	_, user := s.viewer()
	if user == "" {
		return "", errors.New("slack: viewer unknown")
	}
	return user, nil
}

func (s *Source) viewer() (team, user string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.team, s.user
}

func (s *Source) setViewer(ctx context.Context, team, user string) error {
	s.mu.Lock()
	same := s.team == team && s.user == user
	s.team, s.user = team, user
	s.mu.Unlock()
	if same {
		return nil
	}
	if err := s.db.SetCursor(ctx, CursorViewerTeam, team); err != nil {
		return err
	}
	return s.db.SetCursor(ctx, CursorViewerUser, user)
}

// facts are the read facts with the links their messages name.
type facts struct {
	dslack.Facts
	links []link.Link
	// via is how each message read from a delta arrived, by "<conv>/<ts>".
	via map[string]dslack.Via
}

var _ port.Linker = facts{}

func (f facts) Links() []link.Link { return f.links }

func (s *Source) Read(ctx context.Context, objects []string) (port.Facts, error) {
	team, _ := s.viewer()
	f := facts{Facts: dslack.Facts{Team: team}}
	pending := s.inbox.peek(objects)
	kinds := map[string]dslack.ConversationKind{}
	for _, o := range objects {
		conv, ts, err := dslack.ParseObject(o)
		if err != nil {
			return nil, err
		}
		es := pending[o]
		for _, e := range es {
			kinds[conv] = cmp.Or(kinds[conv], e.kind)
			if f.via == nil {
				f.via = map[string]dslack.Via{}
			}
			f.via[conv+"/"+e.delta.TS] = e.delta.Via
		}
		if ts == "" {
			w, ok, err := s.readDM(ctx, conv, es)
			if err != nil {
				return nil, err
			}
			if ok {
				f.DMs = append(f.DMs, w)
			}
			continue
		}
		t, ok, err := s.readThread(ctx, conv, ts, es)
		if err != nil {
			return nil, err
		}
		if ok {
			f.Threads = append(f.Threads, t)
		}
	}
	convs, err := s.newConversations(ctx, kinds)
	if err != nil {
		return nil, err
	}
	named, err := s.names(ctx, threadConversations(f.Threads))
	if err != nil {
		return nil, err
	}
	f.Conversations = withNamed(convs, named)
	if s.links {
		if f.links, err = s.linksOf(ctx, f.Facts); err != nil {
			return nil, err
		}
	}
	return f, nil
}

func threadConversations(ts []dslack.Thread) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Conversation
	}
	return out
}

// withNamed are convs with each named conversation in place of its entry.
func withNamed(convs, named []dslack.Conversation) []dslack.Conversation {
	for _, n := range named {
		if i := slices.IndexFunc(convs, func(c dslack.Conversation) bool { return c.ID == n.ID }); i >= 0 {
			convs[i] = n
			continue
		}
		convs = append(convs, n)
	}
	return convs
}

// newConversations are the conversations the store has no kind for yet.
func (s *Source) newConversations(ctx context.Context, kinds map[string]dslack.ConversationKind) ([]dslack.Conversation, error) {
	if len(kinds) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(kinds))
	for id := range kinds {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	rows, err := s.db.Conversations(ctx, ids)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, r := range rows {
		known[r.ID] = r.Kind != ""
	}
	var out []dslack.Conversation
	for _, id := range ids {
		if !known[id] && kinds[id] != "" {
			out = append(out, dslack.Conversation{ID: id, Kind: kinds[id]})
		}
	}
	return out, nil
}

func (s *Source) readThread(ctx context.Context, conv, anchor string, es []entry) (dslack.Thread, bool, error) {
	row, known, err := s.db.Object(ctx, dslack.ThreadObject(conv, anchor))
	if err != nil {
		return dslack.Thread{}, false, err
	}
	msgs, err := s.apply(ctx, conv, es)
	if err != nil {
		return dslack.Thread{}, false, err
	}
	parent := slices.ContainsFunc(msgs, func(m dslack.Message) bool { return m.TS == anchor })
	if !parent {
		if _, parent, err = storedMessage(ctx, s.db, conv, anchor); err != nil {
			return dslack.Thread{}, false, err
		}
	}
	if len(es) == 0 || !parent {
		return s.readReplies(ctx, conv, anchor)
	}
	t := dslack.Thread{Conversation: conv, TS: anchor, Messages: msgs}
	if known {
		t.LatestReply, t.ReplyCount = row.LatestReply, row.ReplyCount
	}
	for _, m := range msgs {
		if m.TS == anchor {
			if dslack.CompareTS(m.LatestReply, t.LatestReply) > 0 {
				t.LatestReply = m.LatestReply
			}
			t.ReplyCount = max(t.ReplyCount, m.ReplyCount)
			continue
		}
		if dslack.CompareTS(m.TS, t.LatestReply) > 0 && !m.Deleted {
			t.LatestReply = m.TS
			t.ReplyCount++
		}
	}
	return t, true, nil
}

// readReplies reads the whole thread; the parent is kept whoever posted it.
func (s *Source) readReplies(ctx context.Context, conv, anchor string) (dslack.Thread, bool, error) {
	msgs, err := s.replies(ctx, conv, anchor)
	if gone(err) {
		s.logf("slack read: thread %s/%s: %v", conv, anchor, err)
		return dslack.Thread{}, false, nil
	}
	if err != nil {
		return dslack.Thread{}, false, err
	}
	t := dslack.Thread{Conversation: conv, TS: anchor}
	for _, m := range msgs {
		if m.TS == anchor {
			t.LatestReply, t.ReplyCount = m.LatestReply, m.ReplyCount
			t.Messages = append(t.Messages, m)
			continue
		}
		if m.Anchor() != anchor {
			continue
		}
		keep, err := s.admit.keep(ctx, m)
		if err != nil {
			return dslack.Thread{}, false, err
		}
		if keep {
			t.Messages = append(t.Messages, m)
		}
	}
	return t, true, nil
}

func (s *Source) readDM(ctx context.Context, conv string, es []entry) (dslack.DMWindow, bool, error) {
	w := dslack.DMWindow{Conversation: conv}
	row, known, err := s.db.Object(ctx, dslack.DMObject(conv))
	if err != nil {
		return w, false, err
	}
	if known {
		w.Latest = row.Version
	}
	var msgs []dslack.Message
	if len(es) == 0 {
		if msgs, err = s.history(ctx, conv); gone(err) {
			s.logf("slack read: conversation %s: %v", conv, err)
			return w, false, nil
		}
	} else {
		msgs, err = s.apply(ctx, conv, es)
	}
	if err != nil {
		return w, false, err
	}
	if msgs, err = s.withParents(ctx, conv, msgs); err != nil {
		return w, false, err
	}
	w.Messages = msgs
	if w.Units, err = s.dmUnits(ctx, conv, msgs); err != nil {
		return w, false, err
	}
	return w, true, nil
}

// withParents adds the thread of every reply whose parent is neither in
// msgs nor stored, so a DM reply never lands without its parent.
func (s *Source) withParents(ctx context.Context, conv string, msgs []dslack.Message) ([]dslack.Message, error) {
	have := map[string]bool{}
	for _, m := range msgs {
		have[m.TS] = true
	}
	var anchors []string
	for _, m := range msgs {
		if a := m.Anchor(); a != m.TS && !have[a] && !slices.Contains(anchors, a) {
			anchors = append(anchors, a)
		}
	}
	for _, a := range anchors {
		if _, ok, err := storedMessage(ctx, s.db, conv, a); err != nil || ok {
			if err != nil {
				return nil, err
			}
			continue
		}
		read, err := s.replies(ctx, conv, a)
		if gone(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, m := range read {
			if have[m.TS] {
				continue
			}
			keep := m.TS == a
			if !keep {
				if keep, err = s.admit.keep(ctx, m); err != nil {
					return nil, err
				}
			}
			if keep {
				have[m.TS] = true
				msgs = append(msgs, m)
			}
		}
	}
	slices.SortFunc(msgs, func(a, b dslack.Message) int { return dslack.CompareTS(a.TS, b.TS) })
	return msgs, nil
}

func (s *Source) dmUnits(ctx context.Context, conv string, msgs []dslack.Message) ([]unit.Unit, error) {
	if s.segments == nil {
		return nil, nil
	}
	var out []unit.Unit
	for _, m := range msgs {
		us, err := s.segments.UnitsFor(ctx, conv, m.Anchor())
		if err != nil {
			return nil, err
		}
		for _, u := range us {
			if !slices.Contains(out, u) {
				out = append(out, u)
			}
		}
	}
	return out, nil
}

// apply folds the deltas into messages, ascending ts: the latest content
// wins, a delete marks the message, and a reaction makes the message's full
// reaction set, empty when its last reaction went.
func (s *Source) apply(ctx context.Context, conv string, es []entry) ([]dslack.Message, error) {
	byTS := map[string]*dslack.Message{}
	for _, e := range es {
		d := e.delta
		cur := byTS[d.TS]
		switch d.Kind {
		case dslack.KindReactionAdded, dslack.KindReactionRemoved:
			if cur == nil {
				m, ok, err := storedMessage(ctx, s.db, conv, d.TS)
				if err != nil {
					return nil, err
				}
				if !ok {
					continue
				}
				cur = &m
				byTS[d.TS] = cur
			}
			if cur.Reactions == nil {
				if err := s.loadReactions(ctx, conv, cur); err != nil {
					return nil, err
				}
			}
			if d.Reaction != nil {
				cur.Reactions = react(cur.Reactions, d.Reaction.Name, d.Reaction.User, d.Kind == dslack.KindReactionAdded)
			}
		default:
			if d.Message == nil {
				continue
			}
			m := *d.Message
			if cur != nil && m.Reactions == nil {
				m.Reactions = cur.Reactions
			}
			if m.ThreadTS == "" && d.Kind != dslack.KindMessage {
				if cur != nil {
					m.ThreadTS = cur.ThreadTS
				} else if stored, ok, err := storedMessage(ctx, s.db, conv, d.TS); err != nil {
					return nil, err
				} else if ok {
					m.ThreadTS = stored.ThreadTS
				}
			}
			byTS[d.TS] = &m
		}
	}
	out := make([]dslack.Message, 0, len(byTS))
	for _, m := range byTS {
		out = append(out, *m)
	}
	slices.SortFunc(out, func(a, b dslack.Message) int { return dslack.CompareTS(a.TS, b.TS) })
	return out, nil
}

// loadReactions sets m's stored reaction set, never nil.
func (s *Source) loadReactions(ctx context.Context, conv string, m *dslack.Message) error {
	stored, ok, err := storedMessage(ctx, s.db, conv, m.TS)
	if err != nil {
		return err
	}
	m.Reactions = []dslack.Reaction{}
	if ok && stored.Reactions != nil {
		m.Reactions = stored.Reactions
	}
	return nil
}

func react(rs []dslack.Reaction, name, user string, add bool) []dslack.Reaction {
	out := make([]dslack.Reaction, 0, len(rs)+1)
	found := false
	for _, r := range rs {
		users := slices.Clone(r.Users)
		if r.Name == name {
			found = true
			if i := slices.Index(users, user); add && i < 0 {
				users = append(users, user)
			} else if !add && i >= 0 {
				users = slices.Delete(users, i, i+1)
			}
		}
		if len(users) > 0 {
			out = append(out, dslack.Reaction{Name: r.Name, Users: users})
		}
	}
	if add && !found {
		out = append(out, dslack.Reaction{Name: name, Users: []string{user}})
	}
	return out
}

// replies reads a thread, every message a full state: reactions never nil.
func (s *Source) replies(ctx context.Context, conv, ts string) ([]dslack.Message, error) {
	var out []dslack.Message
	cursor := ""
	for range readMaxPages {
		p, err := s.client.Replies(ctx, conv, ts, slackapi.PageArgs{Cursor: cursor, Limit: readPage})
		if err != nil {
			return nil, err
		}
		if out, err = appendPage(out, p.Messages); err != nil {
			return nil, err
		}
		if !p.HasMore || p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}
	return out, nil
}

// history is the latest page of a DM conversation, integration posts dropped.
func (s *Source) history(ctx context.Context, conv string) ([]dslack.Message, error) {
	p, err := s.client.History(ctx, conv, slackapi.PageArgs{Limit: historyLimit})
	if err != nil {
		return nil, err
	}
	msgs, err := appendPage(nil, p.Messages)
	if err != nil {
		return nil, err
	}
	out := msgs[:0]
	for _, m := range msgs {
		keep, err := s.admit.keep(ctx, m)
		if err != nil {
			return nil, err
		}
		if keep {
			out = append(out, m)
		}
	}
	slices.SortFunc(out, func(a, b dslack.Message) int { return dslack.CompareTS(a.TS, b.TS) })
	return out, nil
}

func appendPage(out []dslack.Message, raws []json.RawMessage) ([]dslack.Message, error) {
	for _, raw := range raws {
		m, err := dslack.MessageFrom(raw)
		if err != nil {
			return nil, err
		}
		if m.Reactions == nil {
			m.Reactions = []dslack.Reaction{}
		}
		out = append(out, m)
	}
	return out, nil
}

// gone says whether Slack answered that the object no longer exists or is
// out of reach, so the pass goes on without it.
func gone(err error) bool {
	var api *slackapi.APIError
	if !errors.As(err, &api) {
		return false
	}
	switch api.Code {
	case "thread_not_found", "message_not_found", "channel_not_found", "not_in_channel", "is_archived":
		return true
	}
	return false
}

func (s *Source) logf(format string, args ...any) {
	if s.log != nil {
		s.log(format, args...)
	}
}

// linksOf are the mention links of every unit the facts name, over the
// unit's stored messages with the facts' on top, so a pass that reads one
// reply keeps the links of the earlier ones.
func (s *Source) linksOf(ctx context.Context, f dslack.Facts) ([]link.Link, error) {
	var out []link.Link
	for _, t := range f.Threads {
		u := dslack.ThreadUnit(f.Team, t.Conversation, t.TS)
		ls, err := s.unitLinks(ctx, u, t.Conversation, t.Messages)
		if err != nil {
			return nil, err
		}
		out = append(out, ls...)
	}
	for _, w := range f.DMs {
		for _, u := range w.Units {
			var mine []dslack.Message
			for _, m := range w.Messages {
				us, err := s.segments.UnitsFor(ctx, w.Conversation, m.Anchor())
				if err != nil {
					return nil, err
				}
				if slices.Contains(us, u) {
					mine = append(mine, m)
				}
			}
			ls, err := s.unitLinks(ctx, u, w.Conversation, mine)
			if err != nil {
				return nil, err
			}
			out = append(out, ls...)
		}
	}
	return out, nil
}

func (s *Source) unitLinks(ctx context.Context, u unit.Unit, conv string, read []dslack.Message) ([]link.Link, error) {
	stored, err := s.store.Messages(ctx, u, "", 0)
	if err != nil && !errors.Is(err, dslack.ErrUnknownUnit) {
		return nil, err
	}
	byTS := map[string]dslack.Message{}
	for _, m := range stored {
		byTS[m.TS] = m
	}
	for _, m := range read {
		if old, ok := byTS[m.TS]; ok && (old.Deleted || dslack.CompareTS(m.EditedTS, old.EditedTS) < 0) {
			continue
		}
		byTS[m.TS] = m
	}
	msgs := make([]dslack.Message, 0, len(byTS))
	for _, m := range byTS {
		msgs = append(msgs, m)
	}
	slices.SortFunc(msgs, func(a, b dslack.Message) int { return dslack.CompareTS(a.TS, b.TS) })
	var out []link.Link
	seen := map[unit.Unit]bool{}
	for _, m := range msgs {
		if m.Deleted {
			continue
		}
		for _, raw := range m.Links {
			to, ok := linkedUnit(s.units, raw)
			if !ok || seen[to] {
				continue
			}
			seen[to] = true
			at := tsTime(m.TS)
			out = append(out, link.Link{From: u, To: to, Kind: link.Mention, Origin: link.Facts, At: at,
				Evidence: unit.Anchor{Unit: u, Ref: send.Ref(conv, m.TS), Permalink: m.Permalink, At: at}})
		}
	}
	return out, nil
}

// linkedUnit is the unit of another plugin a URL names, through the codecs
// of units; nil reads none.
func linkedUnit(units func() *unit.Registry, raw string) (unit.Unit, bool) {
	if units == nil {
		return unit.Unit{}, false
	}
	u, ok := units().ParseURL(strings.TrimSpace(raw))
	if !ok || u.Provider == dslack.Provider {
		return unit.Unit{}, false
	}
	return u, true
}
