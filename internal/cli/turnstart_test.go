package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/headless"
	"github.com/wloophq/wloop/internal/core/lock"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/daemon"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/workspace/fake"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// editedDefs are defs with edit applied to each intent, as a profile whose
// intent's role has a prepare and whose guidance has a max would load it.
type editedDefs struct {
	turns.Defs
	edit func(d *turns.Def)
}

func (d editedDefs) Def(name dturn.Intent) (turns.Def, bool) {
	def, ok := d.Defs.Def(name)
	if ok {
		d.edit(&def)
	}
	return def, ok
}

// actWithPrepare is the profile's act with a guidance.max of 12 and an
// input its prepare fills.
func actWithPrepare(t *testing.T, te *testEnv) {
	t.Helper()
	act, err := readBundled("intents/act/intent.toml")
	if err != nil {
		t.Fatal(err)
	}
	ours := strings.Replace(string(act), "[guidance]\n", "[guidance]\nmax = 12\n", 1) + `
[inputs.situation]
type   = "text"
from   = "prepare"
hidden = true
`
	userProfile(t, te, map[string]string{"intents/act/intent.toml": ours})
}

// preparedAct gives act's role a prepare, run by p.
func preparedAct(st *turns.Starter, p turns.Preparer) {
	st.Preparer = startPreparer{p}
	st.Defs = editedDefs{Defs: st.Defs, edit: func(d *turns.Def) {
		if d.Name == dintent.Act {
			d.Prepare = "action:act.prepare"
		}
	}}
}

const longGuidance = "Ask about the retries first."

func TestAHeadlessActionStartsThroughItsRolesPrepareOnItsCutGuidance(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.cfg = withoutWindows(te.cfg)
	te.cfg.Placement = []config.PlacementRule{{Match: "slack:#general", Checkout: t.TempDir(), Role: "work"}}
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), storeFile), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	runSlackPlugin(t, te, db)
	actWithPrepare(t, te)
	ctx := context.Background()
	loop := &lock.Mutex{}
	d := te.serveDeps(ctx, db, te.cfg, t.Logf, loop, &daemon.AppState{})
	if d.Startup != nil {
		if _, err := d.Startup(ctx); err != nil {
			t.Fatal(err)
		}
		d.Startup = nil
	}
	svc, ok := d.Headless.(*headless.Service)
	if !ok || svc.Starter == nil || svc.Starter != te.Turns.Starter {
		t.Fatalf("headless %T is not started through the turn service's starter", d.Headless)
	}
	if l, ok := svc.Starter.Loop.(serveLoop); !ok || l.mu != loop {
		t.Fatalf("the starter's loop %+v is not the daemon's", svc.Starter.Loop)
	}
	p := &fakePreparer{filled: map[string]any{"situation": "Waiting on ana since Monday."}}
	preparedAct(svc.Starter, p)
	a := &promptAsker{}
	svc.Asker = a
	svc.Go = func(run func()) { run() }
	te.Socket = serveSocket(t, daemon.New(d).Handler())
	c := client.New(te.Socket)
	if _, err := c.Action(ctx, slackThread2, "act", api.ActionRequest{Guidance: longGuidance}); err != nil {
		t.Fatalf("act: %v\n%s", err, te.err)
	}
	b := slackBook(t, te, slackThread2)
	if len(b.Turns) != 1 || b.Turns[0].Guidance != "Ask about th" || b.Turns[0].Inputs["situation"] != "Waiting on ana since Monday." || len(a.asks) != 1 {
		t.Fatalf("turns %+v, %d calls", b.Turns, len(a.asks))
	}

	p.refusal = fmt.Errorf("%w: nobody to ask", src.ErrUnfit)
	_, err = c.Action(ctx, slackThread1, "act", api.ActionRequest{})
	if err == nil || !strings.Contains(err.Error(), "nobody to ask") {
		t.Fatalf("a refused prepare: %v", err)
	}
	if b := slackBook(t, te, slackThread1); len(b.Turns) != 0 || len(a.asks) != 1 {
		t.Fatalf("a refused prepare started %+v, %d calls", b.Turns, len(a.asks))
	}
}

// startedHeadless is a headless runner over te's turns whose starts go
// through te's starter, its calls answered by a.
func startedHeadless(t *testing.T, te *testEnv, a turns.Asker) *headless.Service {
	t.Helper()
	return &headless.Service{Turns: te.Turns, Starter: te.Turns.Starter, Build: headlessBuilder{e: te.Env, cfg: te.cfg}, Place: headlessPlacer{e: te.Env, cfg: te.cfg},
		Asker: a, Intents: te.Env.headlessIntent, Default: te.Env.headlessDefault, Clock: clockFunc(te.Env.Now), Go: func(run func()) { run() },
		Log: t.Logf, Wrapper: te.Env.headlessWrapper(t.Logf)}
}

