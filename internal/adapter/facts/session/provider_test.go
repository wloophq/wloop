package factsession_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	factsession "github.com/wloophq/wloop/internal/adapter/facts/session"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
)

var t0 = time.Date(2026, 10, 20, 18, 20, 0, 0, time.UTC)

const (
	ws1      = "repo::/wt/acme-api-7"
	wsRemote = "repo::ssh-box/wt"
)

type snapshot struct{ s wsdomain.Snapshot }

func (f *snapshot) Read(context.Context, wsdomain.Part) (*wsdomain.Snapshot, error) {
	s := f.s
	return &s, nil
}

type prStates map[unit.Unit]dinv.PR

func (p prStates) PRs(_ context.Context, us []unit.Unit) (map[unit.Unit]dinv.PR, error) {
	out := map[unit.Unit]dinv.PR{}
	for _, u := range us {
		if got, ok := p[u]; ok {
			out[u] = got
		}
	}
	return out, nil
}

type world struct {
	reg     *turnstest.Registry
	store   *turnstest.Store
	sess    *turnstest.Sessions
	prompts *turnstest.Prompts
	snap    *snapshot
	prs     prStates
	missing map[string]bool
	clock   *automationtest.Clock
	svc     *turns.Service
	p       *factsession.Provider
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{reg: turnstest.NewRegistry(), store: turnstest.NewStore(), sess: turnstest.NewSessions(), prompts: turnstest.NewPrompts(),
		snap: &snapshot{}, prs: prStates{}, missing: map[string]bool{}, clock: automationtest.NewClock(t0)}
	w.snap.s.Workspaces = []wsdomain.Workspace{{ID: ws1, Path: "/wt/acme-api-7", HostID: wsdomain.LocalHost},
		{ID: wsRemote, Path: "/home/remote/wt", HostID: "ssh-box"}}
	w.snap.s.TerminalScope.Complete = true
	w.snap.s.WorkspaceScope.Complete = true
	w.svc = &turns.Service{Intents: turnstest.Intents(), Store: w.store, Registry: w.reg, Sessions: w.sess, Prompts: w.prompts,
		Clock: w.clock, Command: brand.Name, FastJob: time.Minute}
	w.p = factsession.New(factsession.Deps{Turns: w.svc, Snapshot: w.snap, PRs: w.prs, Exists: func(p string) bool { return !w.missing[p] },
		Cfg: config.Defaults, Clock: w.clock})
	return w
}

// wsOf is the workspace of a registered handle, ws1 for any other.
func (w *world) wsOf(handle string) string {
	if s, ok, _ := w.reg.Session(context.Background(), handle); ok && s.WorkspaceID != "" {
		return s.WorkspaceID
	}
	return ws1
}

// terminal lists an agent terminal; an unusable one is orphaned. A handle no
// session took lists with no session, as the router lists it.
func (w *world) terminal(handle, title, agent string, usable bool) {
	ws := w.wsOf(handle)
	w.snap.s.Terminals = append(w.snap.s.Terminals, wsdomain.Terminal{Manager: turnstest.TestManager, Handle: handle, WorkspaceID: ws, TabID: "tab-" + handle, Title: title, Agent: agent, Usable: usable})
	if usable {
		session := handle
		if _, taken, _ := w.reg.SessionOf(context.Background(), turnstest.TestManager, handle); !taken {
			session = ""
		}
		terms, _ := w.sess.Terminals(context.Background(), ws)
		w.sess.Put(ws, append(terms, turns.Terminal{Session: session, Manager: turnstest.TestManager, Handle: handle, Key: "tab-" + handle, Title: title, Agent: agent})...)
	}
}

func (w *world) agent(handle string, st turn.AgentState, since time.Time, focused bool) {
	agents, _ := w.sess.Agents(context.Background())
	w.sess.SetAgents(append(agents, turns.AgentObservation{WorkspaceID: w.wsOf(handle), Pane: "tab-" + handle + ":leaf-1", TerminalKey: "tab-" + handle, State: st, StateSince: since, UserActive: focused})...)
}

