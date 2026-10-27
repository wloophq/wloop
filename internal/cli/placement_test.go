package cli

import (
	"cmp"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

var (
	r8       = github.Ref{Owner: "acme", Repo: "api", Number: 8}
	sharedWS = wsdomain.Workspace{ID: "ws-shared", RepoID: "r1", Path: "/wt/shared", DisplayName: "shared", HostID: "local"}
)

// sharedEnv is a workspace the context map shares between acme/api's pull
// requests, whose manager reports live the terminal that printed last.
func sharedEnv(t *testing.T, latest string) (*testEnv, *fake.Manager) {
	t.Helper()
	m := &fake.Manager{
		RepoList:      []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}},
		WorkspaceList: []wsdomain.Workspace{sharedWS},
		Sessions:      map[string]string{sharedWS.ID: latest},
		LastOutput:    map[string]time.Time{sharedWS.ID: testNow.Add(-time.Minute)},
	}
	te := newTestEnv(t, m)
	te.cfg.Placement = []config.PlacementRule{{Match: "github:acme/api#*", Repo: "acme/api", Role: "work", Shared: true}}
	return te, m
}

// registerSession saves s, live under the fake manager with its id as the
// handle unless it names its own or is closed.
func (te *testEnv) registerSession(t *testing.T, s dturn.Session) {
	t.Helper()
	if s.Handle == "" && s.ClosedAt.IsZero() {
		s.Manager, s.Handle = cmp.Or(s.Manager, "fake"), s.ID
	}
	if err := te.repo.SaveSession(context.Background(), s, port.Event{Kind: turns.KindSessionLaunched, Key: s.ID, At: testNow}); err != nil {
		t.Fatal(err)
	}
	if s.Handle != "" {
		if err := te.repo.BindHandle(context.Background(), s.ID, s.Manager, s.Handle, "", testNow); err != nil {
			t.Fatal(err)
		}
	}
}

func (te *testEnv) liveTerminals(ws string, ts ...turns.Terminal) {
	te.Turns.Sessions.(*turnstest.Sessions).Put(ws, ts...)
}

func TestSharedWorkspaceTypesIntoTheUnitsSession(t *testing.T) {
	t.Parallel()
	te, m := sharedEnv(t, "s2")
	te.registerSession(t, dturn.Session{ID: "s1", WorkspaceID: sharedWS.ID, Unit: r7.Unit(), StartedAt: testNow.Add(-time.Hour)})
	te.registerSession(t, dturn.Session{ID: "s2", WorkspaceID: sharedWS.ID, Unit: r8.Unit(), StartedAt: testNow.Add(-time.Minute)})
	te.liveTerminals(sharedWS.ID, turns.Terminal{Session: "s1", LastOutput: testNow.Add(-10 * time.Minute)}, turns.Terminal{Session: "s2", LastOutput: testNow.Add(-time.Minute)})
	ctx := context.Background()
	p, verb, err := te.session(ctx, te.cfg, sharedWS, r7.Unit(), roundOptions{keepLive: true})
	if err != nil || p.id != "s1" || p.handle != "s1" || verb != verbExisting {
		t.Fatalf("session = %+v %q %v", p, verb, err)
	}
	if p, _, err := te.session(ctx, te.cfg, sharedWS, r8.Unit(), roundOptions{keepLive: true}); err != nil || p.id != "s2" {
		t.Fatalf("B's session = %+v %v", p, err)
	}
	sent := sentTurn{Prompt: "go"}
	if _, err := te.deliver(ctx, r7.Unit(), &sent, p, false); err != nil {
		t.Fatal(err)
	}
	if len(m.Prompts) != 1 || m.Prompts[0].Handle != "s1" || strings.Join(m.Writes, "\n") != "send s1" {
		t.Fatalf("prompts %+v writes %q", m.Prompts, m.Writes)
	}
}

