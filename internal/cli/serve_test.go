package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"
	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/adapter/view/testsink"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/ingest"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/core/views"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/event"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/record"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/view"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/github/githubtest"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
	"github.com/wloophq/wloop/internal/source"
	"github.com/wloophq/wloop/internal/transport/mcp"
	"github.com/wloophq/wloop/internal/workspace/fake"
	wsorca "github.com/wloophq/wloop/internal/workspace/orca"
)

type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestServeDegradedUntilTheConfigLoads(t *testing.T) {
	defer func(d time.Duration) { configRetry = d }(configRetry)
	configRetry = 20 * time.Millisecond

	te := newTestEnv(t, &fake.Manager{Unreachable: errors.New("orca is not running")})
	dir := shortDir(t)
	te.StateDir = dir
	te.Socket = filepath.Join(dir, "daemon.sock")
	te.Out, te.Err = &syncBuffer{}, &syncBuffer{}
	seedR7(t, te)
	var broken atomic.Bool
	broken.Store(true)
	te.LoadConfig = func() (config.Config, error) {
		if broken.Load() {
			return config.Config{}, errors.New("config.json: unexpected end of JSON input")
		}
		return Defaults(), nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runServe(ctx, te.Env, []string{"--mcp-port", strconv.Itoa(freePort(t))}) }()
	c := client.New(te.Socket)

	var h api.Health
	eventually(t, "the config error in health", func() bool {
		var err error
		h, err = c.Health(ctx)
		return err == nil && slices.ContainsFunc(h.Warnings, func(w string) bool { return strings.Contains(w, "unexpected end of JSON input") })
	})
	if names := jobNames(h); !slices.Equal(names, []string{"retain", "sweep", "usage"}) {
		t.Fatalf("degraded jobs %v", names)
	}
	_, err := c.Action(ctx, r7.Unit(), "review", api.ActionRequest{Source: "cli"})
	if err == nil || !strings.Contains(err.Error(), "unexpected end of JSON input") {
		t.Fatalf("review in degraded serve: %v", err)
	}

	broken.Store(false)
	eventually(t, "the restart with collection", func() bool {
		h, err := c.Health(ctx)
		return err == nil && slices.Equal(jobNames(h), []string{"due", "extract:slack", "fast", "ingest:github", "ingest:slack", "retain", "slow", "sweep", "usage"})
	})
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not stop")
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestServeNoJobsDegradedRunsNoJob(t *testing.T) {
	defer func(d time.Duration) { configRetry = d }(configRetry)
	configRetry = 20 * time.Millisecond
	te := newServeEnv(t)
	var broken atomic.Bool
	broken.Store(true)
	te.LoadConfig = func() (config.Config, error) {
		if broken.Load() {
			return config.Config{}, errors.New("config.json: unexpected end of JSON input")
		}
		cfg := Defaults()
		cfg = withoutWindows(cfg)
		return cfg, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runServe(ctx, te.Env, []string{"--no-jobs", "--mcp-port", strconv.Itoa(freePort(t))}) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("serve: %v", err)
		}
	}()
	if h := waitHealth(t, te.Env, "the degraded daemon", func(api.Health) bool { return true }); len(h.Jobs) != 0 {
		t.Fatalf("degraded --no-jobs runs %v", jobNames(h))
	}
	broken.Store(false)
	eventually(t, "the restart after the config loads", func() bool {
		return strings.Contains(te.Err.(*syncBuffer).String(), "config loaded")
	})
	if h := waitHealth(t, te.Env, "the restarted daemon", func(api.Health) bool { return true }); len(h.Jobs) != 0 {
		t.Fatalf("restarted --no-jobs runs %v", jobNames(h))
	}
}

func TestServeNoJobsRegistersNone(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	storeDB, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer storeDB.Close()
	te.cfg.Brief.Enabled = true
	te.noJobs = true
	d := serveDeps(context.Background(), te.Env, storeDB, te.cfg, func(string, ...any) {}, &sync.Mutex{}, nil)
	if d.Collector != nil || d.Sweep != nil || d.Brief != nil || d.Usage != nil || d.Retain != nil {
		t.Fatalf("--no-jobs left a job: collector %v sweep %v brief %v usage %v retain %v", d.Collector != nil, d.Sweep != nil, d.Brief != nil, d.Usage != nil, d.Retain != nil)
	}
	if d.Actions == nil || d.PRs == nil {
		t.Fatal("--no-jobs dropped the APIs")
	}
}

func TestServeNoJobsWritesNoCard(t *testing.T) {
	t.Parallel()
	for _, noJobs := range []bool{false, true} {
		t.Run(fmt.Sprintf("no-jobs=%v", noJobs), func(t *testing.T) {
			t.Parallel()
			te := newTestEnv(t, &fake.Manager{})
			storeDB, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer storeDB.Close()
			te.cfg = withoutWindows(te.cfg)
			te.noJobs = noJobs
			sink := testsink.New("test")
			te.ports = func(*inventory.Store, config.Config) (turns.Sessions, []views.Sink) {
				return turnstest.NewSessions(), []views.Sink{{ViewSink: sink, Placements: sink}}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			d := serveDeps(ctx, te.Env, storeDB, te.cfg, func(string, ...any) {}, &sync.Mutex{}, nil)
			ref := pr.Ref(r7)
			if _, err := d.Turns.Send(ctx, turns.SendRequest{Unit: ref.Unit(), Intent: dturn.Intent(dintent.Review), Turn: "abc", Trigger: dturn.Automated, Head: "c2", Records: true}); err != nil {
				t.Fatal(err)
			}
			if _, err := d.Turns.Start(ctx, "term_1", "abc"); err != nil {
				t.Fatal(err)
			}
			rec := record.Record{State: record.Done, Summary: "approved", Outcome: &record.Outcome{Verdict: "APPROVE", URL: r7.URL() + "#pullrequestreview-1"}}
			if _, err := d.Turns.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "abc", Via: "mcp", Record: rec}); err != nil {
				t.Fatal(err)
			}
			if !noJobs {
				eventually(t, "the card written", func() bool { c, _, _ := sink.Card(ref.Unit()); return c == sink.Column(view.Done) })
				return
			}
			time.Sleep(100 * time.Millisecond)
			if got := sink.Batches(); len(got) != 0 {
				t.Fatalf("--no-jobs wrote cards: %+v", got)
			}
		})
	}
}