// session registers a managed session with a live, done agent.
func (w *world) session(handle string, edit func(*turn.Session)) turn.Session {
	s := turn.Session{ID: handle, Pane: "tab-" + handle + ":leaf-1", WorkspaceID: ws1, Worktree: "/wt/acme-api-7", Managed: true, AgentSession: "agent-" + handle, StartedAt: t0.Add(-5 * time.Hour)}
	if edit != nil {
		edit(&s)
	}
	w.reg.Put(s)
	w.terminal(handle, "review 20/10 10:00", "claude", true)
	return s
}

func sessionUnit(h string) unit.Unit { return unit.Unit{Provider: unit.Session, Key: h} }

func (w *world) facts(t *testing.T, h string) workflow.Facts {
	t.Helper()
	f, err := w.p.Facts(context.Background(), sessionUnit(h))
	if err != nil {
		t.Fatalf("Facts(%s): %v", h, err)
	}
	return f
}

func prUnit(n int) unit.Unit { return pr.Ref{Owner: "acme", Repo: "api", Number: n}.Unit() }

func (w *world) book(u unit.Unit, ts ...turn.Turn) {
	w.store.Put(turn.Book{Unit: u, Turns: ts})
}

func TestSessionUnitsContract(t *testing.T) {
	automationtest.RunUnits(t, unit.Session, func(t *testing.T) automation.Units {
		w := newWorld(t)
		w.session("term_1", func(s *turn.Session) { s.IdleSince = t0.Add(-time.Hour) })
		w.agent("term_1", turn.AgentDone, t0.Add(-time.Hour), false)
		w.terminal("term_9", "claude", "claude", false)
		return w.p
	})
}

func TestRefreshKeepsTheIdlePeriod(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	w.session("term_1", nil)
	w.agent("term_1", turn.AgentDone, t0.Add(-3*time.Hour), false)

	if f := w.facts(t, "term_1"); f["session.idle_for"] != time.Duration(0) {
		t.Fatalf("before Refresh idle_for = %v, want 0: facts keep no idle period", f["session.idle_for"])
	}
	if evs := w.reg.Events(); len(evs) != 0 {
		t.Fatalf("Facts wrote %v", evs)
	}
	if err := w.p.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	got, _, _ := w.reg.Session(ctx, "term_1")
	if !got.IdleSince.Equal(t0.Add(-3 * time.Hour)) {
		t.Fatalf("stored idle_since = %v, want the agent's done time", got.IdleSince)
	}
	f := w.facts(t, "term_1")
	if at, _ := f["session.idle_since"].(time.Time); !at.Equal(t0.Add(-3 * time.Hour)) {
		t.Errorf("idle_since = %v", f["session.idle_since"])
	}
	if f["session.idle_for"] != 3*time.Hour {
		t.Errorf("idle_for = %v, want 3h", f["session.idle_for"])
	}

	w.sess.SetAgents(turns.AgentObservation{WorkspaceID: ws1, Pane: "tab-term_1:leaf-1", TerminalKey: "tab-term_1", State: turn.AgentWorking, StateSince: t0})
	if err := w.p.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if f := w.facts(t, "term_1"); f["session.idle_for"] != time.Duration(0) || f["session.agent_state"] != "working" {
		t.Errorf("a working agent: idle_for = %v, agent_state = %v", f["session.idle_for"], f["session.agent_state"])
	}
}

func TestRefreshTakesTheLoopsTitledTerminalAsAManagedSession(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	w.terminal("term_2", "review 20/10 10:00", "claude", true)
	w.agent("term_2", turn.AgentDone, t0.Add(-time.Hour), false)
	if got, _ := w.p.List(ctx, unit.Session); len(got) != 0 {
		t.Fatalf("before Refresh a process no session took is listed: %v", got)
	}
	if err := w.p.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	id, ok, _ := w.reg.SessionOf(ctx, turnstest.TestManager, "term_2")
	if !ok {
		t.Fatal("Refresh took no session for the titled terminal")
	}
	if f := w.facts(t, id); f["session.managed"] != true || f["session.state"] != "active" {
		t.Errorf("after Refresh managed = %v, state = %v", f["session.managed"], f["session.state"])
	}
}