func TestClosedRegistrySessionIsSkipped(t *testing.T) {
	t.Parallel()
	te, m := sharedEnv(t, "s2")
	te.registerSession(t, dturn.Session{ID: "s1", WorkspaceID: sharedWS.ID, Unit: r7.Unit(), StartedAt: testNow.Add(-time.Hour)})
	te.registerSession(t, dturn.Session{ID: "s2", WorkspaceID: sharedWS.ID, Unit: r8.Unit(), StartedAt: testNow.Add(-time.Minute)})
	te.liveTerminals(sharedWS.ID, turns.Terminal{Session: "s2", LastOutput: testNow})
	p, verb, err := te.session(context.Background(), te.cfg, sharedWS, r7.Unit(), roundOptions{keepLive: true})
	if err != nil || p.id == "s1" || p.id == "s2" || verb != verbNew {
		t.Fatalf("session = %+v %q %v", p, verb, err)
	}
	if got := strings.Join(m.Writes, "\n"); got != "session ws-shared" {
		t.Fatalf("writes %q", got)
	}
}

func TestNewSessionCarriesItsUnit(t *testing.T) {
	t.Parallel()
	te, _ := sharedEnv(t, "s2")
	te.registerSession(t, dturn.Session{ID: "s2", WorkspaceID: sharedWS.ID, Unit: r8.Unit(), StartedAt: testNow.Add(-time.Minute)})
	te.liveTerminals(sharedWS.ID, turns.Terminal{Session: "s2", LastOutput: testNow})
	ctx := context.Background()
	p, verb, err := te.session(ctx, te.cfg, sharedWS, r7.Unit(), roundOptions{keepLive: true})
	if err != nil || verb != verbNew {
		t.Fatalf("session = %+v %q %v", p, verb, err)
	}
	got, ok, err := te.repo.Session(ctx, p.id)
	if err != nil || !ok || got.Unit != r7.Unit() || got.WorkspaceID != sharedWS.ID || !got.Managed || got.Handle != p.handle {
		t.Fatalf("registered %+v %v %v", got, ok, err)
	}
	te.liveTerminals(sharedWS.ID, turns.Terminal{Session: "s2", LastOutput: testNow}, turns.Terminal{Session: p.id, LastOutput: testNow})
	if again, verb, err := te.session(ctx, te.cfg, sharedWS, r7.Unit(), roundOptions{keepLive: true}); err != nil || again.id != p.id || verb != verbExisting {
		t.Fatalf("the next round: %+v %q %v", again, verb, err)
	}
}

func TestUserStartedSessionStillPickedInPRWorkspace(t *testing.T) {
	t.Parallel()
	m := warmReviewWorkspace()
	m.LastOutput["ws-rev"] = testNow.Add(-48 * time.Hour)
	te := newTestEnv(t, m)
	te.registerSession(t, dturn.Session{ID: "term-rev", WorkspaceID: "ws-rev"})
	ws := m.WorkspaceList[0]
	p, verb, err := te.session(context.Background(), te.cfg, ws, r7.Unit(), roundOptions{keepLive: true})
	if err != nil || p.id != "term-rev" || verb != verbExisting || len(m.Writes) != 0 {
		t.Fatalf("session = %+v %q %v, writes %q", p, verb, err, m.Writes)
	}
}

