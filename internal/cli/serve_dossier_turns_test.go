package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/core/dossiers"
	"github.com/wloophq/wloop/internal/core/headless"
	"github.com/wloophq/wloop/internal/core/lock"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/dossier"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/task"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/gitx"
	"github.com/wloophq/wloop/internal/turn"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func servedDossiers(t *testing.T) (*testEnv, *sqlite.DB) {
	t.Helper()
	return servedDossiersAfter(t, false)
}

// servedDossiersAfter serves the dossiers, after a pass whose context ended, as a daemon started
// with a broken config serves until the config loads.
func servedDossiersAfter(t *testing.T, degraded bool) (*testEnv, *sqlite.DB) {
	t.Helper()
	te := newTestEnv(t, &fake.Manager{})
	te.cfg = withoutWindows(te.cfg)
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), storeFile), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if degraded {
		dctx, stop := context.WithCancel(context.Background())
		te.serveDeps(dctx, db, te.cfg, t.Logf, &lock.Mutex{}, nil)
		stop()
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	te.serveDeps(ctx, db, te.cfg, t.Logf, &lock.Mutex{}, nil)
	if te.dossierTurns == nil {
		t.Fatal("the dossiers are not wired into the turns")
	}
	return te, db
}

func dossierPathOf(t *testing.T, te *testEnv, u unit.Unit) string {
	t.Helper()
	p, err := dossier.Dir(te.StateDir, dossier.Owner{Unit: u})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDossierOpenedBeforeLaunch(t *testing.T) {
	t.Parallel()
	te, _ := servedDossiers(t)
	ctx := context.Background()
	path := dossierPathOf(t, te, headlessThread)
	line, err := te.agentCommand(ctx, te.cfg, "/wt", roundOptions{trusted: true, unit: headlessThread})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "--add-dir '"+path+"'") || !strings.Contains(line, "Edit(/"+path+"/log/**)") {
		t.Fatalf("the launch line does not grant %s: %s", path, line)
	}
	if _, err := os.Stat(filepath.Join(path, "notes.md")); err != nil {
		t.Fatalf("the dossier was not opened before the launch: %v", err)
	}
	if line, _ := te.agentCommand(ctx, te.cfg, "/wt", roundOptions{trusted: true}); strings.Contains(line, "--add-dir") {
		t.Fatalf("a launch with no unit got a grant: %s", line)
	}

	render := func() string {
		t.Helper()
		_, out, err := te.renderTurn(ctx, te.cfg, turn.Request{Intent: turn.Intent(dintent.Act), Unit: headlessThread, Payload: unitPayload{headlessThread}})
		if err != nil {
			t.Fatal(err)
		}
		return out.Text
	}
	if text := render(); !strings.Contains(text, "`"+path+"`") || !strings.Contains(text, "append to log/") {
		t.Fatalf("a granted session's instructions:\n%s", text)
	}
	old := dturn.Session{ID: "term-old", Unit: headlessThread, Managed: true, StartedAt: testNow.Add(-time.Hour)}
	if err := te.Turns.Registry.SaveSession(ctx, old); err != nil {
		t.Fatal(err)
	}
	if text := render(); !strings.Contains(text, "reachable on its next launch") || strings.Contains(text, "append to log/") {
		t.Fatalf("a session launched before the grant is told to write:\n%s", text)
	}
}

func TestHeadlessBuilderLeavesTheDossierToThePrompt(t *testing.T) {
	t.Parallel()
	te, _ := servedDossiers(t)
	got, err := headlessBuilder{e: te.Env, cfg: te.cfg}.Instructions(context.Background(), headlessThread, dturn.Intent(dintent.Act), nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "### Dossier") {
		t.Fatal("a headless call's instructions carry the session's dossier section")
	}
}

type dossierAsker struct {
	mu    sync.Mutex
	asks  []turns.Ask
	reply string
}

func (a *dossierAsker) Ask(_ context.Context, q turns.Ask) (turns.Answer, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.asks = append(a.asks, q)
	return turns.Answer{JSON: []byte(a.reply)}, nil
}