func TestADuplicateNeverMarksTheFocusedOne(t *testing.T) {
	for _, tc := range []struct {
		name       string
		focusNewer bool
		promptNew  bool
		want       map[string]bool
	}{
		{name: "the newer is the extra one", want: map[string]bool{"term_old": false, "term_new": true}},
		{name: "the focused newer stays", focusNewer: true, want: map[string]bool{"term_old": true, "term_new": false}},
		{name: "the user-prompted newer stays", promptNew: true, want: map[string]bool{"term_old": true, "term_new": false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			w.session("term_old", func(s *turn.Session) { s.AgentSession = "shared" })
			w.session("term_new", func(s *turn.Session) {
				s.AgentSession, s.StartedAt = "shared", t0.Add(-time.Hour)
				if tc.focusNewer {
					s.WorkspaceID = "repo::/wt/acme-api-7-copy"
				}
				if tc.promptNew {
					s.PromptSeenAt, s.UserPromptAt = t0.Add(-time.Minute), t0.Add(-time.Minute)
				}
			})
			w.session("term_other", nil)
			w.agent("term_old", turn.AgentDone, t0.Add(-2*time.Hour), false)
			w.agent("term_new", turn.AgentDone, t0.Add(-2*time.Hour), tc.focusNewer)
			w.agent("term_other", turn.AgentDone, t0.Add(-2*time.Hour), false)
			for h, want := range tc.want {
				if got := w.facts(t, h)["session.duplicate"]; got != want {
					t.Errorf("%s duplicate = %v, want %v", h, got, want)
				}
			}
			if got := w.facts(t, "term_other")["session.duplicate"]; got != false {
				t.Errorf("a session alone is duplicate = %v", got)
			}
		})
	}
}

func TestADuplicateNeedsBothListed(t *testing.T) {
	w := newWorld(t)
	w.session("term_old", func(s *turn.Session) { s.AgentSession = "shared" })
	w.reg.Put(turn.Session{ID: "term_new", WorkspaceID: ws1, Managed: true, AgentSession: "shared", StartedAt: t0.Add(-time.Hour)})
	if got := w.facts(t, "term_new")["session.duplicate"]; got != false {
		t.Errorf("an unlisted session is duplicate = %v", got)
	}
}

func TestRefreshTakesAnyAgentProcessAsAManualSession(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	w.session("term_1", nil)
	w.reg.Put(turn.Session{ID: "term_orphan", WorkspaceID: ws1})
	w.terminal("term_orphan", "claude", "claude", false)
	w.terminal("term_live", "claude", "claude", true)
	w.terminal("term_shell", "zsh", "", true)
	w.agent("term_live", turn.AgentDone, t0.Add(-time.Hour), false)
	w.reg.Put(turn.Session{ID: "term_closed", Manager: turnstest.TestManager, Handle: "term_closed", WorkspaceID: ws1, ClosedAt: t0.Add(-time.Hour)})
	w.terminal("term_closed", "claude", "claude", true)
	if err := w.p.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	live, ok, _ := w.reg.SessionOf(ctx, turnstest.TestManager, "term_live")
	if !ok {
		t.Fatal("the live agent process is no session")
	}
	got, err := w.p.List(ctx, unit.Session)
	if err != nil {
		t.Fatal(err)
	}
	want := []unit.Unit{sessionUnit("term_1"), sessionUnit("term_orphan"), sessionUnit(live)}
	slices.SortFunc(want, func(a, b unit.Unit) int { return strings.Compare(a.Key, b.Key) })
	if !slices.Equal(got, want) {
		t.Fatalf("List = %v, want %v", got, want)
	}
	o := w.facts(t, "term_orphan")
	if o["session.managed"] != false || o["session.orphaned"] != true || o["session.listed"] != false {
		t.Errorf("orphan: managed %v, orphaned %v, listed %v", o["session.managed"], o["session.orphaned"], o["session.listed"])
	}
	l := w.facts(t, live)
	if l["session.managed"] != false || l["session.orphaned"] != false || l["session.listed"] != true {
		t.Errorf("taken live: managed %v, orphaned %v, listed %v", l["session.managed"], l["session.orphaned"], l["session.listed"])
	}
	if m := w.facts(t, "term_1"); m["session.managed"] != true || m["session.orphaned"] != false {
		t.Errorf("registered: managed %v, orphaned %v", m["session.managed"], m["session.orphaned"])
	}
	if _, shell, _ := w.reg.SessionOf(ctx, turnstest.TestManager, "term_shell"); shell {
		t.Error("a shell became a session")
	}
	for _, h := range []string{"term_closed", "term_none"} {
		if _, err := w.p.Facts(ctx, sessionUnit(h)); !errors.Is(err, automation.ErrUnitGone) {
			t.Errorf("Facts(%s) = %v, want ErrUnitGone", h, err)
		}
	}
}

