package cli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/agent/echo"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/setup"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// withCallerHome runs te as a Claude Code session whose home is home.
func (te *testEnv) withCallerHome(home string) {
	getenv := te.Getenv
	te.Getenv = func(k string) string {
		if k == "CLAUDE_CONFIG_DIR" {
			return home
		}
		return getenv(k)
	}
}

type noHome struct{}

func (noHome) Check(context.Context, string) ([]setup.Finding, error) { return nil, nil }
func (noHome) Install(context.Context, string, bool) ([]string, error) {
	return nil, nil
}
func (noHome) Remove(context.Context, string, bool) ([]string, error) { return nil, nil }

// fakeKind keeps its homes in <user>/.fake and reads skills under
// <home>/<skills>, none when skills is "".
type fakeKind struct{ skills string }

func (fakeKind) New(setup.HomeSpec) setup.AgentHome { return noHome{} }
func (fakeKind) Find(setup.HomeSpec) []string       { return nil }
func (fakeKind) Default(userHome string) string     { return filepath.Join(userHome, ".fake") }
func (k fakeKind) SkillsRoot(home string) string {
	if k.skills == "" {
		return ""
	}
	return filepath.Join(home, k.skills)
}

// fakeProtocol names the caller's fake home in FAKE_HOME and its session in
// FAKE_SESSION.
type fakeProtocol struct{}

func (fakeProtocol) SessionID(getenv func(string) string) string { return getenv("FAKE_SESSION") }
func (fakeProtocol) Home(getenv func(string) string) string      { return getenv("FAKE_HOME") }
func (fakeProtocol) SessionStart([]byte) (string, string, string, error) {
	return "", "", "", nil
}
func (fakeProtocol) Stop([]byte) (string, string, string, error) { return "", "", "", nil }
func (fakeProtocol) Block(string) []byte                         { return nil }

func fakeKindEnv(t *testing.T, k fakeKind, env map[string]string) *testEnv {
	te := newTestEnv(t, &fake.Manager{})
	te.Homes["fake"] = k
	te.Harness.Agents["fake"] = fakeProtocol{}
	te.cfg.Agent = "fake"
	te.Getenv = func(key string) string { return env[key] }
	return te
}

func TestDefaultAgentHomeIsPinned(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.Home = "/u"
	if got := te.agentHome(te.cfg); got != filepath.Join("/u", ".claude") {
		t.Fatalf("agent home %q", got)
	}
	te.cfg.AgentHome = "/work"
	if got := te.agentHome(te.cfg); got != "/work" {
		t.Fatalf("configured agent home %q", got)
	}
}

func TestAgentHomeIsTheKindsDefault(t *testing.T) {
	t.Parallel()
	te := fakeKindEnv(t, fakeKind{}, nil)
	te.Home = "/u"
	if got := te.agentHome(te.cfg); got != "/u/.fake" {
		t.Fatalf("agent home %q", got)
	}
}

func TestCallerHomeComesFromTheKind(t *testing.T) {
	t.Parallel()
	te := fakeKindEnv(t, fakeKind{}, map[string]string{"FAKE_HOME": "/p/home", "CLAUDE_CONFIG_DIR": "/claude/home"})
	if got := te.callerHome(te.cfg, te.cfg.Agent); got != "/p/home" {
		t.Fatalf("caller home %q", got)
	}
	if got := te.callerAgentHome(te.cfg); got != "/p/home" {
		t.Fatalf("caller agent home %q", got)
	}
	if got := te.homeSpec(te.cfg).CallerHome; got != "/p/home" {
		t.Fatalf("spec caller home %q", got)
	}
}

func TestCallerHomeEqualToTheDefaultIsNoHome(t *testing.T) {
	user := t.TempDir()
	t.Setenv("HOME", user)
	for _, home := range []string{user + "/.fake", user + "/.fake/"} {
		te := fakeKindEnv(t, fakeKind{}, map[string]string{"FAKE_HOME": home})
		te.Home = user
		if got := te.callerAgentHome(te.cfg); got != "" {
			t.Errorf("fake %q: caller agent home %q", home, got)
		}
	}
	for _, home := range []string{"", user + "/.claude", user + "/.claude/", "~/.claude"} {
		te := newTestEnv(t, &fake.Manager{})
		te.Home = user
		te.withCallerHome(home)
		if got := te.callerAgentHome(te.cfg); got != "" {
			t.Errorf("claude %q: caller agent home %q", home, got)
		}
	}
	te := newTestEnv(t, &fake.Manager{})
	te.Home = user
	te.withCallerHome("~/.claude-work")
	if got := te.callerAgentHome(te.cfg); got != filepath.Join(user, ".claude-work") {
		t.Fatalf("caller agent home %q", got)
	}
}

