package config

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/turns"
)

// tools is Defaults with the agent and workspace managers the composition
// root supplies.
func tools() Config {
	c := Defaults()
	c.Agent = "claude"
	c.WorkspaceManagers = []string{"claude-desktop", "orca"}
	return c
}

func TestDefaults(t *testing.T) {
	c, err := LoadFile(filepath.Join(t.TempDir(), "absent.json"), tools())
	if err != nil {
		t.Fatal(err)
	}
	if c.ProjectsDir != "" || c.Profile != "" || c.GitHub.ReviewPrefix != "REVIEW " || len(c.Views) != 0 ||
		c.Turns.PerUnitPerDay != 5 || c.Agent != "claude" || !slices.Equal(c.WorkspaceManagers, tools().WorkspaceManagers) {
		t.Fatalf("defaults = %+v", c)
	}
}

func TestDefaultsNameNoTool(t *testing.T) {
	if d := Defaults(); d.Agent != "" || d.WorkspaceManagers != nil {
		t.Fatalf("agent %q, managers %q", d.Agent, d.WorkspaceManagers)
	}
}

func TestARefusedFileFallsBackToTheBase(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(file, []byte(`{"workspace_managers":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFile(file, tools())
	if err == nil || !strings.Contains(err.Error(), `["claude-desktop" "orca"]`) || c.Agent != "claude" || !slices.Equal(c.WorkspaceManagers, tools().WorkspaceManagers) {
		t.Fatalf("loaded agent %q, managers %q, %v", c.Agent, c.WorkspaceManagers, err)
	}
}

func TestLoadOverridesAndExpandsHome(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"projects_dir": "~/projects/work", "turns": {"per_unit_per_day": 2}, "github": {"owners": ["acme"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFile(path, tools())
	if err != nil {
		t.Fatal(err)
	}
	if c.ProjectsDir != filepath.Join(Home(), "projects/work") || c.Turns.PerUnitPerDay != 2 || c.GitHub.Owners[0] != "acme" {
		t.Fatalf("config = %+v", c)
	}
}

func TestLoadRejectsBrokenJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path, tools()); err == nil {
		t.Fatal("expected an error")
	}
}

func TestXDGPaths(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/cfg")
	t.Setenv("XDG_STATE_HOME", "/st")
	if Path() != "/cfg/"+brand.StateDir()+"/config.json" || StateDir() != "/st/"+brand.StateDir() {
		t.Fatalf("Path = %s, StateDir = %s", Path(), StateDir())
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_STATE_HOME", "relative")
	if Path() != filepath.Join(Home(), ".config/"+brand.StateDir()+"/config.json") || StateDir() != filepath.Join(Home(), ".local/state/"+brand.StateDir()) {
		t.Fatalf("Path = %s, StateDir = %s", Path(), StateDir())
	}
}

func TestProjectsOverrideByFirstMatch(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	file := filepath.Join(dir, "config.json")
	raw := `{"projects_dir":"/work","projects":[
		{"match":"Carol/notes-app"},
		{"match":"carol/*","projects_dir":"/oss","agent_home":"/p/.claude"}]}`
	if err := os.WriteFile(file, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFile(file, tools())
	if err != nil {
		t.Fatal(err)
	}
	sm := c.For("carol", "notes-app")
	if sm.AgentHome != "" || sm.ProjectsDir != "/work" {
		t.Errorf("notes-app: %+v", sm)
	}
	other := c.For("carol", brand.StateDir())
	if other.ProjectsDir != "/oss" || other.AgentHome != "/p/.claude" {
		t.Errorf(brand.StateDir()+": %+v", other)
	}
	if work := c.For("globex", "widgets"); work.ProjectsDir != "/work" || work.AgentHome != "" {
		t.Errorf("globex/widgets: %+v", work)
	}
	if got := c.AgentHomes(); len(got) != 1 || got[0] != "/p/.claude" {
		t.Errorf("AgentHomes = %v", got)
	}

	if err := os.WriteFile(file, []byte(`{"projects":[{"match":""}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(file, tools()); err == nil {
		t.Error("accepted a project without match")
	}
}

func TestFollowupsWithoutWorkspaceIsPerProject(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	file := filepath.Join(dir, "config.json")
	raw := `{
  "github": {
    "followups_without_workspace": true
  },
  "projects": [
    {
      "match": "carol/sandbox",
      "github": {
        "followups_without_workspace": true
      }
    },
    {
      "match": "globex/*",
      "github": {
        "followups_without_workspace": false
      }
    },
    {
      "match": "carol/*",
      "agent_home": "/p/.claude"
    }
  ]
}`
	if err := os.WriteFile(file, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFile(file, tools())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		owner, repo string
		want        bool
	}{{"carol", "loop-sandbox", true}, {"globex", "widgets", false}, {"carol", brand.StateDir(), true}, {"acme", "api", true}} {
		if got := c.For(tc.owner, tc.repo).GitHub.FollowupsWithoutWorkspace; got != tc.want {
			t.Errorf("%s/%s: %v, want %v", tc.owner, tc.repo, got, tc.want)
		}
	}
	if Defaults().GitHub.FollowupsWithoutWorkspace {
		t.Error("on by default")
	}
}

func TestAddressWithoutWorkspaceIsPerProject(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	file := filepath.Join(dir, "config.json")
	raw := `{
  "github": {
    "address_without_workspace": true
  },
  "projects": [
    {
      "match": "globex/*",
      "github": {
        "address_without_workspace": false
      }
    },
    {
      "match": "carol/*",
      "agent_home": "/p/.claude"
    }
  ]
}`
	if err := os.WriteFile(file, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFile(file, tools())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		owner, repo string
		want        bool
	}{{"globex", "widgets", false}, {"carol", brand.StateDir(), true}, {"acme", "api", true}} {
		if got := c.For(tc.owner, tc.repo).GitHub.AddressWithoutWorkspace; got != tc.want {
			t.Errorf("%s/%s: %v, want %v", tc.owner, tc.repo, got, tc.want)
		}
	}
	if Defaults().GitHub.AddressWithoutWorkspace {
		t.Error("on by default")
	}
}

func TestCockpitDefaults(t *testing.T) {
	c := Defaults()
	if time.Duration(c.GitHub.Stalled.Review) != 48*time.Hour || time.Duration(c.GitHub.Stalled.Reply) != 24*time.Hour || time.Duration(c.GitHub.Stalled.Merge) != 24*time.Hour {
		t.Fatalf("stalled defaults: %+v", c.GitHub.Stalled)
	}
	if time.Duration(c.Sweep.Fast) != 2*time.Minute || time.Duration(c.Sweep.Slow) != time.Hour || c.Sweep.RateLimitFraction != 0.5 {
		t.Fatalf("sweep defaults: %+v", c.Sweep)
	}
	if c.HTTP.Port != 0 {
		t.Fatalf("http port default: %d", c.HTTP.Port)
	}
	if c.MCP.Port != 47800 || c.MCP.URL() != "http://127.0.0.1:47800/mcp" {
		t.Fatalf("mcp default: %d %s", c.MCP.Port, c.MCP.URL())
	}
}

func TestLoadCockpitKeys(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.json")
	os.WriteFile(file, []byte(`{
  "github": {
    "scope": {
      "people": [
        "ana"
      ],
      "teams": [
        "acme/search"
      ],
      "repos": [
        "acme/api"
      ],
      "watch": [
        "bruno"
      ]
    },
    "stalled": {
      "review": "3d",
      "reply": "12h"
    }
  },
  "sweep": {
    "fast": "5m",
    "rate_limit_fraction": 0.3
  },
  "http": {
    "port": 4242
  },
  "mcp": {
    "port": 47899
  }
}`), 0o644)
	c, err := LoadFile(file, tools())
	if err != nil {
		t.Fatal(err)
	}
	if c.GitHub.Scope.People[0] != "ana" || c.GitHub.Scope.Teams[0] != "acme/search" || c.GitHub.Scope.Repos[0] != "acme/api" || c.GitHub.Scope.Watch[0] != "bruno" {
		t.Fatalf("scope: %+v", c.GitHub.Scope)
	}
	if time.Duration(c.GitHub.Stalled.Review) != 72*time.Hour || time.Duration(c.GitHub.Stalled.Reply) != 12*time.Hour || time.Duration(c.GitHub.Stalled.Merge) != 24*time.Hour {
		t.Fatalf("stalled: %+v", c.GitHub.Stalled)
	}
	if time.Duration(c.Sweep.Fast) != 5*time.Minute || time.Duration(c.Sweep.Slow) != time.Hour || c.Sweep.RateLimitFraction != 0.3 {
		t.Fatalf("sweep: %+v", c.Sweep)
	}
	if c.HTTP.Port != 4242 || c.MCP.Port != 47899 {
		t.Fatalf("ports: %d %d", c.HTTP.Port, c.MCP.Port)
	}
}

func TestLoadRejectsOutOfRangeCockpitKeys(t *testing.T) {
	for _, body := range []string{
		`{"sweep": {"fast": "0s"}}`,
		`{"github": {"stalled": {"review": "-1d"}}}`,
		`{"sweep": {"rate_limit_fraction": 2}}`,
		`{"http": {"port": 70000}}`,
		`{"mcp": {"port": 0}}`,
		`{"mcp": {"port": 65536}}`,
	} {
		file := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadFile(file, tools()); err == nil {
			t.Fatalf("%s: expected an error", body)
		}
	}
}

func loadJSON(t *testing.T, raw string) Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFile(path, tools())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestBriefDefaultsAndValidation(t *testing.T) {
	if d := Defaults().Brief; d.Enabled || d.Tier != turns.TierFast || d.MaxInputBytes != 60000 {
		t.Fatalf("defaults %+v", d)
	}
	if c := loadJSON(t, `{"brief": {"max_input_bytes": 200000}}`); c.Brief.MaxInputBytes != 200000 {
		t.Fatalf("ceiling refused: %+v", c.Brief)
	}
	c := loadJSON(t, `{"brief": {"enabled": true}}`)
	if !c.Brief.Enabled || c.Brief.Tier != turns.TierFast || c.Brief.MaxInputBytes != 60000 {
		t.Fatalf("partial brief lost its defaults: %+v", c.Brief)
	}
	for _, bad := range []string{`{"brief": {"max_input_bytes": 10}}`, `{"brief": {"max_input_bytes": 200001}}`, `{"brief": {"tier": "haiku"}}`} {
		path := filepath.Join(t.TempDir(), "config.json")
		os.WriteFile(path, []byte(bad), 0o600)
		if _, err := LoadFile(path, tools()); err == nil || !strings.Contains(err.Error(), "brief.") {
			t.Errorf("%s: err %v", bad, err)
		}
	}
}

func TestAgentModelsPinTiersOpaquely(t *testing.T) {
	c := loadJSON(t, `{"brief": {"tier": "balanced"}, "agent_models": {"claude": {"balanced": "claude-sonnet-5-5"}}}`)
	if c.Brief.Tier != turns.TierBalanced || c.AgentModels["claude"][turns.TierBalanced] != "claude-sonnet-5-5" {
		t.Fatalf("%+v %v", c.Brief, c.AgentModels)
	}
	for _, bad := range []string{`{"agent_models": {"claude": {"huge": "x"}}}`, `{"agent_models": {"claude": {"fast": " "}}}`} {
		path := filepath.Join(t.TempDir(), "config.json")
		os.WriteFile(path, []byte(bad), 0o600)
		if _, err := LoadFile(path, tools()); err == nil || !strings.Contains(err.Error(), "agent_models.claude") {
			t.Errorf("%s: err %v", bad, err)
		}
	}
}

func TestTrustedAuthorsPerProject(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	file := filepath.Join(dir, "config.json")
	raw := `{
  "github": {
    "trusted_authors": [
      "a[bot]"
    ]
  },
  "projects": [
    {
      "match": "acme/api",
      "github": {
        "trusted_authors": [
          "b[bot]"
        ]
      }
    },
    {
      "match": "acme/*"
    }
  ]
}`
	if err := os.WriteFile(file, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFile(file, tools())
	if err != nil {
		t.Fatal(err)
	}
	if got := c.For("acme", "api").GitHub.TrustedAuthors; len(got) != 1 || got[0] != "b[bot]" {
		t.Errorf("project value = %v", got)
	}
	if got := c.For("acme", "web").GitHub.TrustedAuthors; len(got) != 1 || got[0] != "a[bot]" {
		t.Errorf("top-level value = %v", got)
	}
}

func TestEmptyTrustedAuthorIsRefused(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	for _, raw := range []string{
		`{"github": {"trusted_authors": ["a[bot]", " "]}}`,
		`{"projects": [{"match": "acme/*", "github": {"trusted_authors": [""]}}]}`,
	} {
		file := filepath.Join(dir, "config.json")
		if err := os.WriteFile(file, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadFile(file, tools()); err == nil || !strings.Contains(err.Error(), "trusted_authors") {
			t.Errorf("%s: %v", raw, err)
		}
	}
}

func TestProjectAgentOverridesTheTopLevel(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.json")
	raw := `{"projects":[{"match":"oss/*","agent":"echo"},{"match":"acme/*","agent_home":"/w"}]}`
	if err := os.WriteFile(file, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFile(file, tools())
	if err != nil {
		t.Fatal(err)
	}
	if got := c.For("oss", "lib").Agent; got != "echo" {
		t.Errorf("oss/lib agent %q", got)
	}
	if got := c.For("acme", "api"); got.Agent != "claude" || got.AgentHome != "/w" {
		t.Errorf("acme/api agent %q home %q", got.Agent, got.AgentHome)
	}
	if got := c.Agent; got != "claude" {
		t.Errorf("top-level agent %q", got)
	}
}

func TestWorkspaceManagersOverrideTheBase(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(file, []byte(`{"workspace_managers":["orca"],"projects":[{"match":"acme/*","workspace_managers":["claude-desktop"]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFile(file, tools())
	if err != nil || !slices.Equal(c.WorkspaceManagers, []string{"orca"}) {
		t.Fatalf("loaded %q, %v", c.WorkspaceManagers, err)
	}
	if got := c.For("acme", "api").WorkspaceManagers; !slices.Equal(got, []string{"claude-desktop"}) {
		t.Fatalf("acme/api picks from %q", got)
	}
	if got := c.For("other", "x").WorkspaceManagers; !slices.Equal(got, []string{"orca"}) {
		t.Fatalf("other/x picks from %q", got)
	}
}

func TestWorkspaceManagersRefusesTheOldKeyAndAnEmptyList(t *testing.T) {
	dir := t.TempDir()
	for name, raw := range map[string]string{"old": `{"workspace_manager":"orca"}`, "empty": `{"workspace_managers":[]}`} {
		file := filepath.Join(dir, name+".json")
		if err := os.WriteFile(file, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadFile(file, tools()); err == nil {
			t.Errorf("%s: %s loaded", name, raw)
		}
	}
}

func loadRaw(t *testing.T, raw string) Config {
	t.Helper()
	file := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(file, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFile(file, tools())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestWithColumnsKeepsTheDefaultsTheConfigLeavesOut(t *testing.T) {
	defaults := map[string]map[string]string{"orca": {"working": "reviewing", "needs-me": "needs-me"}}
	for name, c := range map[string]Config{"defaults": Defaults(), "empty file": loadRaw(t, `{}`)} {
		if got := c.WithColumns(defaults).Views["orca"].Columns; !maps.Equal(got, defaults["orca"]) {
			t.Errorf("%s: columns %v", name, got)
		}
	}
	blank := loadRaw(t, `{"views":{"orca":{"columns":{"needs-me":""}}}}`).WithColumns(defaults)
	if got := blank.Column("orca", ColumnNeedsMe); got != "" {
		t.Errorf("a column set to empty took its default: %q", got)
	}
	if defaults["orca"]["needs-me"] != "needs-me" {
		t.Errorf("WithColumns wrote into the defaults: %v", defaults)
	}
}

func TestViewsSectionSetsOnlyItsColumns(t *testing.T) {
	c := loadRaw(t, `{"views":{"orca":{"columns":{"needs-me":"new"}},"zed":{"columns":{"needs-me":"z"}}}}`).WithColumns(map[string]map[string]string{"orca": {"working": "reviewing", "needs-me": "needs-me"}})
	if got := c.Column("orca", ColumnNeedsMe); got != "new" {
		t.Errorf("needs-me column %q", got)
	}
	if got := c.Column("orca", ColumnWorking); got != "reviewing" {
		t.Errorf("working column %q", got)
	}
	if got := c.Column("zed", ColumnNeedsMe); got != "z" {
		t.Errorf("zed column %q", got)
	}
}

func TestGTDDefaultsAndParsing(t *testing.T) {
	if d := Defaults().GTD; d.StaleWaitingDays != 2 || len(d.Areas) != 0 || len(d.Contexts) != 0 {
		t.Fatalf("defaults %+v", d)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	raw := `{"gtd": {"stale_waiting_days": 3, "areas": ["OSS", "Hiring"], "contexts": ["@decide", "@reply"]}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFile(path, tools())
	if err != nil {
		t.Fatal(err)
	}
	if g := c.GTD; g.StaleWaitingDays != 3 || strings.Join(g.Areas, ",") != "OSS,Hiring" || strings.Join(g.Contexts, ",") != "@decide,@reply" {
		t.Fatalf("gtd %+v", g)
	}
}

func TestLoadRefusesUnknownKeysByPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	raw := `{"agnet": "claude", "sweep": {"fast": "2m", "fsat": "1m"}, "projects": [{"match": "a/*", "agent_hom": "/h"}],
		"workflows": {"address": {"approval": {"windw": "1m"}}}, "github": {"drafts": {"aproval": {}}}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadFile(path, tools())
	var unknown *UnknownKeysError
	if !errors.As(err, &unknown) || unknown.File != path {
		t.Fatalf("err %v", err)
	}
	want := []string{"agnet", "github.drafts.aproval", "projects[0].agent_hom", "sweep.fsat", "workflows.address.approval.windw"}
	if !slices.Equal(unknown.Keys, want) || !strings.Contains(err.Error(), path+": unknown keys agnet, ") {
		t.Fatalf("keys %v, error %q", unknown.Keys, err)
	}
}

func TestEveryDefaultKeyIsKnown(t *testing.T) {
	raw, err := json.Marshal(tools())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path, tools()); err != nil {
		t.Fatal(err)
	}
}

func TestTurnsAndAWorkflowsDraftApprovalLoad(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(file, []byte(`{"turns": {"per_day": 7}, "workflows": {"slack-closure": {"drafts": {"approval": {"mode": "delay", "window": "10m"}}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFile(file, tools())
	if err != nil {
		t.Fatal(err)
	}
	if c.Turns.PerDay != 7 {
		t.Errorf("turns %+v", c.Turns)
	}
	if a := c.Workflows["slack-closure"].DraftApproval(); a.Mode != "delay" || time.Duration(*a.Window) != 10*time.Minute {
		t.Errorf("draft approval %+v", a)
	}
	if Defaults().Turns.PerDay != 20 {
		t.Error("the default daily cap is not 20")
	}
	bad := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(bad, []byte(`{"workflows": {"x": {"drafts": {"approval": {"mode": "delay"}}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(bad, tools()); err == nil || !strings.Contains(err.Error(), "workflows.x.drafts.approval") {
		t.Fatalf("a window without one loaded: %v", err)
	}
}