func TestAManualSessionIdlesFromItsAgent(t *testing.T) {
	w := newWorld(t)
	w.session("term_m", func(s *turn.Session) { s.Managed = false })
	w.agent("term_m", turn.AgentDone, t0.Add(-30*time.Hour), false)
	f := w.facts(t, "term_m")
	if f["session.idle_for"] != 30*time.Hour || f["session.agent_state"] != "done" {
		t.Errorf("manual: idle_for %v, agent_state %v", f["session.idle_for"], f["session.agent_state"])
	}
}

func TestWorktreeGone(t *testing.T) {
	w := newWorld(t)
	w.session("term_1", nil)
	w.session("term_2", func(s *turn.Session) { s.Worktree = "" })
	w.session("term_3", func(s *turn.Session) { s.Worktree, s.WorkspaceID = "", "elsewhere" })
	w.session("term_4", func(s *turn.Session) { s.Worktree, s.WorkspaceID = "", wsRemote })
	w.session("term_5", func(s *turn.Session) { s.Worktree, s.WorkspaceID = "/home/remote/wt", wsRemote })
	w.session("term_6", func(s *turn.Session) { s.Worktree, s.WorkspaceID = "wt/relative", "elsewhere" })
	w.missing["/home/remote/wt"], w.missing["wt/relative"] = true, true
	if w.facts(t, "term_1")["session.worktree_gone"] != false {
		t.Error("an existing worktree is gone")
	}
	w.missing["/wt/acme-api-7"] = true
	if w.facts(t, "term_1")["session.worktree_gone"] != true {
		t.Error("a missing worktree is not gone")
	}
	if w.facts(t, "term_2")["session.worktree_gone"] != true {
		t.Error("a session with no worktree does not read its workspace's path")
	}
	if w.facts(t, "term_3")["session.worktree_gone"] != false {
		t.Error("a session with no known path is gone")
	}
	for _, h := range []string{"term_4", "term_5"} {
		if w.facts(t, h)["session.worktree_gone"] != false {
			t.Errorf("%s: a workspace on another host is gone", h)
		}
	}
	if w.facts(t, "term_6")["session.worktree_gone"] != false {
		t.Error("a relative worktree of an unlisted workspace is gone")
	}
	w.missing["/wt/gone"] = true
	w.session("term_7", func(s *turn.Session) { s.Worktree, s.WorkspaceID = "/wt/gone", "elsewhere" })
	if w.facts(t, "term_7")["session.worktree_gone"] != true {
		t.Error("an absolute missing worktree of an unlisted workspace is not gone")
	}
	w.snap.s.WorkspaceScope.Complete = false
	w.snap.s.Gen++
	if w.facts(t, "term_7")["session.worktree_gone"] != false {
		t.Error("an incomplete workspace listing let an unplaced worktree be stat'ed")
	}
	if w.facts(t, "term_1")["session.worktree_gone"] != true {
		t.Error("a listed local workspace stops being checked when the listing is incomplete")
	}
}

