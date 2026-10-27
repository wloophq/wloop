package cli

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/config"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/workspace"
	"github.com/wloophq/wloop/internal/workspace/fake"
	"github.com/wloophq/wloop/internal/workspace/orca"
)

func TestTheRootsRegistriesAgree(t *testing.T) {
	root := t.TempDir()
	for _, v := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME"} {
		t.Setenv(v, filepath.Join(root, v))
	}
	e := NewEnv()
	kinds := slices.Sorted(maps.Keys(e.Agents))
	if !slices.Equal(kinds, slices.Sorted(maps.Keys(e.Homes))) || !slices.Equal(kinds, slices.Sorted(maps.Keys(e.Harness.Agents))) {
		t.Fatalf("agents %v, homes %v, protocols %v", kinds, slices.Sorted(maps.Keys(e.Homes)), slices.Sorted(maps.Keys(e.Harness.Agents)))
	}
	def := Defaults()
	if _, ok := e.Agents[def.Agent]; !ok || e.Harness.DefaultAgent != def.Agent {
		t.Fatalf("default agent %q, harness %q, kinds %v", def.Agent, e.Harness.DefaultAgent, kinds)
	}
	var registered []string
	for _, n := range def.WorkspaceManagers {
		if _, ok := e.Managers[n]; ok {
			registered = append(registered, n)
		}
	}
	if len(registered) == 0 || !slices.Equal(e.WS.Names(), registered) {
		t.Fatalf("default managers %q, registered %q, WS %q", def.WorkspaceManagers, registered, e.WS.Names())
	}
	if !slices.Equal(slices.Sorted(maps.Keys(e.Harness.SessionEnvs)), slices.Sorted(maps.Keys(e.Managers))) {
		t.Fatalf("session envs %v, managers %v", slices.Sorted(maps.Keys(e.Harness.SessionEnvs)), slices.Sorted(maps.Keys(e.Managers)))
	}
	if _, ok := e.Harness.SessionEnvs["orca"].(orca.SessionEnv); !ok {
		t.Fatalf("orca's session env is %T", e.Harness.SessionEnvs["orca"])
	}
}

func TestTheRootPicksClaudeAndDesktopThenOrca(t *testing.T) {
	t.Parallel()
	d := Defaults()
	if d.Agent != "claude" || !slices.Equal(d.WorkspaceManagers, []string{"claude-desktop", "orca"}) {
		t.Fatalf("agent %q, managers %q", d.Agent, d.WorkspaceManagers)
	}
	file := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(file, []byte(`github = {review_prefix = "R "}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.LoadFile(file, d)
	if err != nil || c.Agent != "claude" || !slices.Equal(c.WorkspaceManagers, d.WorkspaceManagers) {
		t.Fatalf("loaded agent %q, managers %q, %v", c.Agent, c.WorkspaceManagers, err)
	}
}

func TestOrcasProfileHomesFollowTheRegisteredIdentities(t *testing.T) {
	user := t.TempDir()
	t.Setenv("ORCA_USER_DATA_PATH", filepath.Join(user, "orca"))
	home := filepath.Join(user, "orca", "codex-profiles", "x", "home")
	mkdir(t, home)
	if got := orcaManager(map[string]string{"gem": "codex"}).Homes(user, "gem"); !slices.Equal(got, []string{home}) {
		t.Fatalf("gem's homes %q", got)
	}
}

func TestOrcaKeepsTodaysColumns(t *testing.T) {
	t.Parallel()
	want := map[string]string{"working": "reviewing", "working-fallback": "in-review", "needs-me": "needs-me", "reviewed": "reviewed", "ready": "ready-to-merge", "in-progress": "in-progress", "in-review": "in-review", "completed": "completed"}
	e := &Env{LoadConfig: func() (config.Config, error) {
		return config.LoadFile(filepath.Join(t.TempDir(), "absent.json"), Defaults())
	}}
	loaded, err := e.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]config.Config{"defaults": e.defaultConfig(), "empty file": loaded} {
		if got := c.Views["orca"].Columns; !maps.Equal(got, want) {
			t.Errorf("%s: columns %v", name, got)
		}
	}
	if s := e.boardSink(loaded); s != "orca" {
		t.Errorf("orca's statuses go to sink %q", s)
	}
}

func TestStatusesGoToTheSelectedManagersSink(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{ManagerName: "zed"}
	te := newTestEnv(t, m)
	te.Managers = map[string]ManagerEntry{"orca": orcaManager(nil), "zed": {Display: "Zed", Sink: "zed",
		Columns: map[string]string{config.ColumnWorking: "z-working", config.ColumnWorkingFallback: "z-fallback", config.ColumnReady: "z-ready"},
		New:     func() workspace.Manager { return m }}}
	te.cfg.WorkspaceManagers = []string{"zed"}
	cfg, err := te.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	te.Views = nil
	if err := te.setReviewStatus(context.Background(), cfg, r7, wsdomain.Workspace{ID: "w1"}, false); err != nil || !slices.Equal(m.Writes, []string{"status w1 z-working"}) {
		t.Fatalf("writes %q, %v", m.Writes, err)
	}
	if err := te.setColumn(context.Background(), cfg, wsdomain.Workspace{ID: "w1"}, config.ColumnInProgress); err != nil || len(m.Writes) != 1 {
		t.Fatalf("a status the sink has no column for was written: %q, %v", m.Writes, err)
	}
	te.run("doctor")
	if want := "views.zed.columns.working=z-working views.zed.columns.working-fallback=z-fallback views.zed.columns.ready=z-ready"; !strings.Contains(te.out.String(), want) {
		t.Errorf("doctor lacks %q:\n%s", want, te.out)
	}
	if strings.Contains(te.out.String(), "orca") {
		t.Errorf("doctor names orca:\n%s", te.out)
	}
	te.Managers["zed"] = ManagerEntry{Display: "Zed", New: func() workspace.Manager { return m }}
	m.Writes = nil
	if err := te.setReviewStatus(context.Background(), te.cfg, r7, wsdomain.Workspace{ID: "w1"}, false); err != nil || len(m.Writes) != 0 {
		t.Fatalf("a manager without a sink got %q, %v", m.Writes, err)
	}
}
