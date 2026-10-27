package turns

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/shell"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// loopTitle matches the titles the loop gives the agent terminals it
// starts: a session name that a user-facing intent's session title leads.
func loopTitle(titles []string) *regexp.Regexp {
	quoted := make([]string, len(titles))
	for i, t := range titles {
		quoted[i] = regexp.QuoteMeta(t)
	}
	return regexp.MustCompile(`^(` + strings.Join(quoted, "|") + `)( .+)?$`)
}

type sessionFacts struct {
	Session    turn.Session
	Listed     bool
	Agent      AgentObservation
	HasAgent   bool
	UserActive bool
	// Unit is the unit of the session's book, zero when it has none.
	Unit unit.Unit
	// Open is the session's open turn, Newest its newest turn.
	Open, Newest *turn.Turn
}

// sight is one cleanup run's look at Orca: its agents and the terminals of
// the workspaces whose terminals could be read.
type sight struct {
	agents    []AgentObservation
	terminals map[string][]Terminal
	unread    map[string]bool
}

func (s *Service) lookAround(ctx context.Context) (*sight, error) {
	agents, err := s.Sessions.Agents(ctx)
	if err != nil {
		return nil, err
	}
	return &sight{agents: agents, terminals: map[string][]Terminal{}, unread: map[string]bool{}}, nil
}

// terminalsOf reads the workspace's terminals once per run; ok is false when
// they could not be read.
func (s *Service) terminalsOf(ctx context.Context, o *sight, workspaceID string) ([]Terminal, bool) {
	if o.unread[workspaceID] {
		return nil, false
	}
	if ts, ok := o.terminals[workspaceID]; ok {
		return ts, true
	}
	ts, err := s.Sessions.Terminals(ctx, workspaceID)
	if err != nil {
		o.unread[workspaceID] = true
		return nil, false
	}
	o.terminals[workspaceID] = ts
	return ts, true
}

// backfill takes, as a session of its own, every agent process no session
// took: as started by the loop when it carries the loop's titles. It returns
// the sessions it registered.
func (s *Service) backfill(ctx context.Context, o *sight, known []turn.Session) ([]turn.Session, error) {
	var workspaces []string
	for _, a := range o.agents {
		if a.WorkspaceID != "" && !slices.Contains(workspaces, a.WorkspaceID) {
			workspaces = append(workspaces, a.WorkspaceID)
		}
	}
	isKnown := make(map[string]bool, len(known))
	for _, k := range known {
		isKnown[k.ID] = true
	}
	titled := loopTitle(s.Intents.SessionTitles())
	var added []turn.Session
	for _, id := range workspaces {
		terms, ok := s.terminalsOf(ctx, o, id)
		if !ok {
			continue
		}
		for _, t := range terms {
			if t.Agent == "" || isKnown[t.Session] || (t.Session != "" || t.Handle == "") && !titled.MatchString(t.Title) {
				continue
			}
			sess, registered, err := s.register(ctx, id, t, titled.MatchString(t.Title))
			if err != nil {
				return added, err
			}
			if registered {
				added = append(added, sess)
			}
		}
	}
	return added, nil
}

func (s *Service) register(ctx context.Context, workspaceID string, t Terminal, titled bool) (turn.Session, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.Session == "" {
		id, err := s.resolveLocked(ctx, t.Manager, t.Handle, turn.Session{WorkspaceID: workspaceID, Managed: titled})
		if err != nil {
			return turn.Session{}, false, err
		}
		sess, _, err := s.Registry.Session(ctx, id)
		return sess, err == nil, err
	}
	_, known, err := s.Registry.Session(ctx, t.Session)
	if err != nil || known {
		return turn.Session{}, false, err
	}
	ev := s.event(unit.Unit{}, KindSessionLaunched, t.Session, "registered from its title "+t.Title)
	sess := turn.Session{ID: t.Session, Manager: t.Manager, Handle: t.Handle, WorkspaceID: workspaceID, Managed: true}
	if err := s.Registry.SaveSession(ctx, sess, ev); err != nil {
		return turn.Session{}, false, err
	}
	s.publish(ev)
	return sess, true, nil
}