func TestGoneFollowsTwoCompleteCensuses(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	w.reg.Put(turn.Session{ID: "term_lost", WorkspaceID: ws1, Managed: true, StartedAt: t0.Add(-time.Hour)})
	if err := w.p.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if w.facts(t, "term_lost")["session.process_gone"] != false {
		t.Fatal("gone after one census")
	}
	if due := w.p.Due(); !due.Equal(t0.Add(time.Minute)) {
		t.Errorf("Due = %v, want the census mark plus FastJob", due)
	}
	w.clock.Advance(time.Minute)
	if err := w.p.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if w.facts(t, "term_lost")["session.process_gone"] != true {
		t.Fatal("not gone after two censuses FastJob apart")
	}
	if got, _, _ := w.reg.Session(ctx, "term_lost"); got.State() != turn.SessionActive {
		t.Error("Refresh moved the session; hygiene-session-dormant does that")
	}

	w.clock.Advance(10 * time.Second)
	w.reg.Put(turn.Session{ID: "term_lost2", WorkspaceID: ws1, Managed: true, StartedAt: t0.Add(-time.Hour)})
	if err := w.p.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if due, want := w.p.Due(), t0.Add(time.Minute+10*time.Second+time.Minute); !due.Equal(want) {
		t.Errorf("with one session gone, Due = %v, want the next mark's %v", due, want)
	}
}

func TestASessionOnlyTheRegistryHoldsIsTheTruth(t *testing.T) {
	w := newWorld(t)
	w.session("term_1", nil)
	w.snap.s.TerminalScope.Complete = false
	if _, err := w.p.Facts(context.Background(), sessionUnit("term_unseen")); !errors.Is(err, automation.ErrUnitGone) {
		t.Errorf("Facts of a session no registry holds = %v, want ErrUnitGone", err)
	}
	if f := w.facts(t, "term_1"); f["session.state"] != "active" {
		t.Errorf("an incomplete listing moved a session's state: %v", f["session.state"])
	}
	w.reg.Put(turn.Session{ID: "term_closed", WorkspaceID: ws1, ClosedAt: t0})
	if _, err := w.p.Facts(context.Background(), sessionUnit("term_closed")); !errors.Is(err, automation.ErrUnitGone) {
		t.Errorf("a closed session = %v, want ErrUnitGone", err)
	}
}

type countingStore struct {
	*turnstest.Store
	books int
}

func (c *countingStore) Books(ctx context.Context, us ...unit.Unit) (map[unit.Unit]turn.Book, error) {
	c.books++
	return c.Store.Books(ctx, us...)
}