func TestPRSessionPick(t *testing.T) {
	t.Parallel()
	existing := pick{handle: "term-rev", verb: verbExisting}
	fresh := pick{handle: "term-ws-rev", verb: verbNew, writes: "session ws-rev"}
	cases := []struct {
		name   string
		output time.Duration
		noMCP  bool
		live   bool
		opts   roundOptions
		want   pick
	}{
		{"warm automated", time.Minute, false, true, roundOptions{}, existing},
		{"cold automated", 2 * time.Hour, false, true, roundOptions{}, fresh},
		{"cold by hand", 2 * time.Hour, false, true, roundOptions{allowUserActive: true}, existing},
		{"cold keep live", 2 * time.Hour, false, true, roundOptions{keepLive: true}, existing},
		{"new session asked", time.Minute, false, true, roundOptions{newSession: true}, fresh},
		{"warm without MCP", time.Minute, true, true, roundOptions{}, pick{handle: "term-ws-rev", verb: verbNoMCP, writes: "session ws-rev"}},
		{"busy without MCP", time.Minute, true, true, roundOptions{busy: true, keepLive: true}, pick{err: errBusy}},
		{"no live session", 0, false, false, roundOptions{}, fresh},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := warmReviewWorkspace()
			m.LastOutput["ws-rev"] = testNow.Add(-c.output)
			if !c.live {
				m.Sessions = nil
			}
			te := newTestEnv(t, m)
			te.gh.basics = openBasics
			if c.noMCP {
				te.reg.withoutMCP("term-rev")
			}
			var got pick
			var p proc
			p, got.verb, got.err = te.session(context.Background(), te.cfg, m.WorkspaceList[0], r7.Unit(), c.opts)
			got.handle = p.handle
			got.writes = strings.Join(m.Writes, "\n")
			if got.handle != c.want.handle || got.verb != c.want.verb || got.writes != c.want.writes || !errors.Is(got.err, c.want.err) {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

type pick struct {
	handle, verb, writes string
	err                  error
}

func TestGitHubDefaultPlacement(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	ctx := context.Background()
	if err := te.Store.SetViewer(ctx, unit.GitHub, "me"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []dinv.PR{{Ref: r7, Author: "someone", State: dgh.StateOpen, LastSeenAt: testNow}, {Ref: r8, Author: "me", State: dgh.StateOpen, LastSeenAt: testNow}} {
		if err := te.Store.UpsertPR(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	s := te.placements(te.cfg)
	for ref, role := range map[github.Ref]string{r7: "review", r8: "author", {Owner: "acme", Repo: "web", Number: 1}: "review"} {
		got, err := s.Place(ctx, ref.Unit(), "")
		if err != nil || got.Repo != ref.Owner+"/"+ref.Repo || got.Role != role || got.Shared || got.Source != "provider" {
			t.Errorf("%s: %+v %v, want role %s", ref, got, err, role)
		}
	}
}

func TestAWakeThatFailsLateBindsTheProcessAndStartsANewSession(t *testing.T) {
	t.Parallel()
	te, m := sharedEnv(t, "")
	m.Sessions = nil
	m.WakeLate = errors.New("no process within 10s")
	te.registerSession(t, dturn.Session{ID: "old", WorkspaceID: sharedWS.ID, Unit: r7.Unit(), AgentSession: "conv-1", StartedAt: testNow.Add(-time.Hour)})
	ctx := context.Background()
	if err := te.repo.EndHandle(ctx, "old", testNow.Add(-30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	_, woke, err := te.wake(ctx, te.cfg, m, sharedWS, r7.Unit(), roundOptions{}, func(time.Time) bool { return true })
	if err != nil || woke {
		t.Fatalf("wake = %v, %v; want a fall through to a new session", woke, err)
	}
	if id, ok, err := te.repo.SessionOf(ctx, "fake", "term-"+sharedWS.ID); err != nil || !ok || id != "old" {
		t.Fatalf("the late process went to %q %v %v, want the woken session", id, ok, err)
	}
	if !strings.Contains(te.err.String(), "starting a new one") {
		t.Fatalf("stderr %q", te.err)
	}
}

func TestAWakeNamesTheResumedSession(t *testing.T) {
	t.Parallel()
	te, m := sharedEnv(t, "")
	m.Sessions = nil
	te.registerSession(t, dturn.Session{ID: "old", WorkspaceID: sharedWS.ID, Unit: r7.Unit(), AgentSession: "conv-1", StartedAt: testNow.Add(-time.Hour)})
	ctx := context.Background()
	if err := te.repo.EndHandle(ctx, "old", testNow.Add(-30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	d, err := te.intentDef("address")
	if err != nil {
		t.Fatal(err)
	}
	if _, woke, err := te.wake(ctx, te.cfg, m, sharedWS, r7.Unit(), intentOptions(d, roundOptions{}), func(time.Time) bool { return true }); err != nil || !woke {
		t.Fatalf("wake = %v, %v", woke, err)
	}
	const name = "triage acme/api#7 09/10 15:00"
	if len(m.Wakes) != 1 || m.Wakes[0].Title != name || !strings.Contains(m.Wakes[0].Command, " --name '"+name+"'") {
		t.Fatalf("wakes %+v", m.Wakes)
	}
}