// look reads what cleanup's rules need about the listed session, bar its
// book, and keeps its idle period: it starts when the agent entered the done
// state and ends when the agent leaves the done state or enters it again. ok
// is false when the session sits in a workspace with no agent or its
// workspace's terminals could not be read.
func (s *Service) look(ctx context.Context, o *sight, cur turn.Session) (sessionFacts, bool, error) {
	if !slices.ContainsFunc(o.agents, func(a AgentObservation) bool { return cur.WorkspaceID != "" && a.WorkspaceID == cur.WorkspaceID }) {
		_, err := s.keepIdle(ctx, cur, AgentObservation{}, false)
		return sessionFacts{}, false, err
	}
	terms, read := s.terminalsOf(ctx, o, cur.WorkspaceID)
	if !read {
		return sessionFacts{}, false, nil
	}
	f := sessionFacts{Session: cur}
	var term Terminal
	if i := slices.IndexFunc(terms, func(t Terminal) bool { return t.Session == cur.ID }); i >= 0 {
		f.Listed, term = true, terms[i]
		f.Agent, f.HasAgent = agentOf(o.agents, cur, term)
		f.UserActive = slices.ContainsFunc(o.agents, func(a AgentObservation) bool { return a.UserActive && a.WorkspaceID == cur.WorkspaceID })
	}
	var err error
	if f.Session, err = s.keepIdle(ctx, cur, f.Agent, f.HasAgent); err != nil {
		return sessionFacts{}, false, err
	}
	return f, true, nil
}

func agentOf(agents []AgentObservation, sess turn.Session, term Terminal) (AgentObservation, bool) {
	i := slices.IndexFunc(agents, func(a AgentObservation) bool {
		if sess.Pane != "" {
			return a.Pane == sess.Pane
		}
		return term.Key != "" && a.TerminalKey == term.Key && a.WorkspaceID == sess.WorkspaceID
	})
	if i < 0 {
		return AgentObservation{}, false
	}
	return agents[i], true
}

// keepIdle moves the listed session's idle period to what the agent says. It
// writes only on a change, then decides again on the stored row, which may
// have moved since it was listed.
func (s *Service) keepIdle(ctx context.Context, listed turn.Session, agent AgentObservation, has bool) (turn.Session, error) {
	done := has && agent.State == turn.AgentDone
	if _, _, changed := s.idleStep(listed, agent, done); !changed {
		return listed, nil
	}
	s.mu.Lock()
	cur, ok, err := s.Registry.Session(ctx, listed.ID)
	if err != nil || !ok || !cur.Managed || !cur.ClosedAt.IsZero() {
		s.mu.Unlock()
		return listed, err
	}
	next, ev, changed := s.idleStep(cur, agent, done)
	if !changed {
		s.mu.Unlock()
		return cur, nil
	}
	err = s.Registry.SaveSession(ctx, next, ev)
	s.mu.Unlock()
	if err != nil {
		return cur, err
	}
	s.publish(ev)
	return next, nil
}

func (s *Service) idleStep(cur turn.Session, agent AgentObservation, done bool) (turn.Session, port.Event, bool) {
	switch since := cur.IdleSince; {
	case done && since.IsZero():
		cur.IdleSince = s.now()
		if !agent.StateSince.IsZero() {
			cur.IdleSince = agent.StateSince
		}
		return cur, s.event(unit.Unit{}, KindSessionIdle, cur.ID+":"+cur.IdleSince.Format(time.RFC3339Nano), ""), true
	case !since.IsZero() && (!done || agent.StateSince.After(since)):
		cur.IdleSince = time.Time{}
		return cur, s.event(unit.Unit{}, KindSessionActive, cur.ID+":"+since.Format(time.RFC3339Nano), ""), true
	}
	return cur, port.Event{}, false
}

// Resumer is an agent's resume command, as a session_closed line names it.
type Resumer interface {
	Resume(home, agentSession string) string
}

// ResumeWith joins a session_closed event's reason and its resume line.
const ResumeWith = "; resume with: "

// resumeLine is "cd <worktree> && " plus r's line.
func resumeLine(sess turn.Session, r Resumer) string {
	line := r.Resume(sess.Home, sess.AgentSession)
	if sess.Worktree == "" {
		return line
	}
	return "cd " + shell.Quote(sess.Worktree) + " && " + line
}

func (s *Service) closeDetail(f sessionFacts, reason string) string {
	if s.AgentFor == nil {
		return reason
	}
	r := s.AgentFor(f.Unit)
	if r == nil {
		return reason
	}
	return reason + ResumeWith + resumeLine(f.Session, r)
}