func TestHeadlessReadsTheDossier(t *testing.T) {
	t.Parallel()
	te, db := servedDossiers(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	d := te.serveDeps(ctx, db, te.cfg, func(string, ...any) {}, &lock.Mutex{}, nil)
	svc, ok := d.Headless.(*headless.Service)
	if !ok {
		t.Fatalf("headless is %T", d.Headless)
	}
	raw, _ := json.Marshal(map[string]any{"record": record.Record{State: record.Done, Summary: "read it"}, "drafts": []any{}, "log": "the thread asks for a date"})
	a := &dossierAsker{reply: string(raw)}
	svc.Asker = a
	res, err := svc.Run(ctx, headless.Request{Unit: headlessThread})
	if err != nil {
		t.Fatal(err)
	}
	path := dossierPathOf(t, te, headlessThread)
	if len(a.asks) != 1 || !slices.Contains(a.asks[0].Dirs, path) || !slices.Equal(a.asks[0].Tools, turns.ReadOnlyTools) {
		t.Fatalf("asks %+v", a.asks)
	}
	logs, err := filepath.Glob(filepath.Join(path, "log", "*-headless-*.md"))
	if err != nil || len(logs) != 1 || !strings.HasSuffix(logs[0], "-headless-"+strconv.FormatInt(res.Seq, 10)+".md") {
		t.Fatalf("logs %v %v", logs, err)
	}
}

func TestHeadlessTurnsCountTheDossierCalls(t *testing.T) {
	t.Parallel()
	te, db := servedDossiers(t)
	ctx := context.Background()
	te.dossierCalls.Caps = dossiers.Caps{PerCallUSD: 0.5, PerDayUSD: 0.5}
	te.dossierCalls.Asker = &dossierAsker{reply: `{"notes":"x"}`}
	if _, err := te.dossierTurns.Open(ctx, headlessThread); err != nil {
		t.Fatal(err)
	}
	te.dossierTurns.Dossiers.Policy.CompactAt = 1
	path := dossierPathOf(t, te, headlessThread)
	if err := os.WriteFile(filepath.Join(path, "log", "20261009T150000Z-session.md"), []byte("a log entry"), 0o600); err != nil {
		t.Fatal(err)
	}
	if done, err := te.dossierCalls.Compact(ctx, headlessThread); err != nil || !done {
		t.Fatalf("compact: %v %v", done, err)
	}
	d := serveDepsAgain(t, te, db)
	spent, err := d.Spent(ctx, testNow)
	if err != nil || spent != 0.5 {
		t.Fatalf("the headless cap reads %v spent: %v", spent, err)
	}
}

// serveDepsAgain serves a second pass, as a restart does, and returns its headless service.
func serveDepsAgain(t *testing.T, te *testEnv, db *sqlite.DB) *headless.Service {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	svc, ok := te.serveDeps(ctx, db, te.cfg, t.Logf, &lock.Mutex{}, nil).Headless.(*headless.Service)
	if !ok {
		t.Fatal("no headless service")
	}
	return svc
}

func TestATurnEndCommitsTheDossier(t *testing.T) {
	t.Parallel()
	te, _ := servedDossiers(t)
	assertTurnEndCommits(t, te)
}

func TestATurnEndCommitsAfterTheConfigLoads(t *testing.T) {
	t.Parallel()
	te, _ := servedDossiersAfter(t, true)
	assertTurnEndCommits(t, te)
}

func assertTurnEndCommits(t *testing.T, te *testEnv) {
	t.Helper()
	ctx := context.Background()
	d, err := te.dossierTurns.Open(ctx, headlessThread)
	if err != nil {
		t.Fatal(err)
	}
	seq, err := te.Turns.BeginHeadless(ctx, turns.HeadlessStart{Unit: headlessThread, Intent: dturn.Intent(dintent.Act), Turn: "r1", Trigger: dturn.ByHand})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.Path, "sources", "a.md"), []byte("a source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.Path, "notes.md"), []byte("overwritten"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := te.Turns.EndHeadless(ctx, seq, turns.HeadlessEnd{Rec: record.Record{State: record.Done, Summary: "s"}}); err != nil {
		t.Fatal(err)
	}
	want := "turn " + strconv.FormatInt(seq, 10)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if got, err := gitLog(d.Path); err == nil && got == want {
			break
		}
		if time.Now().After(deadline) {
			got, err := gitLog(d.Path)
			t.Fatalf("last commit %q, want %q: %v", got, want, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	changes, err := te.dossierTurns.Dossiers.Files.Changed(ctx, d.Path)
	raw, _ := os.ReadFile(filepath.Join(d.Path, "notes.md"))
	if err != nil || len(changes) != 0 || string(raw) == "overwritten" {
		t.Fatalf("the turn end left %+v, notes %q: %v", changes, raw, err)
	}
}

func gitLog(dir string) (string, error) {
	cmd := exec.Command(gitx.Binary(), "-C", dir, "log", "-1", "--format=%s")
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_DIR="+filepath.Join(dir, ".git"))
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func TestDossierSpendSurvivesARestart(t *testing.T) {
	t.Parallel()
	te, db := servedDossiers(t)
	ctx := context.Background()
	n := 0
	compactOnce := func() error {
		t.Helper()
		te.dossierCalls.Caps = dossiers.Caps{PerCallUSD: 0.5, PerDayUSD: 0.5}
		te.dossierCalls.Asker = &dossierAsker{reply: `{"notes":"x"}`}
		te.dossierTurns.Dossiers.Policy.CompactAt = 1
		d, err := te.dossierTurns.Open(ctx, headlessThread)
		if err != nil {
			t.Fatal(err)
		}
		n++
		name := "log/" + te.Now().UTC().Format(dossiers.LogTime) + "-session-" + strconv.Itoa(n) + ".md"
		if err := os.WriteFile(filepath.Join(d.Path, name), []byte("a log entry"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err = te.dossierCalls.Compact(ctx, headlessThread)
		return err
	}
	if err := compactOnce(); err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	te.serveDeps(cctx, db, te.cfg, t.Logf, &lock.Mutex{}, nil)
	if err := compactOnce(); !errors.Is(err, dossiers.ErrOverCap) {
		t.Fatalf("a restarted daemon ran a call past the day's cap: %v", err)
	}
}

func TestOnlyALaunchWithTheGrantIsGranted(t *testing.T) {
	t.Parallel()
	te, _ := servedDossiers(t)
	ctx := context.Background()
	render := func() string {
		t.Helper()
		_, out, err := te.renderTurn(ctx, te.cfg, turn.Request{Intent: turn.Intent(dintent.Act), Unit: headlessThread, Payload: unitPayload{headlessThread}})
		if err != nil {
			t.Fatal(err)
		}
		return out.Text
	}
	granted := func(text string) bool { return strings.Contains(text, "append to log/") }
	if _, err := te.agentCommand(ctx, te.cfg, "/wt", roundOptions{trusted: true, unit: headlessThread}); err != nil {
		t.Fatal(err)
	}
	ws := wsdomain.Workspace{ID: "ws1", Path: "/wt"}
	if err := te.launched(ctx, te.cfg, ws, headlessThread, proc{"term-1", te.WS.First(), "term-1"}); err != nil {
		t.Fatal(err)
	}
	if err := te.Turns.SessionStarted(ctx, turns.SessionStart{Session: "term-1", Source: "startup"}); err != nil {
		t.Fatal(err)
	}
	if text := render(); !granted(text) {
		t.Fatalf("a session the loop launched with the grant:\n%s", text)
	}
	now := testNow.Add(time.Hour)
	te.Now = func() time.Time { return now }
	if err := te.Turns.SessionStarted(ctx, turns.SessionStart{Session: "term-1", Source: "resume"}); err != nil {
		t.Fatal(err)
	}
	if text := render(); granted(text) {
		t.Fatal("a session resumed without the grant is told to write")
	}

	te2, _ := servedDossiers(t)
	if err := te2.Turns.SessionStarted(ctx, turns.SessionStart{Session: "term-user", Source: "startup"}); err != nil {
		t.Fatal(err)
	}
	if err := te2.Turns.Launched(ctx, dturn.Session{ID: "term-user", Unit: headlessThread}); err != nil {
		t.Fatal(err)
	}
	if _, out, err := te2.renderTurn(ctx, te2.cfg, turn.Request{Intent: turn.Intent(dintent.Act), Unit: headlessThread, Payload: unitPayload{headlessThread}}); err != nil || granted(out.Text) {
		t.Fatalf("a session registered without a grant is told to write: %v", err)
	}
}

func TestASessionOnATasksThreadIsGrantedTheTasksDossier(t *testing.T) {
	t.Parallel()
	te, db := servedDossiers(t)
	ctx := context.Background()
	tk := task.Task{ID: "t-00000000000000f1", Kind: task.KindAction, State: "next", Category: task.CategoryReady, Title: "Answer Ana",
		Units: []unit.Unit{headlessThread}, Origin: task.OriginDerived, Created: testNow, Updated: testNow}
	if err := db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: tk}}); err != nil {
		t.Fatal(err)
	}
	path := dossierPathOf(t, te, task.UnitOf(tk.ID))
	line, err := te.agentCommand(ctx, te.cfg, "/wt", roundOptions{trusted: true, unit: headlessThread})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "--add-dir '"+path+"'") {
		t.Fatalf("the launch line does not grant the task's dossier %s: %s", path, line)
	}
	ws := wsdomain.Workspace{ID: "ws1", Path: "/wt"}
	if err := te.launched(ctx, te.cfg, ws, headlessThread, proc{"term-1", te.WS.First(), "term-1"}); err != nil {
		t.Fatal(err)
	}
	if err := te.Turns.SessionStarted(ctx, turns.SessionStart{Session: "term-1", Source: "startup"}); err != nil {
		t.Fatal(err)
	}
	_, out, err := te.renderTurn(ctx, te.cfg, turn.Request{Intent: turn.Intent(dintent.Act), Unit: headlessThread, Payload: unitPayload{headlessThread}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Text, "`"+path+"`") || !strings.Contains(out.Text, "append to log/") {
		t.Fatalf("the session's instructions do not grant the task's dossier:\n%s", out.Text)
	}
}