func TestHandRoundOverridesTheCallerHome(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.withCallerHome("/daemon/home")
	if got := te.callerAgentHome(te.cfg); got != "/daemon/home" {
		t.Fatalf("daemon's own caller home %q", got)
	}
	for brought, want := range map[string]string{"/session/home": "/session/home", "": "", te.Home + "/.claude": ""} {
		q := *te.Env
		q.HomeOverride = &brought
		if got := q.callerAgentHome(te.cfg); got != want {
			t.Errorf("brought %q: caller agent home %q, want %q", brought, got, want)
		}
		if got := q.homeSpec(te.cfg).CallerHome; got != brought {
			t.Errorf("brought %q: spec caller home %q", brought, got)
		}
	}
}

func TestHandNudgeNeverReentersTheAction(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.gh.basics = openBasics
	te.Rec = storeRecorder{te.Store}
	inputs := map[string]any{"situation": "Waiting on: bob.", "waits_on_maintainers": false, "who": []string{"bob"}}
	res, err := te.byHand(&sync.Mutex{})(context.Background(), api.ByHandRequest{Intent: "nudge", URL: r7.URL(), Home: "/session/home", Inputs: inputs}, "term_1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "Waiting on: bob.") {
		t.Fatalf("text:\n%s", res.Text)
	}
	evs, _ := te.Store.Events(context.Background(), r7)
	if !slices.ContainsFunc(evs, func(ev dinv.Event) bool { return ev.Kind == dinv.KindNudged && ev.Data == "bob" }) {
		t.Fatalf("events %+v", evs)
	}
}

func TestHandRoundHomeEqualToAnyKindsDefaultIsNoHome(t *testing.T) {
	t.Parallel()
	te := fakeKindEnv(t, fakeKind{}, map[string]string{"FAKE_HOME": "/p/home"})
	te.cfg.Agent = "claude"
	for brought, want := range map[string]string{te.Home + "/.fake": "", te.Home + "/.claude/": "", "/p/home": "/p/home"} {
		q := *te.Env
		q.HomeOverride = &brought
		if got := q.callerAgentHome(config.Config{Agent: "claude"}); got != want {
			t.Errorf("brought %q: caller agent home %q, want %q", brought, got, want)
		}
	}
}

func TestEveryAgentKindHasAHomeKind(t *testing.T) {
	t.Parallel()
	e := NewEnv()
	for name := range e.Agents {
		if _, ok := e.Homes[name]; !ok {
			t.Errorf("agent %q has no home kind", name)
		}
	}
}

func TestAHandRoundsHomeReachesOnlyItsKindsHomes(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	echoKind := echo.HomeKind{Root: t.TempDir()}
	te.Homes["echo"] = echoKind
	te.Harness.Agents["echo"] = echo.Protocol{}
	te.cfg.Projects = []config.Project{{Match: "acme/*", Agent: "echo"}}
	for home, kind := range map[string]string{"/elsewhere/home": "claude", echoKind.Default(""): "echo"} {
		te.HomeOverride = &home
		byKind := te.homesByKind(te.cfg)
		for _, other := range []string{"claude", "echo"} {
			if got := slices.Contains(byKind[other], home); got != (other == kind) {
				t.Errorf("%s brought by hand: in %s's homes %v: %q", home, other, got, byKind[other])
			}
		}
	}
}

func TestDelegatedCallsFindTheKindWhoseSessionIsSet(t *testing.T) {
	t.Parallel()
	te := fakeKindEnv(t, fakeKind{}, map[string]string{"FAKE_SESSION": "s-1", "FAKE_HOME": "/p/home"})
	te.cfg.Agent = "claude"
	if got := te.callerKind(); got != "fake" {
		t.Fatalf("kind %q", got)
	}
	te.Getenv = func(k string) string { return map[string]string{"FAKE_HOME": "/p/home"}[k] }
	if got := te.callerKind(); got != "claude" {
		t.Fatalf("a home but no session: kind %q", got)
	}
	te.Getenv = func(string) string { return "" }
	if got := te.callerKind(); got != "claude" {
		t.Fatalf("no caller home: kind %q", got)
	}
}

