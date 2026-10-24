package factsession

import (
	"cmp"
	"context"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
)

// quietStates are the newest turn states cleanup.idle applies to.
var quietStates = []turn.State{turn.Done, turn.Settled, turn.Failed}

// world is one read of the registry, the managers' snapshot and the turn
// books, shared by the facts of every session in it.
type world struct {
	p    *Provider
	key  worldKey
	snap *wsdomain.Snapshot
	// open are the registry's sessions not closed, by id; terminals the
	// processes of the snapshot that a session took, by session id.
	open      map[string]turn.Session
	terminals map[string]wsdomain.Terminal
	spaces    map[string]wsdomain.Workspace
	agents    []turns.AgentObservation
	units     map[string]unit.Unit
	books     map[unit.Unit]turn.Book
	prs       map[unit.Unit]string
	// transcripts are each session's known transcript file.
	transcripts map[string]string
	// closedByManager are the dormant sessions their manager's record closed.
	closedByManager map[string]bool
}

type worldKey struct {
	snapshot uint64
	log      port.Generation
}

// read is the world as of the snapshot's and the change log's generations,
// read again when either moved.
func (p *Provider) read(ctx context.Context) (*world, error) {
	snap, err := p.d.Snapshot.Read(ctx, wsdomain.PartWorkspaces|wsdomain.PartPanes|wsdomain.PartTerminals)
	if err != nil {
		return nil, err
	}
	key := worldKey{snapshot: snap.Gen}
	if p.d.Changes != nil {
		key.log = p.d.Changes.Gen()
		p.mu.Lock()
		cached := p.cached
		p.mu.Unlock()
		if cached != nil && cached.key == key {
			return cached, nil
		}
	}
	w, err := p.build(ctx, snap)
	if err != nil {
		return nil, err
	}
	w.key = key
	p.mu.Lock()
	p.cached = w
	p.mu.Unlock()
	return w, nil
}

func (p *Provider) build(ctx context.Context, snap *wsdomain.Snapshot) (*world, error) {
	svc := p.d.Turns
	w := &world{p: p, snap: snap, open: map[string]turn.Session{}, terminals: map[string]wsdomain.Terminal{}, spaces: map[string]wsdomain.Workspace{}}
	sessions, err := svc.Registry.Sessions(ctx)
	if err != nil {
		return nil, err
	}
	w.transcripts, w.closedByManager = map[string]string{}, map[string]bool{}
	for _, s := range sessions {
		w.open[s.ID] = s
		if p.d.ClosedByManager != nil && s.State() == turn.SessionDormant && s.Manager != "" {
			closed, err := p.d.ClosedByManager(ctx, s.Manager, s.ID, s.LastHandle)
			if err != nil {
				return nil, err
			}
			w.closedByManager[s.ID] = closed
		}
		if p.d.Transcript != nil && s.AgentSession != "" {
			path, err := p.d.Transcript(ctx, s.AgentSession)
			if err != nil {
				return nil, err
			}
			w.transcripts[s.ID] = path
		}
	}
	for _, ws := range snap.Workspaces {
		w.spaces[ws.ID] = ws
	}
	for _, t := range snap.Terminals {
		id, known, err := svc.Registry.(turns.Handles).SessionOf(ctx, t.Manager, t.Handle)
		if err != nil {
			return nil, err
		}
		if known {
			w.terminals[id] = t
		}
	}
	if w.agents, err = svc.Sessions.Agents(ctx); err != nil {
		return nil, err
	}
	if w.units, err = svc.Store.UnitsOf(ctx, w.ids()); err != nil {
		return nil, err
	}
	var wanted []unit.Unit
	for _, u := range w.units {
		if !slices.Contains(wanted, u) {
			wanted = append(wanted, u)
		}
	}
	w.books = map[unit.Unit]turn.Book{}
	if len(wanted) > 0 {
		if w.books, err = svc.Store.Books(ctx, wanted...); err != nil {
			return nil, err
		}
	}
	w.prs = map[unit.Unit]string{}
	var prUnits []unit.Unit
	for _, u := range wanted {
		if u.Provider == unit.GitHub {
			prUnits = append(prUnits, u)
		}
	}
	if len(prUnits) > 0 && p.d.PRs != nil {
		got, err := p.d.PRs.PRs(ctx, prUnits)
		if err != nil {
			return nil, err
		}
		for u, pr := range got {
			w.prs[u] = strings.ToLower(pr.State)
		}
	}
	return w, nil
}

