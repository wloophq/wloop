package turns_test

import (
	"cmp"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/domain/turn"
)

const cleanupWS = "repo::/wt/acme-api-7"

type cleanupCase struct {
	sess       turn.Session
	unknown    bool
	title      string
	state      turn.AgentState
	userActive bool
	lastOutput time.Time
	stateSince time.Time
	turns      []turn.Turn
}

func loopSession(handle string) turn.Session {
	return turn.Session{ID: handle, Manager: "orca", Handle: handle, Pane: "tab-" + handle + ":leaf-1", WorkspaceID: cleanupWS, Worktree: "/wt/acme api", Managed: true, AgentSession: "sess-" + handle}
}

func (c cleanupCase) put(reg *turnstest.Registry, store *turnstest.Store, sess *turnstest.Sessions) {
	h := c.sess.ID
	if !c.unknown {
		reg.Put(c.sess)
	}
	title := cmp.Or(c.title, "review 18/10 10:00")
	terms, _ := sess.Terminals(context.Background(), cleanupWS)
	sess.Put(cleanupWS, append(terms, turns.Terminal{Session: h, Key: "tab-" + h, Title: title, Agent: "claude", LastOutput: cmp.Or(c.lastOutput, t0.Add(-3*time.Hour))})...)
	agents, _ := sess.Agents(context.Background())
	sess.SetAgents(append(agents, turns.AgentObservation{WorkspaceID: cleanupWS, Pane: "tab-" + h + ":leaf-1", TerminalKey: "tab-" + h, State: cmp.Or(c.state, turn.AgentDone), StateSince: c.stateSince, UserActive: c.userActive})...)
	if len(c.turns) > 0 {
		store.Put(turn.Book{Unit: ref(len(h)), Turns: c.turns})
	}
}