func mcpPost(t *testing.T, url, key, sid, body string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set(api.MCPKeyHeader, key)
	}
	req.Header.Set(api.SessionHeader, "term_probe")
	if sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

// seedSession stores, in the store serve opens, a session whose process
// the fake manager addresses by its id.
func seedSession(t *testing.T, te *testEnv, id string) {
	t.Helper()
	db, err := sqlitetest.Open(filepath.Join(te.StateDir, storeFile), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := sqlite.New(db)
	if err := store.SaveSession(context.Background(), dturn.Session{ID: id, Manager: "fake", WorkspaceID: "ws-1", StartedAt: testNow}); err != nil {
		t.Fatal(err)
	}
	if err := store.BindHandle(context.Background(), id, "fake", id, "", testNow); err != nil {
		t.Fatal(err)
	}
}

func TestServeNoJobsAnswersMCP(t *testing.T) {
	te := newServeEnv(t)
	seedSession(t, te, "term_probe")
	port := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runServe(ctx, te.Env, []string{"--no-jobs", "--mcp-port", strconv.Itoa(port)}) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("serve: %v", err)
		}
	}()
	h := waitHealth(t, te.Env, "the daemon", func(api.Health) bool { return true })
	if len(h.Jobs) != 0 {
		t.Fatalf("--no-jobs runs %v", jobNames(h))
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
	const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`
	if resp, body := mcpPost(t, url, "", "", initialize); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no key file, no key: %d %s", resp.StatusCode, body)
	}
	key, err := te.ensureMCPKey(false)
	if err != nil {
		t.Fatal(err)
	}
	if resp, body := mcpPost(t, url, "", "", initialize); resp.StatusCode != http.StatusUnauthorized || !strings.Contains(body, api.MCPKeyHeader) {
		t.Fatalf("without the key: %d %s", resp.StatusCode, body)
	}
	resp, body := mcpPost(t, url, key, "", initialize)
	sid := resp.Header.Get("Mcp-Session-Id")
	if resp.StatusCode != http.StatusOK || sid == "" {
		t.Fatalf("initialize: %d %s", resp.StatusCode, body)
	}
	_, body = mcpPost(t, url, key, sid, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"turn_end","arguments":{"turn":"abc","record":{"state":"settled","summary":"nothing new"}}}}`)
	if !strings.Contains(body, `"isError":true`) || !strings.Contains(body, `no turn \"abc\"`) {
		t.Fatalf("tools/call: %s", body)
	}
	if log := te.Err.(*syncBuffer).String(); !strings.Contains(log, "--no-jobs") || !strings.Contains(log, "mcp_seen term_probe") {
		t.Fatalf("log: %s", te.Err.(*syncBuffer).String())
	}
}

func TestServeMCPPortFlagOverridesTheConfig(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	if d := (mcpEndpoint{}).wire(daemon.Deps{}, cfg); d.MCPPort != config.DefaultMCPPort || d.MCP == nil {
		t.Fatalf("default: %d %v", d.MCPPort, d.MCP)
	}
	cfg.MCP.Port = 47811
	if d := (mcpEndpoint{}).wire(daemon.Deps{}, cfg); d.MCPPort != 47811 {
		t.Fatalf("config: %d", d.MCPPort)
	}
	if d := (mcpEndpoint{port: 47899}).wire(daemon.Deps{}, cfg); d.MCPPort != 47899 || d.MCP.(*mcp.Server).Port != 47899 {
		t.Fatalf("flag: %d", d.MCPPort)
	}
}

func jobNames(h api.Health) []string {
	var names []string
	for _, j := range h.Jobs {
		names = append(names, j.Name)
	}
	return names
}

func TestServeDepsShareTheLoopMutex(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	storeDB, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer storeDB.Close()
	var loop sync.Mutex
	logf := func(string, ...any) {}
	noWindows := withoutWindows(Defaults())
	te.cfg = withoutWindows(te.cfg)
	for _, cfg := range []config.Config{noWindows, te.cfg} {
		if d := serveDeps(context.Background(), te.Env, storeDB, cfg, logf, &loop, nil); d.LoopMu != &loop {
			t.Fatal("a restarted daemon would get its own loop mutex")
		}
	}
}

func TestServeViewerTeamsAreTheOnesIBelongTo(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.cfg.GitHub.Scope.Teams = []string{"acme/platform", "acme/search"}
	storeDB, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	store := inventory.New(storeDB)
	defer storeDB.Close()
	store.SetMeta(context.Background(), "scope_resolved", `{"Teams":["acme/platform","acme/search"],"MyTeams":["acme/search"]}`)
	te.cfg = withoutWindows(te.cfg)
	d := serveDeps(context.Background(), te.Env, storeDB, te.cfg, func(string, ...any) {}, &sync.Mutex{}, nil)
	login, teams, err := d.Viewer(context.Background())
	if err != nil || login != "me" || !slices.Equal(teams, []string{"acme/search"}) {
		t.Fatalf("%q %v %v", login, teams, err)
	}
}

func TestViewerReadsNoGitHub(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	storeDB, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	store := inventory.New(storeDB)
	defer storeDB.Close()
	if err := store.SetViewer(context.Background(), unit.GitHub, "stored"); err != nil {
		t.Fatal(err)
	}
	te.gh.viewerErr = errors.New("GitHub must not be asked")
	te.cfg = withoutWindows(te.cfg)
	d := serveDeps(context.Background(), te.Env, storeDB, te.cfg, func(string, ...any) {}, &sync.Mutex{}, nil)
	for range 100 {
		if login, _, err := d.Viewer(context.Background()); err != nil || login != "stored" {
			t.Fatalf("%q %v", login, err)
		}
	}
	te.Env.Store = store
	if login, err := te.viewerLogin(context.Background()); err != nil || login != "stored" {
		t.Fatalf("%q %v", login, err)
	}
	if len(te.gh.calls) != 0 {
		t.Fatalf("GitHub asked: %v", te.gh.calls)
	}
}