func TestSkillsGoToTheKindsRoot(t *testing.T) {
	t.Parallel()
	te := fakeKindEnv(t, fakeKind{skills: "agent-config"}, nil)
	home := filepath.Join(t.TempDir(), "home")
	mkdir(t, home)
	if code := te.run("install", "--home", home); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	for _, dir := range []string{filepath.Join(home, "agent-config"), filepath.Join(te.Home, ".agents")} {
		if _, err := os.Stat(skillPathIn(dir, brand.Name+"-review")); err != nil {
			t.Errorf("skill: %v", err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "skills")); err == nil {
		t.Errorf("skills written into the home itself")
	}
	checks := te.homeChecksFor(t, home)
	if !strings.Contains(checks, "skills in "+filepath.Join(home, "agent-config")) {
		t.Errorf("doctor checks:\n%s", checks)
	}
}

func TestKindWithoutSkillsGetsOnlyTheSharedDir(t *testing.T) {
	t.Parallel()
	te := fakeKindEnv(t, fakeKind{}, nil)
	home := filepath.Join(t.TempDir(), "home")
	mkdir(t, home)
	if code := te.run("install", "--home", home); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if _, err := os.Stat(skillPathIn(filepath.Join(te.Home, ".agents"), brand.Name+"-review")); err != nil {
		t.Errorf("shared skill: %v", err)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("install wrote into a home without skills: %v", entries)
	}
	if checks := te.homeChecksFor(t, home); strings.Contains(checks, "skills in "+home) {
		t.Errorf("doctor checks skills in a home without them:\n%s", checks)
	}
}

// homeChecksFor is doctor's home checks with home as the config's only one.
func (te *testEnv) homeChecksFor(t *testing.T, home string) string {
	t.Helper()
	cfg := te.cfg
	cfg.AgentHome = home
	var b strings.Builder
	for _, c := range te.homeChecks(context.Background(), cfg) {
		b.WriteString(c.name + "\n")
	}
	return b.String()
}

// recordKind is fakeKind whose homes report every check, and print every
// install, under its name.
type recordKind struct {
	fakeKind
	name string
}

func (k recordKind) New(setup.HomeSpec) setup.AgentHome { return recordHome{name: k.name} }

type recordHome struct {
	noHome
	name string
}

func (h recordHome) Check(_ context.Context, home string) ([]setup.Finding, error) {
	return []setup.Finding{{Home: home, Entry: "checked by " + h.name, Problem: "p"}}, nil
}

func (h recordHome) Install(_ context.Context, home string, _ bool) ([]string, error) {
	return []string{h.name + " installs into " + home}, nil
}

// withEcho registers the echo agent with a recordKind home.
func (te *testEnv) withEcho() {
	te.Agents["echo"] = &echo.Agent{}
	te.Homes["echo"] = recordKind{name: "echo"}
}

func TestHandTakeOverRunsWithTheHomeItBrings(t *testing.T) {
	t.Parallel()
	for brought, want := range map[string]string{"/session/home": "/session/home", "": ""} {
		m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
		te := newTestEnv(t, m)
		te.gh.basics = adoptBasics
		te.git.branch["/worktrees/feat/theirs"] = "feat/theirs"
		te.Rec = storeRecorder{te.Store}
		te.withCallerHome("/daemon/home")
		req := api.ByHandRequest{Intent: "address", URL: "https://github.com/acme/api/pull/20", Home: brought, Cwd: t.TempDir(), Inputs: map[string]any{"take_over": true}}
		if _, err := te.byHand(&sync.Mutex{})(context.Background(), req, "term_1"); err == nil || !strings.Contains(err.Error(), "run this command there") {
			t.Fatalf("brought %q: %v", brought, err)
		}
		if len(m.Commands) != 1 || strings.Contains(m.Commands[0], "/daemon/home") || want != "" && !strings.Contains(m.Commands[0], want) {
			t.Errorf("brought %q: commands %q", brought, m.Commands)
		}
	}
}
