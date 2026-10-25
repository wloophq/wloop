package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/workspace"
	"github.com/wloophq/wloop/internal/workspace/fake"
	"github.com/wloophq/wloop/internal/workspace/orca"
)

func TestConfiguredManagerReplacesTheDefault(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{ManagerName: "orca"})
	te.noDaemon(t)
	selected := &fake.Manager{}
	te.Managers = map[string]ManagerEntry{
		"orca": {Display: "Orca", New: func() workspace.Manager { return &fake.Manager{ManagerName: "orca"} }},
		"fake": {Display: "Fake", New: func() workspace.Manager { return selected }},
	}
	te.cfg.WorkspaceManagers = []string{"fake"}
	if code := te.run("config", "get", "workspace_managers"); code != 0 || te.out.String() != `["fake"]`+"\n" {
		t.Fatalf("exit %d: %s%s", code, te.out, te.err)
	}
	if te.WS.Len() != 1 || te.WS.First() != selected {
		t.Fatalf("WS runs %q, want the configured manager", te.WS.Names())
	}
	te.cfg.WorkspaceManagers = []string{"orca", "fake"}
	before := te.WS
	if err := te.useConfiguredManager(); err != nil || te.WS == before || !slices.Equal(te.WS.Names(), []string{"orca", "fake"}) {
		t.Fatalf("orca and fake: WS %q, %v", te.WS.Names(), err)
	}
	before = te.WS
	if err := te.useConfiguredManager(); err != nil || te.WS != before {
		t.Fatalf("the running managers were replaced by others of their kind: %v", err)
	}
}

func TestUnknownManagerStopsCommandsButNotDoctor(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.Managers = map[string]ManagerEntry{"fake": {Display: "Fake", New: func() workspace.Manager { return &fake.Manager{} }}}
	te.cfg.WorkspaceManagers = []string{"zed"}
	if code := te.run("review", r7.URL()); code != 1 {
		t.Fatalf("review: exit %d", code)
	}
	if want := `error: workspace_managers names "zed", which is not supported; supported: fake` + "\n"; !strings.HasSuffix(te.err.String(), want) {
		t.Fatalf("stderr %q, want %q", te.err, want)
	}
	te.out.Reset()
	if code := te.run("config", "get", "agent"); code != 0 || te.out.String() != "claude\n" {
		t.Fatalf("config get: exit %d: %s%s", code, te.out, te.err)
	}
	te.out.Reset()
	te.run("doctor")
	if out := te.out.String(); !strings.Contains(out, "effective config") || !strings.Contains(out, "[missing] workspace_managers zed") {
		t.Fatalf("doctor:\n%s", out)
	}
}

func TestDoctorNamesAnUnknownManager(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.cfg.WorkspaceManagers = []string{"zed"}
	te.run("doctor")
	out := te.out.String()
	supported := strings.Join(managerNames(te.managers()), ", ")
	for _, want := range []string{
		`  [missing] workspace_managers zed: workspace_managers names "zed", which is not supported; supported: ` + supported + "\n" +
			"            fix with: list only: " + supported + "\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor lacks %q:\n%s", want, out)
		}
	}
	te.out.Reset()
	te.cfg.WorkspaceManagers = []string{"orca"}
	te.run("doctor")
	if strings.Contains(te.out.String(), "[missing] workspace_managers") {
		t.Fatalf("doctor names a supported manager:\n%s", te.out)
	}
}

func TestHomeSpecFollowsTheEnabledManagers(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.Home = "/u"
	te.cfg.WorkspaceManagers = []string{"orca"}
	if s := te.homeSpec(te.cfg); !slices.Equal(s.SessionVars, []string{orca.HandleVar}) || len(s.ManagerHomes) != 0 {
		t.Fatalf("orca: %+v", s)
	}
	te.Managers = map[string]ManagerEntry{"orca": {SessionVar: orca.HandleVar}, "zed": {SessionVar: "ZED_HANDLE", Homes: func(user, kind string) []string { return []string{user + "/zed/" + kind} }}}
	te.cfg.WorkspaceManagers = []string{"zed", "orca"}
	if s := te.homeSpec(te.cfg); !slices.Equal(s.SessionVars, []string{"ZED_HANDLE", orca.HandleVar}) || fmt.Sprint(s.ManagerHomes) != "[/u/zed/claude]" {
		t.Fatalf("zed and orca: %+v", s)
	}
}

type listRunner string

func (r listRunner) Run(context.Context, ...string) ([]byte, error) { return []byte(r), nil }

func (r listRunner) Call(context.Context, string, any) ([]byte, error) { return []byte(r), nil }

func TestOrcaManagerGetsTheRegisteredIdentities(t *testing.T) {
	cached, ok := orcaManager(map[string]string{"gem": "gemini-cli"}).New().(*workspace.Cached)
	if !ok {
		t.Fatal("the orca entry builds no cached manager")
	}
	ws, ok := cached.Direct().(*orca.Manager)
	if !ok {
		t.Fatal("the orca entry builds another manager")
	}
	if _, ok := cached.Direct().(workspace.AgentIdentifier); !ok {
		t.Fatal("the cached manager cannot name an agent's terminals")
	}
	list := listRunner(`{"ok":true,"result":{"terminals":[
		{"handle":"gem","worktreeId":"a","agentIdentity":"gemini-cli","connected":true,"writable":true,"lastOutputAt":1},
		{"handle":"cl","worktreeId":"a","agentIdentity":"claude","connected":true,"writable":true,"lastOutputAt":2}]}}`)
	ws.R, ws.RPC = list, list
	for _, m := range []workspace.Manager{ws, cached} {
		if s, ok, err := m.LiveAgentSession(context.Background(), "a", "gem"); err != nil || !ok || s.Handle != "gem" {
			t.Fatalf("gem: %+v %v %v", s, ok, err)
		}
	}
	root := t.TempDir()
	for _, v := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME"} {
		t.Setenv(v, filepath.Join(root, v))
	}
	e := NewEnv()
	running, _ := e.WS.Named("orca")
	eager, ok := workspace.Direct(running).(*orca.Manager)
	registered, _ := workspace.Direct(e.Managers["orca"].New()).(*orca.Manager)
	if !ok || registered == nil || fmt.Sprintf("%p", eager.Identities) != fmt.Sprintf("%p", registered.Identities) || len(eager.Identities) != 0 {
		t.Fatalf("the eager manager's identities %v are not the registered ones", eager.Identities)
	}
}

func TestTheCachedOrcaManagerKeepsItsFallbackCounter(t *testing.T) {
	t.Parallel()
	ws := orcaManager(nil).New()
	if _, ok := ws.(*workspace.Cached); !ok {
		t.Fatal("the orca entry builds no cached manager")
	}
	if fallbacksOf(workspace.NewSet(ws)) == nil {
		t.Fatal("the cached orca manager hides the fallback counter from Deps.Fallbacks")
	}
}