// seedR7 puts acme/api#7 in the inventory serve opens, so the daemon needs no
// GitHub read to offer its actions.
func seedR7(t *testing.T, te *testEnv) {
	t.Helper()
	db, err := sqlitetest.Open(filepath.Join(te.StateDir, storeFile), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := inventory.New(db).UpsertPR(context.Background(), dinv.PR{Ref: r7, Title: "Batch export", Author: "ana", State: "OPEN", URL: r7.URL(), Reasons: []string{"review-requested"}}); err != nil {
		t.Fatal(err)
	}
}

// newServeEnv is a daemon environment on a short socket path, with Orca
// unreachable so the sweep does nothing, and the gate off.
func newServeEnv(t *testing.T) *testEnv {
	t.Helper()
	te := newTestEnv(t, &fake.Manager{Unreachable: errors.New("orca is not running")})
	dir := shortDir(t)
	te.StateDir = dir
	te.Socket = filepath.Join(dir, "daemon.sock")
	te.Out, te.Err = &syncBuffer{}, &syncBuffer{}
	te.cfg = withoutWindows(te.cfg)
	te.Exit = func(code int) { t.Errorf("unexpected exit %d", code) }
	return te
}

func waitHealth(t *testing.T, e *Env, what string, cond func(api.Health) bool) api.Health {
	t.Helper()
	var h api.Health
	eventually(t, what, func() bool {
		var err error
		h, err = client.New(e.Socket).Health(context.Background())
		return err == nil && cond(h)
	})
	return h
}

func TestServeRefusesApp(t *testing.T) {
	t.Parallel()
	te := newServeEnv(t)
	err := runServe(context.Background(), te.Env, []string{"--app"})
	if err == nil || !strings.Contains(err.Error(), "-app") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(te.Socket); statErr == nil {
		t.Fatal("serve --app started the daemon")
	}
}

func TestServeDepsBriefOnlyWhenEnabled(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	storeDB, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer storeDB.Close()
	te.cfg = withoutWindows(te.cfg)
	if d := serveDeps(context.Background(), te.Env, storeDB, te.cfg, func(string, ...any) {}, &sync.Mutex{}, nil); d.Brief != nil {
		t.Fatal("briefs while brief.enabled is false")
	}
	te.cfg.Brief.Enabled = true
	if d := serveDeps(context.Background(), te.Env, storeDB, te.cfg, func(string, ...any) {}, &sync.Mutex{}, nil); d.Brief == nil {
		t.Fatal("no briefs while brief.enabled is true")
	}
}

func TestBriefHomeFollowsTheProjectsProfile(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	cfg.Projects = []config.Project{{Match: "me/*", AgentHome: "/home/me/.claude-personal"}}
	home := briefHome(cfg)
	if got := home(github.Ref{Owner: "me", Repo: "lib", Number: 1}); got != "/home/me/.claude-personal" {
		t.Fatalf("project: %q", got)
	}
	if got := home(github.Ref{Owner: "acme", Repo: "api", Number: 1}); got != "" {
		t.Fatalf("no profile: %q", got)
	}
}

func TestNoBriefsForAnAgentWithoutHeadlessCalls(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	storeDB, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer storeDB.Close()
	te.cfg = withoutWindows(te.cfg)
	te.cfg.Brief.Enabled = true
	te.Agents = map[string]turns.Agent{"claude": plainKind{}}
	var logs []string
	if d := serveDeps(context.Background(), te.Env, storeDB, te.cfg, func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }, &sync.Mutex{}, nil); d.Brief != nil {
		t.Fatal("briefs from an agent that answers no headless call")
	}
	if !slices.ContainsFunc(logs, func(l string) bool { return strings.Contains(l, "cannot answer headless calls") }) {
		t.Fatalf("logs %v", logs)
	}
}

func sinksOf(ss ...*testsink.Sink) func(*inventory.Store, config.Config) (turns.Sessions, []views.Sink) {
	return func(*inventory.Store, config.Config) (turns.Sessions, []views.Sink) {
		var out []views.Sink
		for _, s := range ss {
			out = append(out, views.Sink{ViewSink: s, Placements: s})
		}
		return turnstest.NewSessions(), out
	}
}

// projectDone ends a review turn of r7, which every sink shows as done.
func projectDone(t *testing.T, ctx context.Context, d daemon.Deps) {
	t.Helper()
	if _, err := d.Turns.Send(ctx, turns.SendRequest{Unit: r7.Unit(), Intent: dturn.Intent(dintent.Review), Turn: "abc", Trigger: dturn.Automated, Head: "c2", Records: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Turns.Start(ctx, "term_1", "abc"); err != nil {
		t.Fatal(err)
	}
	rec := record.Record{State: record.Done, Summary: "approved", Outcome: &record.Outcome{Verdict: "APPROVE", URL: r7.URL() + "#pullrequestreview-1"}}
	if _, err := d.Turns.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "abc", Via: "mcp", Record: rec}); err != nil {
		t.Fatal(err)
	}
}

func shownDone(s *testsink.Sink) bool {
	c, _, _ := s.Card(pr.Ref(r7).Unit())
	return c == s.Column(view.Done)
}

func TestEverySinkIsWired(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	storeDB, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer storeDB.Close()
	te.cfg = withoutWindows(te.cfg)
	a, b := testsink.New("a"), testsink.New("b")
	te.ports = sinksOf(a, b)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := serveDeps(ctx, te.Env, storeDB, te.cfg, func(string, ...any) {}, &sync.Mutex{}, nil)
	projectDone(t, ctx, d)
	eventually(t, "the card on both sinks", func() bool { return shownDone(a) && shownDone(b) })
}

