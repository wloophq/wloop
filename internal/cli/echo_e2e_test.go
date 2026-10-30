package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/agent/echo"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/adapter/view/testsink"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/client/harness"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/lock"
	"github.com/wloophq/wloop/internal/core/setup"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/core/views"
	"github.com/wloophq/wloop/internal/daemon"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/plugins/source/fakesource"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
	"github.com/wloophq/wloop/internal/workspace"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// connectingManager is the fake manager whose started sessions open the loop's
// MCP connection, as an agent with the MCP registration does.
type connectingManager struct {
	*fake.Manager
	e *Env
}

func (m connectingManager) StartAgentSession(ctx context.Context, req workspace.StartRequest) (string, error) {
	handle, err := m.Manager.StartAgentSession(ctx, req)
	if err == nil {
		err = m.e.Turns.MCPSeen(ctx, handle)
	}
	return handle, err
}

var providerTokens = regexp.MustCompile(`(?i)claude|CLAUDE_|ORCA_|mcp__|\borca\b`)

// files maps each regular file under dir to its size and modification time.
func files(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out[p] = fmt.Sprintf("%d %s", info.Size(), info.ModTime())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestEchoAgentRunsWithoutClaudeOrOrca(t *testing.T) {
	root := t.TempDir()
	user := filepath.Join(root, "user")
	t.Setenv("HOME", user)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	echoHome := filepath.Join(root, ".echo")
	mkdir(t, user, echoHome)

	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
	te := newTestEnv(t, m)
	ws := connectingManager{m, te.Env}
	te.WS = workspace.NewSet(ws)
	te.Home = user
	env := map[string]string{}
	te.Getenv = func(k string) string { return env[k] }
	var scripts []string
	te.Sh = func(_ context.Context, name string, args ...string) ([]byte, error) {
		scripts = append(scripts, name+" "+strings.Join(args, " "))
		return nil, fmt.Errorf("no shell in tests")
	}
	te.Agents = map[string]turns.Agent{"echo": &echo.Agent{}}
	te.Homes = map[string]setup.HomeKind{"echo": echo.HomeKind{Root: root}}
	te.Harness = harness.Set{
		SessionEnvs:  map[string]harness.SessionEnv{"fake": fake.SessionEnv{}},
		Agents:       map[string]harness.Protocol{"echo": echo.Protocol{}},
		DefaultAgent: "echo",
	}
	sessions := turnstest.NewSessions()
	sessions.Put("ws-1", turns.Terminal{Session: "term-ws-1", Key: "tab-1", Title: "review"})
	sessions.SetAgents(turns.AgentObservation{WorkspaceID: "ws-1", TerminalKey: "tab-1", State: dturn.AgentDone})
	sink := testsink.New("fake")
	te.Managers = map[string]ManagerEntry{"fake": {
		Display: "Fake", Binary: "true", SessionVar: fake.HandleVar, Sink: "fake",
		Columns: map[string]string{config.ColumnWorking: "doing", config.ColumnWorkingFallback: "doing-too", config.ColumnNeedsMe: "asks", config.ColumnReviewed: "done", config.ColumnReady: "shippable", config.ColumnInProgress: "started", config.ColumnInReview: "checking", config.ColumnCompleted: "closed"},
		New:     func() workspace.Manager { return ws },
		Ports: func(context.Context, workspace.Manager, prrow.ReportedPRs, config.Config, *change.Log) (managerSessions, []views.Sink, error) {
			return handleSessions{"fake", sessions}, []views.Sink{{ViewSink: sink, Placements: sink}}, nil
		},
	}}
	te.cfg.Agent = "echo"
	te.cfg.WorkspaceManagers = []string{"fake"}
	te.cfg = withoutWindows(te.cfg)
	te.gh.basics = openBasics
	te.git.branch["/worktrees/REVIEW feat/x"] = "me/REVIEW-feat-x"
	now := testNow
	var clock sync.Mutex
	te.Now = func() time.Time { clock.Lock(); defer clock.Unlock(); return now }

	db, err := sqlitetest.Open(filepath.Join(root, "state", storeFile), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var logMu sync.Mutex
	var logs []string
	logf := func(format string, args ...any) {
		logMu.Lock()
		defer logMu.Unlock()
		logs = append(logs, fmt.Sprintf(format, args...))
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	te.Rounds = sqlite.NewRounds(db)
	deps := te.serveDeps(ctx, db, te.cfg, logf, &lock.Mutex{}, &daemon.AppState{})
	te.Rec = storeRecorder{te.Store}
	if _, err := deps.Startup(ctx); err != nil {
		t.Fatal(err)
	}
	deps.Startup = nil
	srv := daemon.New(deps)
	te.Socket = serveSocket(t, srv.Handler())

	var printed []string
	run := func(args ...string) int {
		t.Helper()
		te.out.Reset()
		te.err.Reset()
		code := Run(ctx, te.Env, args)
		printed = append(printed, te.out.String(), te.err.String())
		return code
	}
	hook := func(sub, payload string) (int, string) {
		t.Helper()
		var code int
		withStdin(t, payload, func() { code = run("hook", sub, "--agent", "echo") })
		return code, te.out.String()
	}

	if code := run("install", "--dry-run"); code != 0 || !strings.Contains(te.out.String(), "would write "+filepath.Join(echoHome, brand.Name+".json")) {
		t.Fatalf("install --dry-run: exit %d: %s%s", code, te.out, te.err)
	}
	tmp := filepath.Dir(root)
	before := files(t, tmp)
	if code := run("install"); code != 0 {
		t.Fatalf("install: exit %d: %s", code, te.err)
	}
	var skills []string
	for p, stamp := range files(t, tmp) {
		switch {
		case before[p] == stamp:
		case p == filepath.Join(echoHome, brand.Name+".json"), p == te.mcpKeyPath():
		case strings.HasPrefix(p, filepath.Join(echoHome, "skills")+"/"), strings.HasPrefix(p, filepath.Join(user, ".agents", "skills")+"/"):
			skills = append(skills, p)
		default:
			t.Errorf("install wrote %s, outside the echo home and the shared skills", p)
		}
	}
	if len(skills) == 0 {
		t.Errorf("install wrote no skill")
	}
	written, err := os.ReadFile(filepath.Join(echoHome, brand.Name+".json"))
	if err != nil {
		t.Fatal(err)
	}

	if code := run("doctor"); code != 1 || !strings.HasSuffix(te.err.String(), "error: 5 check(s) pending\n") {
		t.Errorf("doctor: exit %d: %s", code, te.err)
	}
	doctor := te.out.String()
	for _, want := range []string{"[ok     ] fake reachable", "[ok     ] agent home " + echoHome + ": hook stop", "[ok     ] skills in " + echoHome,
		"views.fake.columns.working=doing views.fake.columns.working-fallback=doing-too views.fake.columns.ready=shippable",
		"[info   ] board columns doing, asks, done, doing-too, shippable, started, checking, closed:"} {
		if !strings.Contains(doctor, want) {
			t.Errorf("doctor lacks %q:\n%s", want, doctor)
		}
	}
	var failed []string
	for _, line := range strings.Split(doctor, "\n") {
		if name, ok := strings.CutPrefix(line, "  [missing] "); ok {
			failed = append(failed, strings.SplitN(name, ":", 2)[0])
		}
	}
	wantFailed := []string{"LaunchAgent", "gh on the login shell's PATH", "true on the login shell's PATH", "git on the login shell's PATH", "cat on the login shell's PATH"}
	if !slicesEqual(failed, wantFailed) {
		t.Errorf("doctor failed %q, want %q:\n%s", failed, wantFailed, doctor)
	}
	if !strings.Contains(strings.Join(scripts, "\n"), "command -v gh true git cat") {
		t.Errorf("doctor's PATH check: %q", scripts)
	}

	if code := run("review", "https://github.com/acme/api/pull/7"); code != 0 {
		t.Fatalf("review: exit %d: %s", code, te.err)
	}
	if len(m.Prompts) != 1 || !slicesEqual(launches(m.Commands), []string{"cat"}) {
		t.Fatalf("review started %q and typed %+v", m.Commands, m.Prompts)
	}
	_, _, url, round := promptOf(t, m.Prompts[0].Text)
	handle := m.Prompts[0].Handle
	env[fake.HandleVar], env[fake.WorktreeVar], env[echo.SessionVar], env[echo.HomeVar] = handle, "ws-1", "s-1", echoHome

	if code, out := hook("session-start", `{"id":"s-1"}`); code != 0 || out != "" {
		t.Fatalf("session-start: exit %d: %q %s", code, out, te.err)
	}
	if code := run("instructions", "review", "--turn", round, url); code != 0 || !strings.Contains(te.out.String(), round) {
		t.Fatalf("instructions: exit %d: %s%s", code, te.out, te.err)
	}
	if code, out := hook("stop", `{"id":"s-1","prompt":"p-1","last":"looked"}`); code != 0 || !strings.HasPrefix(out, "BLOCK ") {
		t.Fatalf("stop owing a record: exit %d: %q %s", code, out, te.err)
	}
	file := filepath.Join(root, "record.json")
	writeFile(t, file, `{"state":"settled","summary":"nothing new"}`)
	if code := run("turn", "end", url, "--turn", round, "--file", file); code != 0 {
		t.Fatalf("turn end: exit %d: %s", code, te.err)
	}
	if code, out := hook("stop", `{"id":"s-1","prompt":"p-2","last":"recorded"}`); code != 0 || out != "" {
		t.Fatalf("stop after the record: exit %d: %q %s", code, out, te.err)
	}

	if err := te.Turns.KeepSessions(ctx); err != nil {
		t.Fatal(err)
	}
	clock.Lock()
	now = now.Add(2 * time.Hour)
	clock.Unlock()
	if err := te.Turns.KeepSessions(ctx); err != nil {
		t.Fatal(err)
	}
	id, _, err := te.Turns.Registry.(turns.Handles).SessionOf(ctx, "fake", handle)
	if err != nil {
		t.Fatal(err)
	}
	if sess, ok, err := te.Turns.Registry.Session(ctx, id); err != nil || !ok || sess.IdleSince.IsZero() {
		t.Fatalf("the sessions keeper did not see the fake's agent of %s done: %+v %v %v", handle, sess, ok, err)
	}

	logMu.Lock()
	daemonLog := strings.Join(logs, "\n")
	logMu.Unlock()
	if strings.Contains(daemonLog, "has no sessions adapter") {
		t.Errorf("daemon log:\n%s", daemonLog)
	}
	var envEntries []string
	for k, v := range env {
		envEntries = append(envEntries, k+"="+v)
	}
	for what, texts := range map[string][]string{
		"argv":         append(append([]string{}, m.Commands...), scripts...),
		"env":          envEntries,
		"written file": {string(written)},
		"printed line": printed,
		"daemon log":   {daemonLog},
		"typed prompt": {m.Prompts[0].Text},
	} {
		for _, s := range texts {
			s = regexp.MustCompile(`TestEchoAgentRunsWithoutClaudeOrOrca\d*`).ReplaceAllString(s, "<test>")
			if loc := providerTokens.FindStringIndex(s); loc != nil {
				t.Errorf("%s names a provider (%q): %s", what, s[loc[0]:loc[1]], s)
			}
		}
	}
}

func slicesEqual(a, b []string) bool { return strings.Join(a, "\x00") == strings.Join(b, "\x00") }

// threadTurnModule is threadModule with the fake thread's turn payload.
func threadTurnModule(th unit.Unit) compiledPlugin {
	p := threadModule(th)
	build := p.Build
	p.Build = func(cfg config.Config, d moduleDeps) (pluginModule, error) {
		m, err := build(cfg, d)
		m.Payload = fakesource.Payloads{}
		return m, err
	}
	return p
}

func TestEchoAgentRunsAThreadTurn(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "user"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	echoHome := filepath.Join(root, ".echo")
	mkdir(t, filepath.Join(root, "user"), echoHome)

	m := &fake.Manager{
		RepoList:      []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}},
		WorkspaceList: []wsdomain.Workspace{{ID: "ws-main", RepoID: "r1", Path: "/src/api", DisplayName: "api", HostID: wsdomain.LocalHost, IsMain: true}},
	}
	te := newTestEnv(t, m)
	ws := connectingManager{m, te.Env}
	te.WS = workspace.NewSet(ws)
	env := map[string]string{}
	te.Getenv = func(k string) string { return env[k] }
	te.Agents = map[string]turns.Agent{"echo": &echo.Agent{}}
	te.Homes = map[string]setup.HomeKind{"echo": echo.HomeKind{Root: root}}
	te.Harness = harness.Set{
		SessionEnvs:  map[string]harness.SessionEnv{"fake": fake.SessionEnv{}},
		Agents:       map[string]harness.Protocol{"echo": echo.Protocol{}},
		DefaultAgent: "echo",
	}
	sessions := turnstest.NewSessions()
	sink := testsink.New("fake")
	te.Managers = map[string]ManagerEntry{"fake": {
		Display: "Fake", Binary: "true", SessionVar: fake.HandleVar, Sink: "fake",
		New: func() workspace.Manager { return ws },
		Ports: func(context.Context, workspace.Manager, prrow.ReportedPRs, config.Config, *change.Log) (managerSessions, []views.Sink, error) {
			return handleSessions{"fake", sessions}, []views.Sink{{ViewSink: sink, Placements: sink}}, nil
		},
	}}
	te.cfg.Agent = "echo"
	te.cfg.WorkspaceManagers = []string{"fake"}
	te.cfg = withoutWindows(te.cfg)
	te.cfg.Placement = []config.PlacementRule{{Match: "thread:*", Repo: "acme/api", Checkout: "main"}}
	userProfile(t, te, map[string]string{"intents/discuss/intent.toml": discussIntent, "intents/discuss/instructions.md.tmpl": discussBody})
	saved := plugins
	plugins = append(slices.Clone(plugins), threadTurnModule(thread1))
	defer func() { plugins = saved }()

	db, err := sqlitetest.Open(filepath.Join(root, "state", storeFile), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	te.Rounds = sqlite.NewRounds(db)
	deps := te.serveDeps(ctx, db, te.cfg, t.Logf, &lock.Mutex{}, &daemon.AppState{})
	te.Rec = storeRecorder{te.Store}
	if _, err := deps.Startup(ctx); err != nil {
		t.Fatal(err)
	}
	deps.Startup = nil
	te.Socket = serveSocket(t, daemon.New(deps).Handler())

	run := func(args ...string) int {
		t.Helper()
		te.out.Reset()
		te.err.Reset()
		return Run(ctx, te.Env, args)
	}
	hook := func(sub, payload string) (int, string) {
		t.Helper()
		var code int
		withStdin(t, payload, func() { code = run("hook", sub, "--agent", "echo") })
		return code, te.out.String()
	}

	if _, err := client.New(te.Socket).Action(ctx, thread1, "discuss", api.ActionRequest{}); err != nil {
		t.Fatalf("discuss: %v\n%s", err, te.err)
	}
	if len(m.Prompts) != 1 || !slicesEqual(launches(m.Commands), []string{"cat"}) {
		t.Fatalf("discuss started %q and typed %+v", m.Commands, m.Prompts)
	}
	round := discussRound(t, m.Prompts[0].Text)
	handle := m.Prompts[0].Handle
	env[fake.HandleVar], env[fake.WorktreeVar], env[echo.SessionVar], env[echo.HomeVar] = handle, "ws-main", "s-1", echoHome

	if code, out := hook("session-start", `{"id":"s-1"}`); code != 0 || out != "" {
		t.Fatalf("session-start: exit %d: %q %s", code, out, te.err)
	}
	if code := run("instructions", "discuss", "--turn", round, thread1.String()); code != 0 {
		t.Fatalf("instructions: exit %d: %s%s", code, te.out, te.err)
	}
	instructions := te.out.String()
	if !strings.Contains(instructions, "Discuss thread 1700000000.1") || strings.Contains(instructions, "pull request") {
		t.Fatalf("instructions:\n%s", instructions)
	}
	if code, out := hook("stop", `{"id":"s-1","prompt":"p-1","last":"looked"}`); code != 0 || !strings.HasPrefix(out, "BLOCK ") {
		t.Fatalf("stop owing a record: exit %d: %q %s", code, out, te.err)
	}
	file := filepath.Join(root, "record.json")
	writeFile(t, file, `{"state":"done","summary":"answered in the thread"}`)
	if code := run("turn", "end", thread1.String(), "--turn", round, "--file", file); code != 0 || !strings.Contains(te.out.String(), "Recorded done for "+thread1.String()) {
		t.Fatalf("turn end: exit %d: %s%s", code, te.out, te.err)
	}
	if code, out := hook("stop", `{"id":"s-1","prompt":"p-2","last":"recorded"}`); code != 0 || out != "" {
		t.Fatalf("stop after the record: exit %d: %q %s", code, out, te.err)
	}
	books, err := te.Turns.Store.Books(ctx, thread1)
	if err != nil {
		t.Fatal(err)
	}
	b := books[thread1]
	if len(b.Turns) != 1 || b.Turns[0].ID != round || b.Turns[0].State != dturn.Done || b.Turns[0].StartedAt.IsZero() {
		t.Fatalf("turns %+v", b.Turns)
	}
}
