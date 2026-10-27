package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/agent/claude"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/termfmt"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// serveSocket serves h on a Unix socket in a short temporary directory and
// returns the socket path.
func serveSocket(t *testing.T, h http.Handler) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", brand.Name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "daemon.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(h)
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	return path
}

func serveJSON(t *testing.T, routes map[string]any) string {
	t.Helper()
	mux := http.NewServeMux()
	for pattern, v := range routes {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(v)
		})
	}
	return serveSocket(t, mux)
}

func TestDoctorReportsDaemon(t *testing.T) {
	t.Run("not running", bubbled(func(t *testing.T) {
		te := newTestEnv(t, &fake.Manager{})
		te.Socket = filepath.Join(te.StateDir, "daemon.sock")
		if err := os.WriteFile(filepath.Join(te.StateDir, "http.json"), []byte(`{"port":1,"token":"t"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		before, _ := os.ReadDir(te.StateDir)
		start := time.Now()
		if code, took := te.run("doctor"), time.Since(start); code != 1 || took != 0 {
			t.Fatalf("exit %d after waiting %v", code, took)
		}
		if e := te.err.String(); !strings.Contains(e, "launchctl kickstart gui/") || !strings.Contains(e, brand.Command+" serve") {
			t.Fatalf("stderr:\n%s", e)
		}
		if te.out.Len() != 0 {
			t.Fatalf("stdout:\n%s", te.out)
		}
		if after, _ := os.ReadDir(te.StateDir); len(after) != len(before) {
			t.Fatalf("state dir changed: %v -> %v", before, after)
		}
	}))

	t.Run("LaunchAgent not loaded", func(t *testing.T) {
		te := newTestEnv(t, &fake.Manager{})
		fakeLaunchctl(t, te, fakeShell{})
		if code := te.run("app", "install", "--no-menu-bar"); code != 0 {
			t.Fatalf("exit %d: %s", code, te.err)
		}
		te.out.Reset()
		te.Socket = serveJSON(t, map[string]any{"GET /api/health": api.Health{Version: "v1.1.0"}})
		te.run("doctor")
		want := fmt.Sprintf("fix with: launchctl kickstart gui/%d/%s", os.Getuid(), launchAgentLabel)
		if out := te.out.String(); !strings.Contains(out, "LaunchAgent loaded: launchctl does not know it") || !strings.Contains(out, want) || strings.Contains(out, "fix with: "+brand.Command+" app install") {
			t.Fatalf("doctor:\n%s", out)
		}
	})

	t.Run("running with a failing job", func(t *testing.T) {
		te := newTestEnv(t, &fake.Manager{})
		te.Socket = serveJSON(t, map[string]any{"GET /api/health": api.Health{
			Version: "v9",
			Viewer:  "me",
			Jobs: []api.JobStatus{
				{Name: "fast", Every: 10 * time.Minute, LastRun: testNow.Add(-15 * time.Minute), LastOK: testNow.Add(-15 * time.Minute)},
				{Name: "slow", LastRun: testNow.Add(-5 * time.Hour), LastOK: testNow.Add(-5 * time.Hour)},
				{Name: "sweep", LastRun: testNow.Add(-time.Minute), LastErr: "orca: connection refused"},
			},
			Warnings: []string{"fast: search author:me failed"},
			SlowSync: true,
		}})
		te.run("doctor")
		out := te.out.String()
		for _, want := range []string{"daemon: running", "orca: connection refused", "fast: search author:me failed", "slow sync", "job slow"} {
			if !strings.Contains(out, want) {
				t.Errorf("doctor misses %q:\n%s", want, out)
			}
		}
		if strings.Contains(out, "job fast") {
			t.Errorf("a healthy job is reported:\n%s", out)
		}
	})

	t.Run("not answering", func(t *testing.T) {
		for _, tc := range []struct{ os, hint string }{{"darwin", "launchctl kickstart -k gui/"}, {"linux", "restart " + brand.Command + " serve"}} {
			te := newTestEnv(t, &fake.Manager{})
			onOS(t, tc.os)
			te.Socket = serveSocket(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "boom", http.StatusInternalServerError)
			}))
			te.run("doctor")
			out := te.out.String()
			if !strings.Contains(out, "did not answer") || !strings.Contains(out, tc.hint) {
				t.Fatalf("%s: doctor:\n%s", tc.os, out)
			}
		}
	})

	t.Run("older than the CLI", func(t *testing.T) {
		for _, tc := range []struct{ os, hint string }{{"darwin", "launchctl kickstart -k gui/"}, {"linux", "restart " + brand.Command + " serve"}} {
			te := newTestEnv(t, &fake.Manager{})
			onOS(t, tc.os)
			withVersion(t, "v1.2.0")
			te.Socket = serveJSON(t, map[string]any{"GET /api/health": api.Health{Version: "v1.1.0"}})
			te.run("doctor")
			out := te.out.String()
			if !strings.Contains(out, "daemon runs v1.1.0, CLI is v1.2.0") || !strings.Contains(out, tc.hint) {
				t.Fatalf("%s: doctor:\n%s", tc.os, out)
			}
		}
	})

	t.Run("dev build is not compared", func(t *testing.T) {
		te := newTestEnv(t, &fake.Manager{})
		withVersion(t, "dev")
		te.Socket = serveJSON(t, map[string]any{"GET /api/health": api.Health{Version: "v1.1.0"}})
		te.run("doctor")
		if out := te.out.String(); strings.Contains(out, "CLI is") {
			t.Fatalf("doctor:\n%s", out)
		}
	})
}

func onOS(t *testing.T, os string) {
	prev := goos
	goos = os
	t.Cleanup(func() { goos = prev })
}

func withVersion(t *testing.T, v string) {
	prev := Version
	Version = v
	t.Cleanup(func() { Version = prev })
}

// doctorWithApp runs doctor against a daemon that has the app status st.
func doctorWithApp(t *testing.T, st api.AppStatus) string {
	t.Helper()
	d := startDaemon(t, nil)
	if err := client.New(d.socket).ReportApp(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	te := newTestEnv(t, &fake.Manager{})
	te.Socket = d.socket
	te.run("doctor")
	return te.out.String()
}

func TestDoctorReportsDeniedNotifications(t *testing.T) {
	t.Parallel()
	out := doctorWithApp(t, api.AppStatus{State: "running", Notifications: "denied"})
	if !strings.Contains(out, "notifications") || !strings.Contains(out, "System Settings") {
		t.Fatalf("%s", out)
	}
	if strings.Contains(out, "daemon warning") {
		t.Fatalf("the same fact reported twice:\n%s", out)
	}
}

func TestDoctorNamesTheBundleOfTheNotifications(t *testing.T) {
	t.Parallel()
	out := doctorWithApp(t, api.AppStatus{State: "running", Notifications: "not-determined"})
	line := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "] notifications:") {
			line = l
		}
	}
	if !strings.Contains(line, bundleID) || !strings.Contains(out, "System Settings > Notifications > "+brand.Name) {
		t.Fatalf("the bundle id without permission:\n%s", out)
	}
}

func TestDoctorShowsAJobThatNeverExited(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	fakeLaunchctl(t, te, fakeShell{})
	writePlist(t, te, te.renderPlist(daemonAgent(te.Executable), nil))
	te.Sh = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "launchctl" && len(args) > 0 && args[0] == "print" {
			return []byte("\tstate = running\n\tlast exit code = (never exited)\n"), nil
		}
		return nil, nil
	}
	te.run("doctor")
	if !strings.Contains(te.out.String(), "loaded, last exit code (never exited)") {
		t.Fatalf("%s", te.out)
	}
}

func TestDoctorReportsAHomeWithoutTheHooks(t *testing.T) {
	root, user := tempWorld(t)
	te := newTestEnv(t, &fake.Manager{})
	te.Home = user
	home := filepath.Join(root, "work-home")
	writeFile(t, filepath.Join(home, "settings.json"), `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"curl localhost:$ORCA_AGENT_HOOK_PORT"}]}]}}`)
	cfg := te.cfg
	cfg.Projects = []config.Project{{Match: "acme/*", AgentHome: home}}
	find := func(name string) check {
		for _, c := range te.homeChecks(context.Background(), cfg) {
			if c.name == name {
				return c
			}
		}
		t.Fatalf("no check %q", name)
		return check{}
	}
	if c := find("agent home " + home + ": hook Stop"); c.mark != markMissing || c.fix != brand.Name+" install --home "+home {
		t.Fatalf("%+v", c)
	}
	if c := find("agent home " + home + ": Orca status hook"); c.mark != markInfo || !strings.Contains(c.info, "competes") {
		t.Fatalf("%+v", c)
	}
	if c := find("agent home " + filepath.Join(user, ".claude")); c.mark != markInfo || !strings.Contains(c.info, "does not exist") {
		t.Fatalf("an absent home: %+v", c)
	}
	if c := find("skills in " + home); c.mark != markMissing || c.fix != find("agent home "+home+": hook Stop").fix {
		t.Fatalf("one home, two fixes: %+v", c)
	}
	if code := te.run("install", "--home", home); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	for _, entry := range []string{"MCP server " + brand.Name, "hook Stop", "hook SessionStart", "permission " + brand.ToolPrefix() + "turn_end"} {
		if c := find("agent home " + home + ": " + entry); c.mark != markOK {
			t.Errorf("%+v", c)
		}
	}
	if c := find("skills in " + home); c.mark != markOK {
		t.Errorf("%+v", c)
	}
}

func TestDoctorSkillDiffersFromRendered(t *testing.T) {
	root, _ := tempWorld(t)
	home := filepath.Join(root, "home")
	mkdir(t, home)
	te := newTestEnv(t, &fake.Manager{})
	te.run("install", "--home", home)
	if c := te.skillCheck(home, "fix"); c.mark != markOK {
		t.Fatalf("%+v", c)
	}
	writeFile(t, filepath.Join(skillsRoot(home), brand.Name+"-review", "SKILL.md"), string(bundledSkill(t, brand.Name+"-review").Render("zz other", Version)))
	if c := te.skillCheck(home, "fix"); c.mark != markMissing || c.fix != "fix" || !strings.Contains(c.info, "from another install") || !strings.Contains(c.info, brand.Name+"-review") {
		t.Fatalf("a skill rendered for another command name passed: %+v", c)
	}
	writeFile(t, filepath.Join(skillsRoot(home), brand.Name+"-review", "SKILL.md"), "mine")
	if c := te.skillCheck(home, "fix"); c.mark != markMissing || !strings.Contains(c.info, "install leaves it: "+brand.Name+"-review ("+brand.Name+" did not write it") {
		t.Fatalf("a skill install did not write: %+v", c)
	}
}

func TestBriefCheck(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	if c := te.briefCheck(te.cfg, nil, nil); c.mark != markInfo || !strings.Contains(c.info, "off") {
		t.Fatalf("off: %+v", c)
	}
	te.cfg.Brief.Enabled = true
	if c := te.briefCheck(te.cfg, &api.BriefStatus{Ready: 3, Pending: 2}, &api.BriefsHealth{}); c.mark != markOK || c.info != "on (fast tier): 3 ready, 2 pending, 0 failed" {
		t.Fatalf("on: %+v", c)
	}
	if c := te.briefCheck(te.cfg, &api.BriefStatus{LastError: "claude is not on the PATH"}, nil); c.mark != markMissing || !strings.Contains(c.info, "claude is not on the PATH") || !strings.Contains(c.fix, "serve.err.log") {
		t.Fatalf("failing: %+v", c)
	}
	until := time.Date(2026, 10, 14, 15, 4, 0, 0, time.UTC)
	c := te.briefCheck(te.cfg, &api.BriefStatus{Ready: 1, LastError: "claude: Credit balance is too low", PausedUntil: until}, nil)
	if c.mark != markMissing || c.info != "claude: Credit balance is too low; on (fast tier): 1 ready, 0 pending, 0 failed; paused until "+until.Local().Format("15:04") {
		t.Fatalf("paused: %+v", c)
	}
	if c := te.briefCheck(te.cfg, nil, nil); c.mark != markMissing || !strings.Contains(c.fix, "serve.err.log") || !strings.HasSuffix(c.fix, restartHint()) {
		t.Fatalf("on but the daemon does not brief: %+v", c)
	}
	if c := te.briefCheck(te.cfg, nil, &api.BriefsHealth{Unavailable: "claude is not installed"}); c.mark != markMissing || !strings.Contains(c.info, "cannot brief: claude is not installed") {
		t.Fatalf("no asker: %+v", c)
	}
	since := time.Date(2026, 10, 14, 13, 40, 0, 0, time.Local)
	open := &api.BriefsHealth{Open: true, Since: since, Error: "rate limited", Next: since.Add(15 * time.Minute)}
	if c := te.briefCheck(te.cfg, &api.BriefStatus{Ready: 1}, open); c.mark != markMissing || !strings.HasPrefix(c.info, "briefs unavailable since 13:40: rate limited; first turns start without the brief until a trial at 13:55 succeeds") {
		t.Fatalf("breaker open: %+v", c)
	}
}

func TestPathChecksLookOnlyForTheConfiguredAgentWithBriefsOn(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	var script string
	te.Sh = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		script = args[len(args)-1]
		return nil, nil
	}
	te.cfg.Agent = "codex"
	te.cfg.Brief.Enabled = true
	te.pathChecks(context.Background(), te.cfg)
	if strings.Contains(script, "claude") || !strings.Contains(script, " codex") {
		t.Fatalf("briefs run on the configured agent, but the check looked for: %q", script)
	}
}

func TestDoctorNamesAgentModelsOfNoSupportedAgent(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.cfg.AgentModels = map[string]map[turns.Tier]string{"claude": {turns.TierFast: "x"}, "cluade": {turns.TierFast: "y"}}
	cs := te.agentModelChecks(te.cfg)
	if len(cs) != 1 || cs[0].mark != markInfo || cs[0].name != "agent_models.cluade" || !strings.Contains(cs[0].fix, "claude") {
		t.Fatalf("%+v", cs)
	}
}

type binKind struct {
	turns.Agent
	bin string
}

func (k binKind) Binary() string { return k.bin }

func TestPathChecksAskTheKindForItsBinary(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	var script string
	te.Sh = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		script = args[len(args)-1]
		return nil, nil
	}
	te.Agents["claude"] = binKind{Agent: te.Agents["claude"], bin: "claude-bin"}
	te.withEcho()
	te.cfg.Projects = []config.Project{{Match: "oss/*", Agent: "echo"}, {Match: "acme/*", Agent: "claude"}}
	te.pathChecks(context.Background(), te.cfg)
	if script != "command -v gh orca git claude-bin cat" {
		t.Fatalf("the check looked for: %q", script)
	}
}

func TestUnknownProjectAgentIsNamed(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.withEcho()
	te.cfg.Projects = []config.Project{{Match: "acme/*", Agent: "nope"}, {Match: "oss/*", Agent: "echo"}}
	checks := te.projectChecks(te.cfg)
	if len(checks) != 1 {
		t.Fatalf("checks %+v", checks)
	}
	c := checks[0]
	if c.mark != markMissing || c.name != "project acme/* agent" || !strings.Contains(c.info, `agent "nope" is not supported`) ||
		c.fix != "remove agent from the project or use one of: claude, echo" {
		t.Fatalf("check %+v", c)
	}
}

func TestAgentHomeOfAnotherKindIsNamed(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.withEcho()
	claudeHome, echoHome := filepath.Join(t.TempDir(), "claude-home"), t.TempDir()
	writeFile(t, filepath.Join(claudeHome, ".claude.json"), `{}`)
	te.cfg.Projects = []config.Project{
		{Match: "oss/*", Agent: "echo", AgentHome: claudeHome},
		{Match: "acme/*", AgentHome: claudeHome},
		{Match: "other/*", Agent: "echo", AgentHome: echoHome},
	}
	checks := te.projectChecks(te.cfg)
	if len(checks) != 1 {
		t.Fatalf("checks %+v", checks)
	}
	if c := checks[0]; c.mark != markMissing || c.name != "project oss/* agent_home" || c.fix != "set agent_home to a echo home" {
		t.Fatalf("check %+v", c)
	}
}

func TestDoctorLooksUpTheManagersBinary(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	var script string
	te.Sh = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		script = args[len(args)-1]
		return nil, nil
	}
	te.pathChecks(context.Background(), te.cfg)
	if script != "command -v gh orca git claude" {
		t.Fatalf("orca: %q", script)
	}
	te.Managers = map[string]ManagerEntry{"zed": {Binary: "zed-cli"}, "none": {}}
	for manager, want := range map[string]string{"zed": "command -v gh zed-cli git claude", "none": "command -v gh git claude"} {
		te.cfg.WorkspaceManagers = []string{manager}
		te.pathChecks(context.Background(), te.cfg)
		if script != want {
			t.Errorf("%s: %q, want %q", manager, script, want)
		}
	}
}

func TestDoctorNamesTheManagerItReaches(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]string{"orca": "  [ok     ] orca reachable\n", "zed": "  [missing] zed reachable: down\n            fix with: open the Zed app\n"} {
		m := &fake.Manager{ManagerName: name}
		if name == "zed" {
			m.Unreachable = errors.New("down")
		}
		te := newTestEnv(t, m)
		te.Managers = map[string]ManagerEntry{"orca": {Display: "Orca"}, "zed": {Display: "Zed"}}
		te.cfg.WorkspaceManagers = []string{name}
		te.run("doctor")
		if !strings.Contains(te.out.String(), want) {
			t.Errorf("%s: doctor lacks %q:\n%s", name, want, te.out)
		}
	}
}

func TestDoctorPrintsOrcasColumns(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{ManagerName: "orca"})
	te.run("doctor")
	for _, want := range []string{
		"views.orca.columns.working=reviewing views.orca.columns.working-fallback=in-review views.orca.columns.ready=ready-to-merge github.automated_reviewers=",
		"  [info   ] board columns reviewing, needs-me, reviewed, in-review, ready-to-merge, in-progress, completed: not verifiable without writing",
	} {
		if !strings.Contains(te.out.String(), want) {
			t.Errorf("doctor lacks %q:\n%s", want, te.out)
		}
	}
}

func TestDoctorLoadJSON(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	want := api.MeterReport{
		Since:  time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC),
		At:     time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC),
		Load:   api.Load{Stats: []api.MeterStat{{Res: "sql", Calls: 3}}},
		Scopes: []api.MeterScope{{Name: "job:ingest:github", Runs: 1, Stats: []api.MeterStat{{Res: "sql", Op: "select", Calls: 3}}}},
	}
	te.Socket = serveSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != api.AdminMeter {
			_ = json.NewEncoder(w).Encode(api.Health{Version: "dev"})
			return
		}
		_ = json.NewEncoder(w).Encode(want)
	}))
	if code := te.run("doctor", "--load", "--json"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	var got api.MeterReport
	if err := json.Unmarshal(te.out.Bytes(), &got); err != nil {
		t.Fatalf("%v:\n%s", err, te.out)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	te.out.Reset()
	if code := te.run("doctor", "--load"); code != 0 || !strings.Contains(te.out.String(), "job:ingest:github: 1 runs, 3 sql") {
		t.Fatalf("exit %d:\n%s", code, te.out)
	}
}

func TestDoctorLoadForAPersonGroupsScopesAndNestsChildren(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	rep := api.MeterReport{
		Since: time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC),
		At:    time.Date(2026, 10, 8, 11, 0, 0, 0, time.UTC),
		Scopes: []api.MeterScope{
			{Name: "job:sweep", Runs: 4, WallMS: 90_000, Stats: []api.MeterStat{{Res: "git", Calls: 40, Spawns: 40}, {Res: "sql", Calls: 12_000}}},
			{Name: "job:sweep/facts:workspace", Runs: 9, Stats: []api.MeterStat{{Res: "git", Calls: 20, Spawns: 20}}},
			{Name: "route:GET /api/plan", Runs: 2, Stats: []api.MeterStat{{Res: "sql", Calls: 8}}},
			{Name: "startup", Runs: 1},
		},
		Recent: &api.MeterWindow{Since: time.Date(2026, 10, 8, 10, 50, 0, 0, time.UTC), Scopes: []api.MeterScope{
			{Name: "job:sweep", Runs: 5, Stats: []api.MeterStat{{Res: "git", Calls: 10, Spawns: 10}}},
			{Name: "job:sweep/facts:workspace", Runs: 5, Stats: []api.MeterStat{{Res: "git", Calls: 8, Spawns: 8}}},
		}},
	}
	te.Socket = serveSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != api.AdminMeter {
			_ = json.NewEncoder(w).Encode(api.Health{Version: "dev"})
			return
		}
		_ = json.NewEncoder(w).Encode(rep)
	}))
	if code := te.run("doctor", "--load", "--color", "always"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	out := termfmt.Plain(te.out.String())
	for _, want := range []string{"Jobs", "Routes", "Other", "\n    facts:workspace ", "\n  sweep ", "1m30s", "12.0k", "rates over the last 10m", "60/h", "job:sweep 100%", "GET /api/plan"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "agent") {
		t.Errorf("shows a resource no scope spent:\n%s", out)
	}
}

func TestDoctorFlagsMissingRestricted(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		help string
		want int
	}{{"  --tools <tools...>\n", 1}, {"  --restricted   Restricted mode\n", 0}} {
		te := newTestEnv(t, &fake.Manager{})
		te.Agents["claude"] = claude.Agent{AskSh: func(_ context.Context, _ string, _ []string, _, _ string, args ...string) ([]byte, error) {
			if args[0] == "--version" {
				return []byte("2.0.1 (Claude Code)\n"), nil
			}
			return []byte(c.help), nil
		}}
		te.Socket = serveJSON(t, map[string]any{"GET /api/health": api.Health{Version: "v1.1.0"}})
		te.run("doctor")
		out := te.out.String()
		if got := strings.Count(out, "--restricted"); got != c.want || c.want == 1 && !strings.Contains(out, "2.0.1") {
			t.Fatalf("help %q: --restricted named %d times, want %d:\n%s", c.help, got, c.want, out)
		}
	}
}

func TestDoctorNamesTheUnknownConfigKeys(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	writeFile(t, te.ConfigPath, `{"gate": {"timeout": "60s"}}`)
	te.LoadConfig = func() (config.Config, error) { return config.LoadFile(te.ConfigPath, Defaults()) }
	te.run("doctor")
	if out := te.out.String(); !strings.Contains(out, te.ConfigPath+": unknown key gate") || !strings.Contains(out, "remove or rename those keys") {
		t.Fatalf("%s", out)
	}
}

func TestDoctorPrintsBudgetScopes(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.run("doctor")
	if want := "turns.per_unit_per_day=5 (github) turns.per_day=20 (entry, session, slack, workspace)"; !strings.Contains(te.out.String(), want) {
		t.Errorf("doctor lacks %q:\n%s", want, te.out)
	}
}