func TestReloadUpdatesEverySink(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	storeDB, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer storeDB.Close()
	te.cfg = withoutWindows(te.cfg)
	oldA, oldB := testsink.New("a"), testsink.New("b")
	te.ports = sinksOf(oldA, oldB)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var logs []string
	logf := func(f string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		logs = append(logs, fmt.Sprintf(f, args...))
	}
	d := serveDeps(ctx, te.Env, storeDB, te.cfg, logf, &sync.Mutex{}, nil)
	newA, newB, extra := testsink.New("a"), testsink.New("b"), testsink.New("c")
	te.ports = sinksOf(newA, newB, extra)
	serveDeps(ctx, te.Env, storeDB, te.cfg, logf, &sync.Mutex{}, nil)
	projectDone(t, ctx, d)
	eventually(t, "the card on both reloaded sinks", func() bool { return shownDone(newA) && shownDone(newB) })
	if len(oldA.Batches()) != 0 || len(oldB.Batches()) != 0 || len(extra.Batches()) != 0 {
		t.Fatalf("writes after the reload: old a %v, old b %v, new c %v", oldA.Batches(), oldB.Batches(), extra.Batches())
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.ContainsFunc(logs, func(l string) bool { return strings.Contains(l, `view sink "c" is new`) }) {
		t.Fatalf("logs %v", logs)
	}
}

func TestAReloadOntoAnotherManagerSaysToRestart(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{ManagerName: "orca"})
	storeDB, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer storeDB.Close()
	te.cfg = withoutWindows(te.cfg)
	te.ports = sinksOf()
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); <-te.served.done }()
	var mu sync.Mutex
	var logs []string
	logf := func(f string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		logs = append(logs, fmt.Sprintf(f, args...))
	}
	reload := func(manager string) {
		te.cfg.WorkspaceManagers = []string{manager}
		serveDeps(ctx, te.Env, storeDB, te.cfg, logf, &sync.Mutex{}, nil)
	}
	for _, manager := range []string{"orca", "orca", "zed", "zed", "orca"} {
		reload(manager)
	}
	mu.Lock()
	defer mu.Unlock()
	var restarts []string
	for _, l := range logs {
		if strings.Contains(l, "restart the daemon to switch") {
			restarts = append(restarts, l)
		}
	}
	if want := []string{`workspace_managers is ["zed"], the daemon runs ["orca"]; restart the daemon to switch`}; !slices.Equal(restarts, want) {
		t.Fatalf("restart lines %q, want %q", restarts, want)
	}
}

func TestASinkNewSinceAStartWithoutSinksIsLogged(t *testing.T) {
	t.Parallel()
	for _, noJobs := range []bool{false, true} {
		t.Run(fmt.Sprintf("no-jobs=%v", noJobs), func(t *testing.T) {
			t.Parallel()
			te := newTestEnv(t, &fake.Manager{})
			storeDB, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer storeDB.Close()
			te.cfg = withoutWindows(te.cfg)
			te.noJobs = noJobs
			te.ports = sinksOf()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var mu sync.Mutex
			var logs []string
			logf := func(f string, args ...any) {
				mu.Lock()
				defer mu.Unlock()
				logs = append(logs, fmt.Sprintf(f, args...))
			}
			serveDeps(ctx, te.Env, storeDB, te.cfg, logf, &sync.Mutex{}, nil)
			te.ports = sinksOf(testsink.New("late"))
			serveDeps(ctx, te.Env, storeDB, te.cfg, logf, &sync.Mutex{}, nil)
			mu.Lock()
			defer mu.Unlock()
			logged := slices.ContainsFunc(logs, func(l string) bool { return strings.Contains(l, `view sink "late" is new`) })
			if logged == noJobs {
				t.Fatalf("logged %v under --no-jobs=%v: %v", logged, noJobs, logs)
			}
		})
	}
}

func TestUnknownManagerIsLoggedNotSilent(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{ManagerName: "zed"})
	storeDB, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer storeDB.Close()
	te.cfg = withoutWindows(te.cfg)
	te.ports = nil
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var logs []string
	logf := func(f string, a ...any) {
		mu.Lock()
		defer mu.Unlock()
		logs = append(logs, fmt.Sprintf(f, a...))
	}
	d := serveDeps(ctx, te.Env, storeDB, te.cfg, logf, &sync.Mutex{}, nil)
	serveDeps(ctx, te.Env, storeDB, te.cfg, logf, &sync.Mutex{}, nil)
	want := `workspace manager "zed" has no sessions adapter; ` + noSessionsAdapter
	mu.Lock()
	n := 0
	for _, l := range logs {
		if l == want {
			n++
		}
	}
	mu.Unlock()
	if n != 1 {
		t.Fatalf("logged %d times: %v", n, logs)
	}
	if r, ok := d.Sessions.(*turns.Router); !ok || len(r.Managers) != 1 {
		t.Fatalf("sessions %T", d.Sessions)
	} else if _, ok := r.Managers[0].(typeOnly); !ok {
		t.Fatalf("zed's sessions %T", r.Managers[0])
	}
}

type resumeKind struct {
	turns.Agent
	line string
}

func (k resumeKind) Resume(string, string) string { return k.line }

func TestAgentForFollowsTheReloadedConfig(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	storeDB, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer storeDB.Close()
	te.Agents = map[string]turns.Agent{"first": resumeKind{line: "first-resume"}, "second": resumeKind{line: "second-resume"}}
	te.cfg = withoutWindows(te.cfg)
	te.cfg.Agent = "first"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := serveDeps(ctx, te.Env, storeDB, te.cfg, func(string, ...any) {}, &sync.Mutex{}, nil)
	if r := d.Turns.AgentFor(pr.Ref{}.Unit()); r == nil || r.Resume("", "") != "first-resume" {
		t.Fatalf("before the reload: %v", r)
	}
	reloaded := te.cfg
	reloaded.Agent = "second"
	serveDeps(ctx, te.Env, storeDB, reloaded, func(string, ...any) {}, &sync.Mutex{}, nil)
	if r := d.Turns.AgentFor(pr.Ref{}.Unit()); r == nil || r.Resume("", "") != "second-resume" {
		t.Fatalf("after the reload: %v", r)
	}
	if r := d.Turns.AgentFor(pr.Ref(r7).Unit()); r == nil || r.Resume("", "") != "second-resume" {
		t.Fatalf("a project without its own agent: %v", r)
	}
	reloaded.Agent = "missing"
	serveDeps(ctx, te.Env, storeDB, reloaded, func(string, ...any) {}, &sync.Mutex{}, nil)
	if r := d.Turns.AgentFor(pr.Ref{}.Unit()); r != nil {
		t.Fatalf("an unknown agent resolved to %v", r)
	}
}

// spentBudget is a GitHub that finds nothing and reports one GraphQL bucket.
type spentBudget struct {
	ghsource.GitHub
	limit dgh.Limit
}