func keep(t *testing.T, s *turns.Service) {
	t.Helper()
	if err := s.KeepSessions(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestKeepSessions(t *testing.T) {
	ctx := context.Background()
	idleFor := func(d time.Duration) turn.Session {
		s := loopSession("term_1")
		s.IdleSince = t0.Add(-d)
		return s
	}
	notStarted := idleFor(2 * time.Hour)
	notStarted.Managed = false

	t.Run("a terminal with a loop title is registered", func(t *testing.T) {
		for _, c := range []struct {
			title string
			want  bool
		}{{"triage 17/10 14:02", true}, {"nudge 17/10 14:02", true}, {"triage acme/api#7 17/10 14:02", true},
			{"probe 17/10 14:02", false}, {"claude", false}, {"triaged", false}, {"review", false}, {"review the plan", false}, {"nudge acme/api#7", false}} {
			s, store, reg, sess, _ := newService(t)
			cleanupCase{sess: turn.Session{ID: "term_1"}, unknown: true, title: c.title}.put(reg, store, sess)
			keep(t, s)
			cur, ok, _ := reg.Session(ctx, "term_1")
			if ok != c.want || ok && !cur.Managed {
				t.Errorf("%q: registered %v %+v, want %v", c.title, ok, cur, c.want)
			}
		}
	})

	t.Run("a later done transition clears IdleSince", func(t *testing.T) {
		s, store, reg, sess, _ := newService(t)
		cleanupCase{sess: idleFor(2 * time.Hour), stateSince: t0.Add(-30 * time.Minute)}.put(reg, store, sess)
		keep(t, s)
		cur, _, _ := reg.Session(ctx, "term_1")
		if !cur.IdleSince.IsZero() || len(kinds(reg.Events(), turns.KindSessionActive)) != 1 {
			t.Fatalf("session %+v, events %+v", cur, reg.Events())
		}
		keep(t, s)
		if cur, _, _ := reg.Session(ctx, "term_1"); !cur.IdleSince.Equal(t0.Add(-30 * time.Minute)) {
			t.Fatalf("the next run did not start a new idle period: %+v", cur)
		}
	})

	t.Run("an agent in an unknown state is not done", func(t *testing.T) {
		s, store, reg, sess, _ := newService(t)
		cleanupCase{sess: idleFor(2 * time.Hour), state: turn.ParseAgentState("idle")}.put(reg, store, sess)
		keep(t, s)
		if cur, _, _ := reg.Session(ctx, "term_1"); !cur.IdleSince.IsZero() {
			t.Fatalf("session %+v", cur)
		}
	})

	t.Run("a working agent clears IdleSince", func(t *testing.T) {
		s, store, reg, sess, _ := newService(t)
		cleanupCase{sess: idleFor(2 * time.Hour), state: turn.AgentWorking}.put(reg, store, sess)
		keep(t, s)
		if cur, _, _ := reg.Session(ctx, "term_1"); !cur.IdleSince.IsZero() {
			t.Fatalf("session %+v", cur)
		}
	})

	t.Run("a session in a workspace with no agent reads no terminals", func(t *testing.T) {
		s, _, reg, _, _ := newService(t)
		gone := idleFor(2 * time.Hour)
		gone.WorkspaceID = "repo::/wt/deleted"
		reg.Put(gone)
		counted := &countingSessions{Sessions: turnstest.NewSessions()}
		s.Sessions = counted
		keep(t, s)
		if counted.reads != 0 {
			t.Fatalf("%d terminal reads", counted.reads)
		}
		if cur, _, _ := reg.Session(ctx, "term_1"); !cur.IdleSince.IsZero() {
			t.Fatalf("session %+v", cur)
		}
	})

	t.Run("a session of another's is not tracked", func(t *testing.T) {
		s, store, reg, sess, _ := newService(t)
		cleanupCase{sess: notStarted}.put(reg, store, sess)
		keep(t, s)
		if cur, _, _ := reg.Session(ctx, "term_1"); !cur.IdleSince.Equal(notStarted.IdleSince) || len(reg.Events()) != 0 {
			t.Fatalf("session %+v, events %+v", cur, reg.Events())
		}
	})

	t.Run("unreadable terminals leave the session untouched", func(t *testing.T) {
		s, store, reg, sess, _ := newService(t)
		cleanupCase{sess: idleFor(2 * time.Hour)}.put(reg, store, sess)
		s.Sessions = &countingSessions{Sessions: sess, fail: true}
		keep(t, s)
		if cur, _, _ := reg.Session(ctx, "term_1"); !cur.IdleSince.Equal(t0.Add(-2*time.Hour)) || len(reg.Events()) != 0 {
			t.Fatalf("session %+v, events %+v", cur, reg.Events())
		}
	})
}

type countingSessions struct {
	*turnstest.Sessions
	fail  bool
	reads int
}

func (c *countingSessions) Terminals(ctx context.Context, workspaceID string) ([]turns.Terminal, error) {
	c.reads++
	if c.fail {
		return nil, errors.New("orca is down")
	}
	return c.Sessions.Terminals(ctx, workspaceID)
}

type homeResumer struct{}

func (homeResumer) Resume(home, agentSession string) string {
	return "agent --home " + home + " --resume " + agentSession
}

func paneless(handle string) turn.Session {
	return turn.Session{ID: handle, WorkspaceID: cleanupWS, Managed: true, IdleSince: t0.Add(-2 * time.Hour)}
}

// An agent matched to the session keeps its idle period; an unmatched one
// leaves the session with no done agent, which ends it.
func TestKeepSessionsMatchesAnAgentByTerminalKey(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name   string
		key    string
		agents []turns.AgentObservation
		want   bool
	}{
		{name: "the same key in its workspace", key: "tab-1", want: true,
			agents: []turns.AgentObservation{{WorkspaceID: cleanupWS, Pane: "tab-1:leaf-1", TerminalKey: "tab-1", State: turn.AgentDone}}},
		{name: "a key not shaped like Orca's", key: "zed/42", want: true,
			agents: []turns.AgentObservation{{WorkspaceID: cleanupWS, Pane: "zed/42/pane", TerminalKey: "zed/42", State: turn.AgentDone}}},
		{name: "the same key in another workspace", key: "tab-1",
			agents: []turns.AgentObservation{
				{WorkspaceID: cleanupWS, Pane: "tab-2:leaf-1", TerminalKey: "tab-2", State: turn.AgentWorking},
				{WorkspaceID: "repo::/wt/other", Pane: "tab-1:leaf-1", TerminalKey: "tab-1", State: turn.AgentDone},
			}},
		{name: "an agent with no terminal key", key: "tab-1",
			agents: []turns.AgentObservation{{WorkspaceID: cleanupWS, Pane: "odd", State: turn.AgentDone}}},
		{name: "tab ten is not tab one", key: "tab-1",
			agents: []turns.AgentObservation{{WorkspaceID: cleanupWS, Pane: "tab-10:leaf-1", TerminalKey: "tab-10", State: turn.AgentDone}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, _, reg, sess, _ := newService(t)
			reg.Put(paneless("term_1"))
			sess.Put(cleanupWS, turns.Terminal{Session: "term_1", Key: c.key, Title: "review 18/10 10:00", Agent: "claude", LastOutput: t0.Add(-3 * time.Hour)})
			sess.SetAgents(c.agents...)
			keep(t, s)
			if cur, _, _ := reg.Session(ctx, "term_1"); cur.IdleSince.IsZero() == c.want {
				t.Fatalf("idle since %v, want matched %v", cur.IdleSince, c.want)
			}
		})
	}
}

type idleRig struct {
	s    *turns.Service
	reg  *turnstest.Registry
	sess *turnstest.Sessions
}

func newIdleRig(t *testing.T) idleRig {
	s, store, reg, sess, _ := newService(t)
	cleanupCase{sess: loopSession("term_1")}.put(reg, store, sess)
	return idleRig{s, reg, sess}
}

func (r idleRig) set(state turn.AgentState, since, lastOutput time.Time) {
	r.sess.Put(cleanupWS, turns.Terminal{Session: "term_1", Key: "tab-term_1", Title: "review 18/10 10:00", Agent: "claude", LastOutput: lastOutput})
	r.sess.SetAgents(turns.AgentObservation{WorkspaceID: cleanupWS, Pane: "tab-term_1:leaf-1", TerminalKey: "tab-term_1", State: state, StateSince: since})
}

func (r idleRig) keep(t *testing.T) {
	t.Helper()
	keep(t, r.s)
}

func TestIdleSurvivesRepaint(t *testing.T) {
	r := newIdleRig(t)
	since := t0.Add(-10 * time.Minute)
	for range 10 {
		r.set(turn.AgentDone, since, r.s.Clock.Now())
		r.keep(t)
		r.s.Clock.(*clock).advance(time.Minute)
	}
	if n := len(kinds(r.reg.Events(), turns.KindSessionIdle)); n != 1 {
		t.Fatalf("%d session_idle events", n)
	}
	if n := len(kinds(r.reg.Events(), turns.KindSessionActive)); n != 0 {
		t.Fatalf("%d session_active events", n)
	}
}

func TestIdleResetsOnMissedWorking(t *testing.T) {
	r := newIdleRig(t)
	first := t0.Add(-10 * time.Minute)
	r.set(turn.AgentDone, first, t0)
	r.keep(t)
	second := t0.Add(2 * time.Minute)
	r.s.Clock.(*clock).advance(3 * time.Minute)
	r.set(turn.AgentDone, second, t0)
	r.keep(t)
	if n := len(kinds(r.reg.Events(), turns.KindSessionActive)); n != 1 {
		t.Fatalf("%d session_active events", n)
	}
	r.keep(t)
	cur, _, _ := r.reg.Session(context.Background(), "term_1")
	if !cur.IdleSince.Equal(second) || len(kinds(r.reg.Events(), turns.KindSessionIdle)) != 2 {
		t.Fatalf("session %+v, events %+v", cur, r.reg.Events())
	}
}

func TestIdleStartsAtStateSince(t *testing.T) {
	r := newIdleRig(t)
	since := t0.Add(-90 * time.Minute)
	r.set(turn.AgentDone, since, t0)
	r.keep(t)
	cur, _, _ := r.reg.Session(context.Background(), "term_1")
	if !cur.IdleSince.Equal(since) {
		t.Fatalf("session %+v, events %+v", cur, r.reg.Events())
	}
}

func TestIdleFutureStateSinceDoesNotFlap(t *testing.T) {
	r := newIdleRig(t)
	since := t0.Add(5 * time.Minute)
	for range 10 {
		r.set(turn.AgentDone, since, r.s.Clock.Now())
		r.keep(t)
		r.s.Clock.(*clock).advance(30 * time.Second)
	}
	if n := len(kinds(r.reg.Events(), turns.KindSessionIdle)); n != 1 {
		t.Fatalf("%d session_idle events", n)
	}
	if n := len(kinds(r.reg.Events(), turns.KindSessionActive)); n != 0 {
		t.Fatalf("%d session_active events", n)
	}
}
