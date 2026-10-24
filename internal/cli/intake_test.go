package cli

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/intake/intaketest"
	"github.com/wloophq/wloop/internal/daemon"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

var waitBriefing = intake.Readiness{Wait: "briefing", Attempt: 1}

type intakeDaemon struct {
	socket  string
	store   *inventory.Store
	intakes intake.Store
}

func serveSocketOf(t *testing.T, h http.Handler) string {
	t.Helper()
	socket := filepath.Join(shortDir(t), "daemon.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewUnstartedServer(h)
	hs.Listener = l
	hs.Start()
	t.Cleanup(hs.Close)
	return socket
}

// startIntakeDaemon serves a daemon whose review, address and run go
// through intakes, with acme/api#7 open in its store.
func startIntakeDaemon(t *testing.T, actions daemon.Actions, briefs intake.Briefs) *intakeDaemon {
	t.Helper()
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "inv.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	st := inventory.New(db)
	if err := st.UpsertPR(context.Background(), dinv.PR{Ref: r7, Title: "Batch export", Author: "ana", State: "OPEN", HeadOid: "h2",
		ReviewRequests: []string{"me"}, Reasons: []string{"review-requested"}, URL: r7.URL()}); err != nil {
		t.Fatal(err)
	}
	srv := daemon.New(withRows(daemon.Deps{Cfg: config.Defaults(), Version: "test", Actions: actions, Intakes: db.Intakes(), IntakeBriefs: briefs,
		Now:    func() time.Time { return daemonNow },
		Viewer: func(context.Context) (string, []string, error) { return "me", nil, nil },
		Log:    t.Logf}, st))
	return &intakeDaemon{socket: serveSocketOf(t, srv.Handler()), store: st, intakes: db.Intakes()}
}

func (d *intakeDaemon) state(t *testing.T, seq int64) intake.State {
	t.Helper()
	i, _, err := d.intakes.Intake(context.Background(), seq)
	if err != nil {
		t.Fatal(err)
	}
	return i.State
}

func (f *recordingActions) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// seedBrief stores a brief of acme/api#7 whose run took 12.8 s.
func seedBrief(t *testing.T, st *inventory.Store) {
	t.Helper()
	ctx := context.Background()
	start := daemonNow.Add(-time.Minute)
	run := dinv.BriefRun{PR: r7, Trigger: "wanted", Version: "v1", AttemptOf: "h2", StartedAt: start}
	id, err := st.StartBriefRun(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	run.ID, run.EndedAt, run.Outcome, run.Model = id, start.Add(12800*time.Millisecond), "ok", "haiku"
	run.Brief = `{"goal":"Export in batches","context":"exports time out","kind":"feature","size":"m","risk":"medium","risk_why":"touches the export","open_questions":[],"tags":["lang:go","change:feature","area:api"]}`
	run.Tags = []string{"lang:go", "change:feature", "area:api"}
	if err := st.EndBriefRun(ctx, run, dinv.BriefRecord{PR: r7, AttemptOf: "h2"}); err != nil {
		t.Fatal(err)
	}
}

func TestReviewFollowsTheIntake(t *testing.T) {
	fa := &recordingActions{}
	briefs := intaketest.NewBriefs(waitBriefing, intake.Readiness{Go: true})
	d := startIntakeDaemon(t, fa, briefs)
	seedBrief(t, d.store)
	te := newTestEnv(t, &fake.Manager{})
	te.Socket = d.socket
	te.LoadConfig = nil
	done := make(chan int)
	go func() { done <- te.run("review", r7.URL(), "look at", "the migration") }()
	eventually(t, "the intake", func() bool { return d.state(t, 1) == intake.Briefing })
	briefs.End(pr.Ref(r7).Unit())
	if code := <-done; code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	want := `review acme/api#7: accepted as intake 1
briefing acme/api#7 (attempt 1; briefs take 10 to 20 s)
brief ready in 12.8 s: feature, size m, risk medium; lang:go, change:feature, area:api
starting the review
did review acme/api#7 look at the migration
review acme/api#7: done by the daemon
`
	if got := te.out.String(); got != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", got, want)
	}
}

func TestReviewNoWait(t *testing.T) {
	fa := &recordingActions{}
	d := startIntakeDaemon(t, fa, nil)
	te := newTestEnv(t, &fake.Manager{})
	te.Socket = d.socket
	te.LoadConfig = nil
	if code := te.run("review", r7.URL(), "--no-wait"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := te.out.String(); got != "review acme/api#7: accepted as intake 1\n" {
		t.Fatalf("stdout %q", got)
	}
	eventually(t, "the intake", func() bool { return slices.Equal(fa.seen(), []string{"review acme/api#7 "}) })
}

func TestReviewNoBriefStartsAtOnce(t *testing.T) {
	fa := &recordingActions{}
	briefs := intaketest.NewBriefs(waitBriefing)
	d := startIntakeDaemon(t, fa, briefs)
	te := newTestEnv(t, &fake.Manager{})
	te.Socket = d.socket
	te.LoadConfig = nil
	if err := d.store.SetOverride(context.Background(), dinv.Override{PR: r7, Adopted: true}); err != nil {
		t.Fatal(err)
	}
	if code := te.run("address", r7.URL(), "--no-brief", "skip the nit"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if !strings.Contains(te.out.String(), intake.NoteNoBrief) || !slices.Equal(fa.seen(), []string{"address acme/api#7 skip the nit"}) {
		t.Fatalf("stdout %s, calls %v", te.out, fa.seen())
	}
	if len(briefs.Asked()) != 0 {
		t.Fatalf("asked for the brief: %v", briefs.Asked())
	}
}

func TestInterruptDetaches(t *testing.T) {
	d := startIntakeDaemon(t, &recordingActions{}, intaketest.NewBriefs(waitBriefing))
	te := newTestEnv(t, &fake.Manager{})
	te.Socket = d.socket
	te.LoadConfig = nil
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int)
	go func() { done <- Run(ctx, te.Env, []string{"review", r7.URL()}) }()
	eventually(t, "the intake", func() bool { return d.state(t, 1) == intake.Briefing })
	time.Sleep(50 * time.Millisecond)
	cancel()
	if code := <-done; code != 130 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if !strings.HasSuffix(te.out.String(), "intake 1 goes on in the daemon; follow it with "+brand.Command+" intake 1, stop it with "+brand.Command+" intake cancel 1\n") {
		t.Fatalf("stdout %q", te.out)
	}
	if got := d.state(t, 1); got != intake.Briefing {
		t.Fatalf("the interrupt changed the intake: %s", got)
	}
}

func TestIntakeCommands(t *testing.T) {
	fa := &recordingActions{}
	d := startIntakeDaemon(t, fa, intaketest.NewBriefs(waitBriefing))
	te := newTestEnv(t, &fake.Manager{})
	te.Socket = d.socket
	te.LoadConfig = nil
	if err := d.store.SetOverride(context.Background(), dinv.Override{PR: r7, Adopted: true}); err != nil {
		t.Fatal(err)
	}
	if code := te.run("review", r7.URL(), "--no-wait"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if code := te.run("address", r7.URL(), "--no-wait"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	eventually(t, "the intake", func() bool { return d.state(t, 1) == intake.Briefing && d.state(t, 2) == intake.Briefing })
	te.out.Reset()
	if code := te.run("intake", "cancel", "2"); code != 0 || te.out.String() != "intake 2 canceled\n" {
		t.Fatalf("exit %d: %s %s", code, te.out, te.err)
	}
	if code := te.run("intake", "cancel", "2"); code != 1 {
		t.Fatalf("a second cancel: exit %d", code)
	}
	te.out.Reset()
	if code := te.run("intake", "without", "1"); code != 0 || te.out.String() != "intake 1 starts without the brief\n" {
		t.Fatalf("exit %d: %s %s", code, te.out, te.err)
	}
	eventually(t, "the intake", func() bool { return d.state(t, 1) == intake.Started })
	te.out.Reset()
	if code := te.run("intake", "1"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if out := te.out.String(); !strings.Contains(out, intake.NoteWithout) || !strings.HasSuffix(out, "review acme/api#7: done by the daemon\n") {
		t.Fatalf("stdout %s", out)
	}
	if code := te.run("intake", "frobnicate", "1"); code != 1 || !strings.Contains(te.err.String(), "usage: "+brand.Command+" intake") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
}

func TestInstructionsPrintsTheNotReadyText(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "intakes.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	mu := &sync.Mutex{}
	srv := daemon.New(withRows(daemon.Deps{Pauses: te.Rounds, Turns: te.Turns, Views: te.Views, Actions: envActions{te.Env, te.Store}, Intents: te.loadedIntents, TurnByHand: te.byHand(mu), LoopMu: mu,
		Intakes: db.Intakes(), IntakeBriefs: intaketest.NewBriefs(waitBriefing), ByHandRef: te.byHandRef, SessionWait: 50 * time.Millisecond,
		Cfg: te.cfg, Version: "test", Now: te.Now, Viewer: func(context.Context) (string, []string, error) { return "me", nil, nil }, Log: t.Logf}, te.Store))
	te.Socket = serveSocketOf(t, srv.Handler())
	if code := te.run("instructions", "review", r7.URL()); code != 1 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if !strings.Contains(te.err.String(), "the brief of acme/api#7 is not ready yet (briefing, attempt 1); run this command again in a minute") || te.out.Len() != 0 {
		t.Fatalf("stdout %q stderr %q", te.out, te.err)
	}
	open, err := db.Intakes().Open(context.Background())
	if err != nil || len(open) != 1 || open[0].Source != "session" {
		t.Fatalf("open intakes %+v %v", open, err)
	}
}

func TestATurnByHandStartsUnderTheHeldLoopMutex(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "intakes.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	mu := &sync.Mutex{}
	srv := daemon.New(withRows(daemon.Deps{Pauses: te.Rounds, Turns: te.Turns, Views: te.Views, Actions: envActions{te.Env, te.Store}, Intents: te.loadedIntents, TurnByHand: te.byHand(mu), LoopMu: mu,
		Intakes: db.Intakes(), IntakeBriefs: intaketest.NewBriefs(intake.Readiness{Go: true}), ByHandRef: te.byHandRef,
		Cfg: te.cfg, Version: "test", Now: te.Now, Viewer: func(context.Context) (string, []string, error) { return "me", nil, nil }, Log: t.Logf}, te.Store))
	te.Socket = serveSocketOf(t, srv.Handler())
	done := make(chan int)
	go func() { done <- te.run("instructions", "review", r7.URL()) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the turn by hand waited on the loop mutex its intake holds")
	}
	if !mu.TryLock() {
		t.Fatal("the loop mutex is still held")
	}
	open, err := db.Intakes().Open(context.Background())
	if err != nil || len(open) != 0 {
		t.Fatalf("open intakes %+v %v", open, err)
	}
}