func (b spentBudget) Allow(dgh.Priority) bool { return true }
func (b spentBudget) Budget() map[dgh.Resource]dgh.Limit {
	return map[dgh.Resource]dgh.Limit{dgh.ResourceGraphQL: b.limit}
}
func (spentBudget) Viewer(context.Context) (string, error) { return "me", nil }
func (spentBudget) SearchStubsFirst(_ context.Context, qs []string, _ int) ([]github.StubPage, []error, error) {
	return make([]github.StubPage, len(qs)), make([]error, len(qs)), nil
}

func TestServeRestoresTheBudgetAFastRunKept(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	at := te.Now()
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	spent := dgh.Limit{Resource: dgh.ResourceGraphQL, Limit: 5000, Remaining: 3000, ResetAt: at.Add(time.Hour).UTC()}
	cfg := te.cfg
	cfg.GitHub.Scope = config.Scope{People: []string{"ana"}}
	col := &ghsource.Collector{Store: inventory.New(db), GH: spentBudget{limit: spent}, Cfg: cfg, Now: func() time.Time { return at }, Log: t.Logf}
	if rep, err := col.Fast(ctx); err != nil || rep.Skipped != "" {
		t.Fatalf("fast: %+v %v", rep, err)
	}

	te.Env.GitHub = github.NewClient(github.Options{Host: "github.com", Now: func() time.Time { return at },
		Token: func(context.Context) (string, error) { return "t", nil }})
	cfg.GitHub.RateLimitFraction = 0.7
	te.Env.restoreBudget(ctx, db, cfg, t.Logf)
	if got := te.Env.GitHub.Budget()[dgh.ResourceGraphQL]; got.Remaining != 3000 || got.Limit != 5000 || !got.ResetAt.Equal(spent.ResetAt) {
		t.Fatalf("restored %+v", got)
	}
	if te.Env.GitHub.Allow(dgh.PriorityBackground) || !te.Env.GitHub.Allow(dgh.PriorityUser) {
		t.Fatal("60 % left under a 0.7 floor: background must wait, the user may read")
	}
}

type ghFunc func(args ...string) ([]byte, error)

func (f ghFunc) Run(_ context.Context, args ...string) ([]byte, error) { return f(args...) }