func headlessActRig(t *testing.T, p *fakePreparer) (*unitStartRig, *promptAsker) {
	t.Helper()
	r := newUnitStartRig(t, implementIntent)
	actWithPrepare(t, r.te)
	r.st.Args = map[string]any{"intent": string(dintent.Act), "runner": string(dturn.RunnerHeadless)}
	preparedAct(r.te.Turns.Starter, p)
	a := &promptAsker{}
	r.steps.d.Headless = startedHeadless(t, r.te, a)
	return r, a
}

func TestAHeadlessWorkflowTurnStartsThroughItsRolesPrepareOnItsCutGuidance(t *testing.T) {
	p := &fakePreparer{filled: map[string]any{"situation": "Waiting on ana since Monday."}}
	r, a := headlessActRig(t, p)
	if _, err := r.steps.Run(context.Background(), r.st, thread1, r.why, automation.RunOpts{Guidance: longGuidance}); err != nil {
		t.Fatalf("%v\n%s", err, r.te.err)
	}
	b := r.book(t, thread1)
	if len(b.Turns) != 1 || b.Turns[0].Runner != dturn.RunnerHeadless || b.Turns[0].Guidance != "Ask about th" ||
		b.Turns[0].Inputs["situation"] != "Waiting on ana since Monday." || b.Turns[0].Workflow != implementWorkflow || len(a.asks) != 1 {
		t.Fatalf("turns %+v, %d calls", b.Turns, len(a.asks))
	}
	if want := []string{string(dintent.Act) + " " + thread1.String()}; !slices.Equal(p.asked, want) {
		t.Fatalf("prepares asked %v, want %v", p.asked, want)
	}
}

func TestAHeadlessWorkflowTurnItsPrepareRefusesStartsNothing(t *testing.T) {
	r, a := headlessActRig(t, &fakePreparer{refusal: fmt.Errorf("%w: nobody to ask", src.ErrUnfit)})
	_, err := r.steps.Run(context.Background(), r.st, thread1, r.why, automation.RunOpts{})
	var refused automation.Refused
	if !errors.As(err, &refused) || refused.Reason != "nobody to ask" {
		t.Fatalf("run: %v, want refused with the prepare's reason", err)
	}
	if b := r.book(t, thread1); len(b.Turns) != 0 || len(a.asks) != 0 {
		t.Fatalf("a refused prepare started %+v, %d calls", b.Turns, len(a.asks))
	}
}

func TestTheStartsDefsHandGuidanceOnlyWithItsEffect(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	defs := loadedDefs{te.Env}
	for name, hands := range map[dintent.Name]bool{dintent.Review: true, dintent.Address: true, dintent.Nudge: false, dintent.Act: false} {
		d, ok := defs.Def(dturn.Intent(name))
		if !ok || d.HandsGuidance != hands {
			t.Errorf("%s: %v, hands guidance %v, want %v", name, ok, d.HandsGuidance, hands)
		}
	}
	if _, ok := defs.Def("no-such-intent"); ok {
		t.Error("an intent the profile lacks loaded")
	}
}

func TestAStartsPrepareRefusalIsRefusedWithItsText(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		err      error
		kind     error
		kindName string
	}{
		{fmt.Errorf("%w: nobody to ask", src.ErrUnfit), turns.ErrRefused, "refused"},
		{fmt.Errorf("%w: the plugin is restarting", src.ErrUnavailable), turns.ErrTransient, "transient"},
	} {
		_, err := startPreparer{&fakePreparer{refusal: c.err}}.PrepareTurn(context.Background(), "act", thread1, "", nil)
		if !errors.Is(err, c.kind) || !errors.Is(err, c.err) || err.Error() != c.err.Error() {
			t.Errorf("%s: %v", c.kindName, err)
		}
	}
	other := errors.New("the store is locked")
	if _, err := (startPreparer{&fakePreparer{refusal: other}}).PrepareTurn(context.Background(), "act", thread1, "", nil); err != other {
		t.Errorf("another error: %v", err)
	}
}

func TestTheStartEntersTheLoopUnderItsMutex(t *testing.T) {
	t.Parallel()
	mu := &lock.Mutex{}
	l := serveLoop{e: &Env{}, mu: mu}
	mu.Lock()
	done, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := l.Enter(done, unit.Unit{Provider: "thread", Key: "C1/1"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("entering a held loop: %v", err)
	}
	mu.Unlock()
	_, release, err := l.Enter(context.Background(), unit.Unit{Provider: "thread", Key: "C1/1"})
	if err != nil {
		t.Fatal(err)
	}
	if mu.TryLock() {
		t.Fatal("the loop was entered without its mutex")
	}
	release()
	if !mu.TryLock() {
		t.Fatal("the release kept the mutex")
	}
}