// ids are the open registry sessions, sorted.
func (w *world) ids() []string {
	out := make([]string, 0, len(w.open))
	for id := range w.open {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// state is what the facts of one session are made of.
type state struct {
	w         *world
	sess      turn.Session
	term      wsdomain.Terminal
	hasTerm   bool
	agent     turns.AgentObservation
	hasAgent  bool
	focused   bool
	unit      unit.Unit
	open      *turn.Turn
	newest    *turn.Turn
	idleSince time.Time
	// transcriptGone is a known transcript file of the session that no longer exists.
	transcriptGone bool
}

func (w *world) state(h string) *state {
	s := &state{w: w}
	sess := w.open[h]
	s.term, s.hasTerm = w.terminals[h]
	s.sess = sess
	if s.hasTerm {
		s.agent, s.hasAgent = turns.AgentOf(w.agents, sess, turns.Terminal{Session: h, Manager: s.term.Manager, Handle: s.term.Handle,
			Key: s.term.TabID, Title: s.term.Title, Agent: s.term.Agent, LastOutput: s.term.LastOutput})
	}
	s.focused = sess.WorkspaceID != "" && slices.ContainsFunc(w.agents, func(a turns.AgentObservation) bool { return a.UserActive && a.WorkspaceID == sess.WorkspaceID })
	if u, ok := w.units[h]; ok {
		s.unit = u
		b := w.books[u]
		s.open = openTurn(&b, h)
		s.newest, _ = b.OfSession(h)
	}
	s.transcriptGone = sess.State() == turn.SessionDormant && w.transcripts[sess.ID] != "" && !w.p.d.Exists(w.transcripts[sess.ID])
	s.idleSince = sess.IdleSince
	if s.idleSince.IsZero() && !sess.Managed && s.hasAgent && s.agent.State == turn.AgentDone {
		s.idleSince = s.agent.StateSince
	}
	return s
}

// openTurn is the handle's newest open turn: started on it, or sent to it
// and not started yet.
func openTurn(b *turn.Book, handle string) *turn.Turn {
	for i := len(b.Turns) - 1; i >= 0; i-- {
		t := &b.Turns[i]
		if t.State.Open() && (t.Session == handle || (t.State == turn.Sent && t.SentTo == handle)) {
			return t
		}
	}
	return nil
}

func (s *state) listed() bool { return s.hasTerm && s.term.Usable }

func (s *state) facts(now time.Time, pf turns.PromptFacts, processGone bool) workflow.Facts {
	w := s.w
	f := workflow.Facts{
		"session.state":                 string(s.sess.State()),
		"session.manager":               s.sess.Manager,
		"session.managed":               s.sess.Managed,
		"session.listed":                s.listed(),
		"session.orphaned":              s.hasTerm && !s.term.Usable,
		"session.agent_state":           "",
		"session.user_active":           s.focused,
		"session.user_typed_after_turn": pf.TypedAfterTurn,
		"session.prompt_knowable":       pf.Knowable,
		"session.prompted_while_idle":   pf.PromptedWhileIdle,
		"session.open_turn":             "",
		"session.newest_state":          "",
		"session.newest_intent":         "",
		"session.closes_when_done":      false,
		"session.idle_for":              time.Duration(0),
		"session.pr_state":              w.prs[s.unit],
		"session.duplicate":             s.duplicate(),
		"session.worktree_gone":         s.worktreeGone(),
		"session.transcript_gone":       s.transcriptGone,
		"session.closed_by_manager":     w.closedByManager[s.sess.ID],
		"session.process_gone":          processGone,
		"session.fingerprint":           s.fingerprint(),
		"session.repo":                  repoOf(s.unit),
	}
	if s.hasAgent {
		f["session.agent_state"] = s.agent.State.String()
	}
	if s.open != nil {
		f["session.open_turn"] = string(s.open.State)
	}
	if t := s.newest; t != nil {
		f["session.newest_state"] = string(t.State)
		f["session.newest_intent"] = string(t.Intent)
		f["session.closes_when_done"] = w.p.d.Turns.Intents != nil && w.p.d.Turns.Intents.ClosesWhenDone(string(t.Intent))
		if born := cmp.Or(t.SentAt, t.StartedAt); !born.IsZero() {
			f["session.newest_age"] = max(0, now.Sub(born))
		}
	}
	if !s.idleSince.IsZero() {
		f["session.idle_since"] = s.idleSince
		f["session.idle_for"] = max(0, now.Sub(s.idleSince))
	}
	return f
}

// duplicate marks a listed open session resuming the same agent session as
// another one, unless it is focused or the user prompted it; the oldest one
// stays when none of them is.
func (s *state) duplicate() bool {
	if !s.listed() || s.sess.AgentSession == "" || s.kept() {
		return false
	}
	w := s.w
	var group []*state
	for h, o := range w.open {
		if h != s.sess.ID && o.AgentSession == s.sess.AgentSession {
			if other := w.state(h); other.listed() {
				group = append(group, other)
			}
		}
	}
	if len(group) == 0 {
		return false
	}
	if slices.ContainsFunc(group, (*state).kept) {
		return true
	}
	return slices.ContainsFunc(group, func(o *state) bool { return older(o.sess, s.sess) })
}

// kept is a session a duplicate never closes: focused, or prompted by the
// user since it started.
func (s *state) kept() bool {
	return s.focused || (!s.sess.UserPromptAt.IsZero() && !s.sess.UserPromptAt.Before(s.sess.StartedAt))
}

func older(a, b turn.Session) bool {
	return cmp.Or(a.StartedAt.Compare(b.StartedAt), strings.Compare(a.ID, b.ID)) < 0
}

// worktreeGone stats only local paths: a listed workspace on this host, or
// the absolute worktree of a session whose workspace a complete listing lacks.
func (s *state) worktreeGone() bool {
	path := s.sess.Worktree
	if ws, listed := s.w.spaces[s.sess.WorkspaceID]; listed {
		if !ws.Local() {
			return false
		}
		path = cmp.Or(path, ws.Path)
	} else if !s.w.snap.WorkspacesComplete(s.sess.Manager) || !filepath.IsAbs(path) {
		return false
	}
	return !s.w.p.d.Exists(path)
}

// fingerprint is id:state:idle-since:newest-seq, so a session's item is the
// same while it stays in one state, idle, and its book does not move.
func (s *state) fingerprint() string {
	since := ""
	if !s.idleSince.IsZero() {
		since = s.idleSince.Format(time.RFC3339Nano)
	}
	var seq int64
	if s.newest != nil {
		seq = s.newest.Seq
	}
	return s.sess.ID + ":" + string(s.sess.State()) + ":" + since + ":" + strconv.FormatInt(seq, 10)
}

func repoOf(u unit.Unit) string {
	ref, ok := port.PROf(port.Event{Unit: u})
	if !ok {
		return ""
	}
	return ref.Owner + "/" + ref.Repo
}