func TestFailedStepsReadsTheJobOnce(t *testing.T) {
	t.Parallel()
	var asked [][]string
	steps := failedSteps(githubtest.Runner(ghFunc(func(args ...string) ([]byte, error) {
		asked = append(asked, args)
		return []byte(`{"steps":[{"name":"Lint PR title","conclusion":"failure"},{"name":"Build","conclusion":"success"},{"name":"Run\ttests\u001b[2J","conclusion":"failure"}]}`), nil
	})))
	got, err := steps(context.Background(), "acme", "api", 114268776435)
	if err != nil || !slices.Equal(got, []string{"Lint PR title", "Run tests [2J"}) {
		t.Fatalf("%q %v", got, err)
	}
	if len(asked) != 1 || !slices.Equal(asked[0], []string{"api", "repos/acme/api/actions/jobs/114268776435"}) {
		t.Fatalf("gh %q", asked)
	}
	steps = failedSteps(githubtest.Runner(ghFunc(func(...string) ([]byte, error) { return nil, errors.New("HTTP 404") })))
	if _, err := steps(context.Background(), "acme", "api", 1); err == nil {
		t.Fatal("no error from a failed call")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFailedStepsOneRequestPerJob(t *testing.T) {
	t.Parallel()
	var paths []string
	gh := githubtest.Client(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"steps":[{"name":"Build","conclusion":"failure"}]}`)),
			Request:    r,
		}, nil
	}))
	gh.SetFloors(github.DefaultFloors(0.5))
	gh.Restore([]dgh.Limit{{Resource: dgh.ResourceCore, Limit: 5000, Remaining: 1000, ResetAt: time.Now().Add(time.Hour)}})
	background := dgh.WithPriority(context.Background(), dgh.PriorityBackground)
	if _, err := gh.REST(background, github.Request{Path: "repos/acme/api"}); !errors.Is(err, dgh.ErrBudget) {
		t.Fatalf("a background read at 20 %% was admitted: %v", err)
	}
	steps := failedSteps(gh)
	for _, job := range []int64{11, 12} {
		got, err := steps(background, "acme", "api", job)
		if err != nil || !slices.Equal(got, []string{"Build"}) {
			t.Fatalf("job %d: %q %v", job, got, err)
		}
	}
	if len(paths) != 2 || !strings.HasSuffix(paths[0], "/repos/acme/api/actions/jobs/11") || !strings.HasSuffix(paths[1], "/repos/acme/api/actions/jobs/12") {
		t.Fatalf("requests %q", paths)
	}
}

func TestBriefSkipsTheHomeOfAProjectOfAnotherAgent(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	cfg.AgentHome = "/home/me/.claude-work"
	cfg.Projects = []config.Project{
		{Match: "me/*", AgentHome: "/home/me/.claude-personal"},
		{Match: "oss/*", Agent: "echo", AgentHome: "/home/me/.echo"},
		{Match: "acme/*", Agent: "claude", AgentHome: "/home/me/.claude-acme"},
	}
	home := briefHome(cfg)
	for ref, want := range map[github.Ref]string{
		{Owner: "me", Repo: "lib", Number: 1}:   "/home/me/.claude-personal",
		{Owner: "oss", Repo: "lib", Number: 1}:  "/home/me/.claude-work",
		{Owner: "acme", Repo: "api", Number: 1}: "/home/me/.claude-acme",
	} {
		if got := home(ref); got != want {
			t.Errorf("%s: brief home %q, want %q", ref, got, want)
		}
	}
}

func TestSessionHomesAreCheckedByTheirProjectsKind(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.withEcho()
	te.cfg.Projects = []config.Project{{Match: "acme/*", Agent: "echo"}}
	ctx := context.Background()
	echoHome, claudeHome := t.TempDir(), t.TempDir()
	for handle, home := range map[string]string{"term-echo": echoHome, "term-claude": claudeHome} {
		if err := te.reg.SaveSession(ctx, dturn.Session{ID: handle, Home: home, Managed: true, StartedAt: testNow}); err != nil {
			t.Fatal(err)
		}
	}
	b := dturn.Book{Unit: pr.Ref(r7).Unit(), Turns: []dturn.Turn{{Intent: dturn.Intent(dintent.Review), ID: "r0", Trigger: dturn.ByHand, Head: "c1", State: dturn.Unrecorded, SentAt: testNow, SentTo: "term-echo"}}}
	if _, err := te.repo.Save(ctx, b); err != nil {
		t.Fatal(err)
	}
	for name, check := range map[string]func() ([]api.HomeFinding, error){
		"checkHomes":   func() ([]api.HomeFinding, error) { return te.checkHomes(ctx, te.cfg) },
		"homeProblems": func() ([]api.HomeFinding, error) { return te.homeProblems(ctx, te.cfg, []string{echoHome, claudeHome}) },
	} {
		findings, err := check()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		entries := map[string][]string{}
		for _, f := range findings {
			entries[f.Home] = append(entries[f.Home], f.Entry)
		}
		if got := entries[echoHome]; !slices.Equal(got, []string{"checked by echo"}) {
			t.Errorf("%s: echo session's home checked as %q", name, got)
		}
		if got := entries[claudeHome]; slices.Contains(got, "checked by echo") || !slices.Contains(got, "hook Stop") {
			t.Errorf("%s: claude session's home checked as %q", name, got)
		}
	}
}

func TestTheResumeLineOfAnEchoProjectNamesEchosCommand(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.withEcho()
	te.cfg.Projects = []config.Project{{Match: "acme/*", Agent: "echo"}}
	agentFor := te.agentFor(&servedTurns{cfg: te.cfg})
	if r := agentFor(pr.Ref(r7).Unit()); r == nil || r.Resume("/h", "s-1") != "cat" {
		t.Fatalf("acme/api resumes with %v", r)
	}
	if r := agentFor(pr.Ref{Owner: "other", Repo: "x", Number: 1}.Unit()); r == nil || r.Resume("/h", "s-1") != "CLAUDE_CONFIG_DIR='/h' claude --resume s-1" {
		t.Fatalf("other/x resumes with %v", r)
	}
}

func TestOrcaSinkGetsTheSectionsColumns(t *testing.T) {
	t.Parallel()
	cfg := (&Env{}).defaultConfig()
	cfg.Views["orca"].Columns["needs-me"] = "waiting-me"
	cfg.Views["orca"].Columns["working"] = "doing"
	_, sinks, err := orcaPorts(context.Background(), &wsorca.Manager{}, nil, cfg, nil)
	if err != nil || len(sinks) != 1 {
		t.Fatalf("sinks %v, %v", sinks, err)
	}
	s := sinks[0].ViewSink
	for column, want := range map[string]bool{"waiting-me": true, "doing": true, "reviewed": true, "needs-me": false, "reviewing": false} {
		if got := s.Manages(view.ReviewRole, column); got != want {
			t.Errorf("manages %q: %v", column, got)
		}
	}
}

func TestHealthNamesTheEnabledManagers(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]string{"orca": "Orca", "zed": "zed"} {
		te := newTestEnv(t, &fake.Manager{ManagerName: name})
		storeDB, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		d := serveDeps(ctx, te.Env, storeDB, te.cfg, func(string, ...any) {}, &sync.Mutex{}, nil)
		if d.WorkspaceManager != want || !slices.Equal(d.WorkspaceManagers, []string{want}) || !slices.Equal(d.ManagerNames, []string{name}) {
			t.Errorf("%s: health names %q %q %q, want %q", name, d.WorkspaceManager, d.WorkspaceManagers, d.ManagerNames, want)
		}
		cancel()
		<-te.served.done
		storeDB.Close()
	}
}

func TestSessionHomesKeepTheRegistryOrder(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	ctx := context.Background()
	root := t.TempDir()
	te.Home = filepath.Join(root, "user")
	homes := []string{filepath.Join(root, "z-home"), filepath.Join(root, "a-home"), filepath.Join(root, "m-home")}
	for i, h := range homes {
		if err := os.MkdirAll(h, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := te.reg.SaveSession(ctx, dturn.Session{ID: fmt.Sprintf("term-%d", i), Home: h, Managed: true, StartedAt: testNow}); err != nil {
			t.Fatal(err)
		}
	}
	sessions, err := te.Turns.Registry.Sessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, s := range sessions {
		want = append(want, s.Home)
	}
	findings, err := te.checkHomes(ctx, te.cfg)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range findings {
		if !slices.Contains(got, f.Home) && slices.Contains(homes, f.Home) {
			got = append(got, f.Home)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("homes checked in order %q, want the registry's %q", got, want)
	}
}

// noGH fails the test on any gh spawn and meters it as one.
type noGH struct{ t *testing.T }

func (g noGH) Run(ctx context.Context, args ...string) ([]byte, error) {
	meter.Add(ctx, meter.Sample{Res: meter.GitHub, Op: "gh " + args[0], Spawn: true})
	g.t.Errorf("a daemon read spawned gh %s", strings.Join(args, " "))
	return nil, errors.New("no gh in this test")
}

func TestATypeOnlyManagerCannotListTerminals(t *testing.T) {
	t.Parallel()
	reg := turnstest.NewRegistry()
	reg.Put(dturn.Session{ID: "term_1", Manager: "fake", Handle: "term_1", WorkspaceID: "ws", Managed: true})
	var s turns.Sessions = turns.NewRouter(reg, typeOnly{&fake.Manager{}})
	if _, err := s.Terminals(context.Background(), "ws"); err == nil {
		t.Fatal("a manager with no sessions adapter listed terminals")
	}
	svc := &turns.Service{Registry: reg, Sessions: s, Store: turnstest.NewStore()}
	if err := svc.MarkDormant(context.Background(), "term_1"); err == nil {
		t.Fatal("MarkDormant ended a process through a manager that cannot list")
	}
	if got, _, _ := reg.Session(context.Background(), "term_1"); got.Handle != "term_1" || !got.ClosedAt.IsZero() {
		t.Fatalf("the session lost its process: %+v", got)
	}
}

func TestServeNotesOneLogFromStoreAndDaemon(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	log := change.NewLog(change.DefaultWindow, nil)
	te.changes = log
	storeDB, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	defer storeDB.Close()
	d := serveDeps(context.Background(), te.Env, storeDB, te.cfg, func(string, ...any) {}, &sync.Mutex{}, nil)
	if d.Changes != log {
		t.Fatal("the daemon does not share the store's log")
	}
	if _, err := storeDB.Write(context.Background(), func(tx *sqlite.Tx) error {
		tx.Touch(port.TopicMeta)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	te.noteConfig()
	if c := log.Since(0, port.TopicMeta|port.TopicConfig); log.Gen() != 2 || c.Topics != port.TopicMeta|port.TopicConfig {
		t.Fatalf("gen %d, %+v", log.Gen(), c)
	}
}

func TestSessionHomesBudget(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	ctx := context.Background()
	cfg := te.cfg
	cfg.Projects = []config.Project{{Match: "acme/api", Agent: "echo"}}
	if err := te.repo.SaveSession(ctx, dturn.Session{ID: "term_none", Home: "/homes/none", Managed: true}); err != nil {
		t.Fatal(err)
	}
	for i := range 158 {
		h := fmt.Sprintf("term_%d", i)
		if err := te.repo.SaveSession(ctx, dturn.Session{ID: h, Home: fmt.Sprintf("/homes/%d", i), Managed: true}); err != nil {
			t.Fatal(err)
		}
		book := dturn.Book{Unit: pr.Ref{Owner: "acme", Repo: "api", Number: 1 + i%40}.Unit(), Turns: []dturn.Turn{{Intent: "review", ID: "r" + h, State: dturn.Done, Session: h, SentTo: h}}}
		if _, err := te.repo.Save(ctx, book); err != nil {
			t.Fatal(err)
		}
	}
	ctx, sc := metertest.Start(t, ctx)
	homes, err := te.sessionHomes(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(homes) != 159 {
		t.Fatalf("%d homes, want 159", len(homes))
	}
	for _, h := range homes {
		want := "echo"
		if h.home == "/homes/none" {
			want = cfg.Agent
		}
		if h.kind != want {
			t.Fatalf("home %s has kind %s, want %s", h.home, h.kind, want)
		}
	}
	metertest.Within(t, sc, metertest.Budget{
		{Res: meter.SQL}:             2,
		{Res: meter.Tx, Op: "write"}: 0,
	})
}

func TestServedAgentsComeFromTheSessionsSnapshot(t *testing.T) {
	t.Parallel()
	if agents, ok := lastAgents(turns.NewRouter(turnstest.NewRegistry(), typeOnly{&fake.Manager{}})).Agents(); !ok || agents != nil {
		t.Fatalf("a manager with no agents: %+v %v, want none, known", agents, ok)
	}
	if a := lastAgents(turnstest.NewSessions()); a != nil {
		t.Fatalf("sessions with no snapshot got agents %v", a)
	}
}

func TestServeRetainsTheStore(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.cfg = withoutWindows(te.cfg)
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), storeFile), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	old := te.Now().Add(-40 * 24 * time.Hour).UTC().Format(event.TimeLayout)
	sqlitetest.Exec(t, db, `INSERT INTO turn_stops (session, prompt_id, at) VALUES ('term_1', 'p1', ?)`, old)
	d := serveDeps(context.Background(), te.Env, db, te.cfg, func(string, ...any) {}, &sync.Mutex{}, nil)
	if d.Retain == nil {
		t.Fatal("no retain job")
	}
	for _, want := range []string{"retain: turn_stops 1", "retain: nothing"} {
		if got, err := d.Retain(context.Background()); err != nil || got != want {
			t.Fatalf("retain = %q %v, want %q", got, err, want)
		}
	}
}

// readyFacts is a github module's facts that read every pull request as
// approved, green and clean in its checkout.
type readyFacts struct{ *automationtest.Units }

func (readyFacts) Author(context.Context, unit.Unit, bool) (*view.AuthorFacts, error) {
	return &view.AuthorFacts{State: "OPEN", Approved: true, ChecksPass: true, Mergeable: "MERGEABLE", Base: "main",
		Local: &view.LocalFacts{Clean: true, Head: "c2", PRHead: "c2"}}, nil
}

func TestServeWiresAuthorFactsIntoViews(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	storeDB, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer storeDB.Close()
	board := testsink.New("board").WithAuthorColumns()
	te.ports = sinksOf(board)
	saved := plugins
	plugins = []source.Plugin{{ID: unit.GitHub, Codec: pr.Codec{}, Build: func(cfg config.Config, sd source.Deps) (source.Module, error) {
		m, err := ghsource.Module(cfg, sd)
		m.Facts = readyFacts{automationtest.NewUnits()}
		return m, err
	}}}
	defer func() { plugins = saved }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := serveDeps(ctx, te.Env, storeDB, te.cfg, func(string, ...any) {}, &sync.Mutex{}, nil)
	u := r7.Unit()
	board.Place(u, string(view.InReview))
	if _, err := d.Turns.Send(ctx, turns.SendRequest{Unit: u, Intent: dturn.Intent(dintent.Address), Turn: "a1", Trigger: dturn.Automated, Head: "c2", Records: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Turns.Start(ctx, "term_1", "a1"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Turns.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "a1", Via: "mcp", Record: record.Record{State: record.Done, Summary: "pushed"}}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the author card in ready", func() bool {
		c, _, _ := board.Card(u)
		return c == string(view.Ready)
	})
}

func TestPendingRoundsReachTheReadModel(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	storeDB, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer storeDB.Close()
	ctx := context.Background()
	if err := inventory.New(storeDB).UpsertPR(ctx, dinv.PR{Ref: r7, Title: "Batch export", Author: "ana", State: "OPEN", URL: r7.URL(), Reasons: []string{"review-requested"}}); err != nil {
		t.Fatal(err)
	}
	deadline := testNow.Add(4 * time.Minute)
	it := workflow.Item{ID: "review-follow-up/github/acme%2Fapi%237/k", Workflow: "review-follow-up", Unit: r7.Unit(), Decision: workflow.DecisionPending,
		Reason: "runs when its window ends", Default: workflow.OutcomeRun, Deadline: deadline, At: testNow, Observe: false,
		Key:   workflow.Key{Workflow: "review-follow-up", Unit: r7.Unit(), Value: "k"},
		Steps: []workflow.Step{{Use: builtin.StepTurnStart, Args: map[string]any{"intent": string(dintent.Review), "stage": "follow-up"}}}}
	if err := storeDB.Ledger().Save(ctx, automation.PlanWrite{Workflows: []string{"review-follow-up"}, Items: []workflow.Item{it}}); err != nil {
		t.Fatal(err)
	}
	d := serveDeps(ctx, te.Env, storeDB, te.cfg, func(string, ...any) {}, &sync.Mutex{}, nil)
	d.Startup = nil
	req := httptest.NewRequest("GET", api.Rows, nil)
	req.Header.Set(api.Header, api.Version)
	rec := httptest.NewRecorder()
	daemon.New(d).Handler().ServeHTTP(rec, req)
	var page api.RowsPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("%v: %s", err, rec.Body)
	}
	i := slices.IndexFunc(page.Rows, func(r api.Row) bool { return r.Unit == r7.Unit() })
	if i < 0 || page.Rows[i].Pending == nil || !page.Rows[i].Pending.Deadline.Equal(deadline) || page.Rows[i].Pending.Intent != "follow-up" {
		t.Fatalf("rows %s", rec.Body)
	}
}

func TestThePlanAPIIsServedFromAnEngineOfItsOwn(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	storeDB, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer storeDB.Close()
	d := serveDeps(context.Background(), te.Env, storeDB, te.cfg, func(string, ...any) {}, &sync.Mutex{}, nil)
	p, ok := d.Plan.(servedPlan)
	if !ok || p.plan == nil || p.plan == te.engine || p.eng.Engine != te.engine || d.Sweep != daemon.Sweeper(p.eng) {
		t.Fatalf("plan %T, sweep %T", d.Plan, d.Sweep)
	}
	if _, ok := d.Pending.(enginePending); !ok {
		t.Fatalf("pending %T", d.Pending)
	}
	if f := te.viewFacts().p.Load(); f == nil {
		t.Fatal("the views read no github facts")
	}
}

// tableSums hashes every row of every table of db.
func tableSums(t *testing.T, db *sqlite.DB) string {
	t.Helper()
	ctx := context.Background()
	rows, err := db.R().QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		rows.Scan(&name)
		tables = append(tables, name)
	}
	rows.Close()
	var b strings.Builder
	for _, table := range tables {
		rs, err := db.R().QueryContext(ctx, `SELECT * FROM "`+table+`" ORDER BY 1`)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rs.Columns()
		for rs.Next() {
			vals, ptrs := make([]any, len(cols)), make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			rs.Scan(ptrs...)
			fmt.Fprintf(&b, "%s %v\n", table, vals)
		}
		rs.Close()
	}
	return b.String()
}

func TestTheServedPlanEvaluatesOverTheRealStepsWithoutWriting(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.changes = change.NewLog(change.DefaultWindow, nil)
	storeDB, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), te.changes)
	if err != nil {
		t.Fatal(err)
	}
	defer storeDB.Close()
	ctx := context.Background()
	st := inventory.New(storeDB)
	if err := st.UpsertPR(ctx, dinv.PR{Ref: r7, Title: "Batch export", Author: "ana", State: "OPEN", HeadOid: "c2", ChecksState: "SUCCESS",
		ReviewRequests: []string{"me"}, Reasons: []string{"review-requested"}, URL: r7.URL()}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetNextGuidance(ctx, r7, "look at the retry"); err != nil {
		t.Fatal(err)
	}
	te.cfg.Brief.Enabled = true
	d := serveDeps(ctx, te.Env, storeDB, te.cfg, func(string, ...any) {}, &sync.Mutex{}, nil)
	d.Startup = nil
	p := d.Plan.(servedPlan)
	p.plan.Workflows = func() []workflow.Workflow {
		return []workflow.Workflow{turnStartWorkflow("review-follow-up"), everyUnit(unit.Session, "session"), everyUnit(unit.Workspace, "workspace")}
	}
	sums, gen := tableSums(t, storeDB), te.changes.Gen()
	req := httptest.NewRequest("GET", api.Plan+"?trigger=sweep", nil)
	req.Header.Set(api.Header, api.Version)
	rec := httptest.NewRecorder()
	daemon.New(d).Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if after := tableSums(t, storeDB); after != sums {
		t.Fatalf("the served plan wrote a store row:\nbefore\n%s\nafter\n%s\n%s", sums, after, rec.Body)
	}
	if got := te.changes.Gen(); got != gen {
		t.Fatalf("the served plan noted the change log: %d to %d", gen, got)
	}
	if o, _ := st.Override(ctx, r7); o.NextGuidance != "look at the retry" {
		t.Fatalf("the served plan took the guidance: %q", o.NextGuidance)
	}
}

func TestDaemonReadsSpawnNoGh(t *testing.T) {
	t.Parallel()
	_, te := scenario(t)
	te.GH = noGH{t}
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	dctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := serveDeps(dctx, te.Env, db, te.cfg, serveLog(t), &sync.Mutex{}, nil)
	te.Rec = storeRecorder{te.Store}

	ctx, all := metertest.Start(t, context.Background())
	phases := []struct {
		name  string
		reads bool
		run   func(context.Context) error
	}{
		{"fast", true, func(ctx context.Context) error {
			_, _, err := ingest.RunJob(ctx, d.Ingest[0].Job, te.Now())
			return err
		}},
		{"slow", true, func(ctx context.Context) error { _, _, err := d.Collector.SlowJob(ctx); return err }},
		{"sweep", false, func(ctx context.Context) error {
			_, _, err := d.Sweep.Sweep(ctx, &sync.Mutex{})
			return err
		}},
	}
	for _, p := range phases {
		pctx, sc := meter.Start(ctx, p.name)
		if err := p.run(pctx); err != nil {
			t.Fatalf("%s: %v", p.name, err)
		}
		if p.reads && metertest.Count(sc, metertest.Limit{Res: meter.GitHub, Op: "graphql"}) == 0 {
			t.Errorf("%s sent no GraphQL request through the client\n%s", p.name, metertest.Report(sc.End()))
		}
	}
	d.FailedSteps(ctx, "acme", "api", 1)
	if n := metertest.Count(all, metertest.Limit{Res: meter.GitHub, Op: "rest"}); n == 0 {
		t.Error("failed steps sent no REST request through the client")
	}
	if n := all.Tally().Total(meter.GitHub).Spawns; n != 0 {
		t.Fatalf("%d GitHub spawns\n%s", n, metertest.Report(all.Tally()))
	}
}