func TestOneEvaluationReadsTheWorldOnce(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	store := &countingStore{Store: w.store}
	w.svc.Store = store
	log := change.NewLog(64, nil)
	w.p = factsession.New(factsession.Deps{Turns: w.svc, Snapshot: w.snap, PRs: w.prs, Changes: log, Exists: func(string) bool { return true },
		Cfg: config.Defaults, Clock: w.clock})
	for i, h := range []string{"term_1", "term_2", "term_3"} {
		w.session(h, nil)
		w.book(prUnit(i+1), turn.Turn{Intent: turn.Intent(dintent.Review), ID: "r" + h, State: turn.Done, Session: h, SentTo: h, SentAt: t0.Add(-time.Hour), StartedAt: t0.Add(-time.Hour), EndedAt: t0})
	}
	us, err := w.p.List(ctx, unit.Session)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range us {
		if _, err := w.p.Facts(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	if store.books != 1 {
		t.Errorf("List and %d Facts read the books %d times, want once", len(us), store.books)
	}
	log.Note(port.TopicSessions, sessionUnit("term_1"))
	if _, err := w.p.Facts(ctx, us[0]); err != nil {
		t.Fatal(err)
	}
	if store.books != 2 {
		t.Errorf("after a store note the books were read %d times in all, want 2", store.books)
	}
	w.snap.s.Gen++
	if _, err := w.p.Facts(ctx, us[0]); err != nil {
		t.Fatal(err)
	}
	if store.books != 3 {
		t.Errorf("after a new snapshot the books were read %d times in all, want 3", store.books)
	}
}

func TestPRStateJoinsTheInventory(t *testing.T) {
	w := newWorld(t)
	w.session("term_1", nil)
	w.session("term_2", nil)
	w.book(prUnit(7), turn.Turn{Intent: turn.Intent(dintent.Review), ID: "r1", State: turn.Done, Session: "term_1", SentTo: "term_1", SentAt: t0.Add(-4 * time.Hour), StartedAt: t0.Add(-4 * time.Hour), EndedAt: t0.Add(-3 * time.Hour)})
	w.prs[prUnit(7)] = dinv.PR{State: "MERGED"}
	f := w.facts(t, "term_1")
	if f["session.pr_state"] != "merged" || f["session.repo"] != "acme/api" {
		t.Errorf("pr_state %v, repo %v", f["session.pr_state"], f["session.repo"])
	}
	if f["session.newest_state"] != "done" || f["session.newest_intent"] != "review" || f["session.newest_age"] != 4*time.Hour {
		t.Errorf("newest: %v %v %v", f["session.newest_state"], f["session.newest_intent"], f["session.newest_age"])
	}
	g := w.facts(t, "term_2")
	if g["session.pr_state"] != "" || g["session.repo"] != "" || g["session.newest_state"] != "" {
		t.Errorf("a session with no book: pr_state %v, repo %v, newest_state %v", g["session.pr_state"], g["session.repo"], g["session.newest_state"])
	}
	if _, ok := g["session.newest_age"]; ok {
		t.Error("newest_age set with no turn")
	}
}

func TestOpenTurnAndClosesWhenDone(t *testing.T) {
	w := newWorld(t)
	w.session("term_1", nil)
	w.session("term_2", nil)
	w.book(prUnit(7), turn.Turn{Intent: turn.Intent(dintent.Review), ID: "r1", State: turn.Asked, Session: "term_1", SentTo: "term_1", SentAt: t0.Add(-time.Hour), StartedAt: t0.Add(-time.Hour)})
	w.book(prUnit(8), turn.Turn{Intent: turn.Intent(dintent.Nudge), ID: "n1", State: turn.Done, Session: "term_2", SentTo: "term_2", SentAt: t0.Add(-time.Hour), StartedAt: t0.Add(-time.Hour), EndedAt: t0})
	if f := w.facts(t, "term_1"); f["session.open_turn"] != "asked" || f["session.closes_when_done"] != false {
		t.Errorf("asked review: open_turn %v, closes_when_done %v", f["session.open_turn"], f["session.closes_when_done"])
	}
	if f := w.facts(t, "term_2"); f["session.open_turn"] != "" || f["session.closes_when_done"] != true {
		t.Errorf("done nudge: open_turn %v, closes_when_done %v", f["session.open_turn"], f["session.closes_when_done"])
	}
}

func TestPromptFacts(t *testing.T) {
	type want struct{ knowable, typed, idle bool }
	for _, tc := range []struct {
		name  string
		edit  func(*turn.Session)
		trans *time.Time
		want  want
	}{
		{name: "the hook saw no prompt of the user", edit: func(s *turn.Session) { s.PromptSeenAt = t0.Add(-time.Minute) }, want: want{knowable: true}},
		{name: "the hook saw the user type after the turn", edit: func(s *turn.Session) {
			s.PromptSeenAt, s.UserPromptAt = t0.Add(-time.Minute), t0.Add(-time.Minute)
		}, want: want{knowable: true, typed: true, idle: true}},
		{name: "the user typed before the turn", edit: func(s *turn.Session) {
			s.PromptSeenAt, s.UserPromptAt, s.TypedAt = t0.Add(-time.Minute), t0.Add(-4*time.Hour), t0.Add(-3*time.Hour)
		}, want: want{knowable: true}},
		{name: "no hook and no transcript", want: want{}},
		{name: "the transcript tells", trans: ptr(t0.Add(-time.Minute)), want: want{knowable: true, typed: true, idle: true}},
		{name: "a transcript with no prompt", trans: ptr(time.Time{}), want: want{knowable: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			w.session("term_1", tc.edit)
			w.agent("term_1", turn.AgentDone, t0.Add(-2*time.Hour), false)
			if tc.trans != nil {
				w.prompts.Put("term_1", *tc.trans)
			}
			f := w.facts(t, "term_1")
			got := want{f["session.prompt_knowable"] == true, f["session.user_typed_after_turn"] == true, f["session.prompted_while_idle"] == true}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestAManualSessionIsNeverTypedAfterTurn(t *testing.T) {
	w := newWorld(t)
	w.session("term_m", func(s *turn.Session) {
		s.Managed, s.PromptSeenAt, s.UserPromptAt = false, t0.Add(-time.Minute), t0.Add(-time.Minute)
	})
	w.agent("term_m", turn.AgentDone, t0.Add(-2*time.Hour), false)
	f := w.facts(t, "term_m")
	if f["session.user_typed_after_turn"] != false || f["session.prompted_while_idle"] != true {
		t.Errorf("manual: typed %v, prompted_while_idle %v", f["session.user_typed_after_turn"], f["session.prompted_while_idle"])
	}
}

func TestTheFingerprintIgnoresIdleFor(t *testing.T) {
	w := newWorld(t)
	w.session("term_1", func(s *turn.Session) { s.IdleSince = t0.Add(-time.Hour) })
	w.agent("term_1", turn.AgentDone, t0.Add(-time.Hour), false)
	w.book(prUnit(7), turn.Turn{Intent: turn.Intent(dintent.Review), ID: "r1", State: turn.Done, Session: "term_1", SentTo: "term_1", SentAt: t0.Add(-2 * time.Hour), StartedAt: t0.Add(-2 * time.Hour), EndedAt: t0.Add(-time.Hour)})
	a := w.facts(t, "term_1")
	w.clock.Advance(time.Hour)
	b := w.facts(t, "term_1")
	if a["session.fingerprint"] != b["session.fingerprint"] {
		t.Errorf("fingerprint moved with the clock: %v then %v", a["session.fingerprint"], b["session.fingerprint"])
	}
	if a["session.idle_for"] == b["session.idle_for"] {
		t.Error("idle_for did not move")
	}
	if want := "term_1:active:" + t0.Add(-time.Hour).Format(time.RFC3339Nano) + ":1"; a["session.fingerprint"] != want {
		t.Errorf("fingerprint = %v, want %s", a["session.fingerprint"], want)
	}
}

func TestDueIsTheEarliestThresholdNotPassed(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	w.session("term_done", func(s *turn.Session) { s.IdleSince = t0.Add(-time.Hour) })
	w.session("term_asked", func(s *turn.Session) { s.IdleSince = t0.Add(-time.Hour) })
	w.session("term_past", func(s *turn.Session) { s.IdleSince = t0.Add(-3 * time.Hour) })
	w.session("term_m", func(s *turn.Session) { s.Managed = false })
	for _, h := range []string{"term_done", "term_asked", "term_past"} {
		w.agent(h, turn.AgentDone, t0.Add(-3*time.Hour), false)
	}
	w.agent("term_m", turn.AgentDone, t0.Add(-time.Hour), false)
	ended := func(h string, st turn.State) turn.Turn {
		return turn.Turn{Intent: turn.Intent(dintent.Review), ID: "r-" + h, State: st, Session: h, SentTo: h, SentAt: t0.Add(-4 * time.Hour), StartedAt: t0.Add(-4 * time.Hour), EndedAt: t0.Add(-3 * time.Hour)}
	}
	w.book(prUnit(1), ended("term_done", turn.Done))
	w.book(prUnit(2), ended("term_asked", turn.Asked))
	w.book(prUnit(3), ended("term_past", turn.Settled))

	if _, err := w.p.List(ctx, unit.Session); err != nil {
		t.Fatal(err)
	}
	if due := w.p.Due(); !due.Equal(t0.Add(time.Hour)) {
		t.Errorf("Due = %v, want idle_since + cleanup.idle of term_done", due)
	}
}

func ptr(t time.Time) *time.Time { return &t }
