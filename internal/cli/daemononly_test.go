package cli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func TestCommandsFailFastWithoutTheDaemon(t *testing.T) {
	for _, k := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "CLAUDE_CONFIG_DIR"} {
		t.Setenv(k, t.TempDir())
	}
	url := "https://github.com/acme/api/pull/7"
	// A valid record, so turn end gets past its local validation to the daemon.
	record := filepath.Join(t.TempDir(), "turn-abc.json")
	if err := os.WriteFile(record, []byte(`{"state":"settled","summary":"nothing new"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"status"}, {"review", url}, {"address", url}, {"take-over", url}, {"nudge", url},
		{"pause", url}, {"plan", "apply", url}, {"doctor"}, {"turn", "show", url},
		{"instructions", "review", "--turn", "abc"}, {"instructions", "review", url},
		{"turn", "end", url, "--turn", "abc", "--file", record}, {"turn", "answer", url},
		{"profile", "try", url}, {"probe", "--workspace", "ws-e2e"},
		{"profile", "show", "acme/api"}, {"explore", "acme/api"}, {"pause"}, {"resume"}, {"open"}, {"open", url},
		{"tui"}, {"brief", "refresh", url}, {"mute", url, "--until", "2d"}, {"track", url}, {"untrack", url},
	} {
		te := newTestEnv(t, &fake.Manager{})
		te.Socket = filepath.Join(t.TempDir(), "none.sock")
		start := time.Now()
		code := te.run(args...)
		if said := te.out.String() + te.err.String(); code == 0 || time.Since(start) > time.Second || !strings.Contains(said, "launchctl kickstart") {
			t.Errorf("%v: exit %d after %v: %s", args, code, time.Since(start), te.err)
		}
		if _, err := os.Stat(filepath.Join(te.StateDir, "store.db")); !os.IsNotExist(err) {
			t.Errorf("%v opened the store", args)
		}
	}
}

// startDaemonOf runs te's daemon as serve does, start-up reconcile included,
// and returns once the reconcile has reported.
func startDaemonOf(t *testing.T, te *testEnv) {
	t.Helper()
	mu := &sync.Mutex{}
	dir := shortDir(t)
	cfg := te.cfg
	cfg.HTTP.Port = 0
	srv := daemon.New(withRows(daemon.Deps{Pauses: te.Rounds, Turns: te.Turns, Views: te.Views, Actions: envActions{te.Env, te.Store}, Intents: te.loadedIntents, TurnByHand: te.byHand(mu), LoopMu: mu,
		Startup: te.startupOnce(te.cfg, t.Logf), Homes: te.liveHomes(te.cfg), DryRunTurn: te.dryRunTurn, Doctor: te.doctorState,
		Cfg: cfg, Version: "test", Now: te.Now, Viewer: func(context.Context) (string, []string, error) { return "me", nil, nil }, Log: t.Logf,
		StateDir: dir, Socket: filepath.Join(dir, "daemon.sock"), LockPath: filepath.Join(dir, "daemon.lock"), InfoPath: filepath.Join(dir, "http.json")}, te.Store))
	te.Socket = filepath.Join(dir, "daemon.sock")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("run: %v", err)
		}
	})
	deadline := time.Now().Add(time.Minute)
	for {
		rep, err := client.New(te.Socket).Doctor(context.Background(), api.DoctorQuery{})
		if err == nil && rep.Reconcile != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the daemon never reported its reconcile: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDoctorReadsStateThroughTheDaemon(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	st := te.loadRounds(t)
	st.FirstCapHit(r7.String(), testNow)
	te.saveRounds(t, st)
	te.setPause(t, round.Pause{At: testNow, Repos: []string{"acme/sandbox"}})
	startDaemonOf(t, te)
	te.run("doctor")
	out := te.out.String()
	for _, want := range []string{
		"[missing] automated rounds held by a cap today: " + r7.String(),
		"[info   ] paused: every workflow in apply mode and board moves paused on acme/sandbox since",
		"; " + brand.Command + " resume restarts them",
		"[info   ] reconcile: at start-up",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(te.StateDir, "store.db")); !os.IsNotExist(err) {
		t.Error("doctor opened a store in the state directory")
	}
}

func TestStartupReportsABrokenHome(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	home := filepath.Join(te.Home, ".claude")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fix := brand.Name + " install --home " + home
	startDaemonOf(t, te)
	eventually(t, "the broken home in health", func() bool { return len(homeWarnings(t, te, home)) > 0 })
	if ws := homeWarnings(t, te, home); !slices.ContainsFunc(ws, func(w string) bool {
		return strings.HasPrefix(w, "agent home "+home+": hook Stop") && strings.HasSuffix(w, "; fix: "+fix)
	}) {
		t.Errorf("warnings %q", ws)
	}
	te.run("doctor")
	if out := te.out.String(); !strings.Contains(out, "agent home "+home+": hook Stop") || !strings.Contains(out, "fix with: "+fix) {
		t.Errorf("doctor:\n%s", out)
	}
}

func homeWarnings(t *testing.T, te *testEnv, home string) []string {
	t.Helper()
	h, err := client.New(te.Socket).Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return slices.DeleteFunc(slices.Clone(h.Warnings), func(w string) bool { return !strings.HasPrefix(w, "agent home "+home) })
}

func TestAFixedHomeStopsBeingReported(t *testing.T) {
	root, user := tempWorld(t)
	te := newTestEnv(t, &fake.Manager{})
	te.Home = user
	home := filepath.Join(root, "work-home")
	writeFile(t, filepath.Join(home, "settings.json"), `{}`)
	te.cfg.Projects = []config.Project{{Match: "acme/*", AgentHome: home}}
	startDaemonOf(t, te)
	eventually(t, "the broken home in health", func() bool { return len(homeWarnings(t, te, home)) > 0 })
	if code := te.run("install", "--home", home); code != 0 {
		t.Fatalf("install: exit %d: %s", code, te.err)
	}
	te.out.Reset()
	te.run("doctor")
	for _, line := range strings.Split(te.out.String(), "\n") {
		if strings.Contains(line, "[missing]") && strings.Contains(line, "agent home "+home) {
			t.Errorf("doctor still reports: %s", line)
		}
	}
	if w := homeWarnings(t, te, home); len(w) != 0 {
		t.Errorf("health still warns: %q", w)
	}
}

func TestOrcaStatusHookIsNoHealthWarning(t *testing.T) {
	root, user := tempWorld(t)
	te := newTestEnv(t, &fake.Manager{})
	te.Home = user
	home := filepath.Join(root, "work-home")
	writeFile(t, filepath.Join(home, "settings.json"), `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"curl localhost:$ORCA_AGENT_HOOK_PORT"}]}]}}`)
	te.cfg.Projects = []config.Project{{Match: "acme/*", AgentHome: home}}
	if code := te.run("install", "--home", home); code != 0 {
		t.Fatalf("install: exit %d: %s", code, te.err)
	}
	startDaemonOf(t, te)
	te.out.Reset()
	te.run("doctor")
	if out := te.out.String(); !strings.Contains(out, "[info   ] agent home "+home+": Orca status hook") {
		t.Errorf("doctor lacks the Orca hook as info:\n%s", out)
	}
	if w := homeWarnings(t, te, home); len(w) != 0 {
		t.Errorf("health warns: %q", w)
	}
}

func TestDoctorListsAHomeOnlyASessionReported(t *testing.T) {
	root, user := tempWorld(t)
	te := newTestEnv(t, &fake.Manager{})
	te.Home = user
	home := filepath.Join(root, "session-home")
	writeFile(t, filepath.Join(home, "settings.json"), `{}`)
	te.cfg.Projects = []config.Project{{Match: "acme/*", AgentHome: home}}
	te.LoadConfig = func() (config.Config, error) { return config.Defaults(), nil }
	startDaemonOf(t, te)
	te.out.Reset()
	te.run("doctor")
	if out := te.out.String(); !strings.Contains(out, "[missing] agent home "+home+": hook Stop") {
		t.Errorf("doctor lacks the daemon's home line:\n%s", out)
	}
}

func TestHooksAllowWithoutTheDaemon(t *testing.T) {
	for _, hook := range []string{"stop", "session-start"} {
		te := newTestEnv(t, &fake.Manager{})
		te.noDaemon(t)
		start := time.Now()
		if code := te.run("hook", hook); code != 0 || time.Since(start) > time.Second {
			t.Fatalf("%s: exit %d after %v: %s", hook, code, time.Since(start), te.err)
		}
		if hook == "session-start" && te.out.Len() != 0 {
			t.Errorf("session-start printed %q", te.out)
		}
		if _, err := os.Stat(filepath.Join(te.StateDir, "store.db")); !os.IsNotExist(err) {
			t.Errorf("%s opened the store", hook)
		}
	}
}

func TestARestartReconcilesAgainOnlyWhenItCancelledTheFirst(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	run := te.startupOnce(te.cfg, t.Logf)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	run(cancelled)
	second := testNow.Add(time.Hour)
	te.Now = func() time.Time { return second }
	if rep, _ := run(context.Background()); !rep.At.Equal(second) {
		t.Fatalf("a cancelled reconcile was kept: at %v", rep.At)
	}
	te.Now = func() time.Time { return second.Add(time.Hour) }
	if rep, _ := run(context.Background()); !rep.At.Equal(second) {
		t.Fatalf("a finished reconcile ran again: at %v", rep.At)
	}
}
