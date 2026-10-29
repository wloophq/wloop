package brief

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory/inventorytest"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
)

const validBrief = `{"goal":"g","kind":"fix","size":"s","risk":"low"}`

func (r *rig) editBody(t *testing.T, n int, at time.Time) {
	t.Helper()
	ctx := context.Background()
	p, _, err := inventorytest.Of(r.st).PR(ctx, refN(n))
	if err != nil {
		t.Fatal(err)
	}
	p.BodyEditedAt = at
	if err := inventorytest.Of(r.st).UpsertPR(ctx, p); err != nil {
		t.Fatal(err)
	}
}

func (r *rig) nextAt(t *testing.T) (time.Time, bool) {
	t.Helper()
	states, err := r.st.BriefStates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return r.q.Next(states, r.now)
}

func TestABodyEditWaitsForTheQuietPeriod(t *testing.T) {
	r := newRig(t, 1)
	r.src.edits = true
	ctx := context.Background()
	r.q.Run(ctx)
	r.editBody(t, 1, t0.Add(time.Minute))
	r.now = t0.Add(time.Minute + BodyQuiet - time.Minute)
	r.q.Run(ctx)
	if r.run.calls != 1 {
		t.Fatalf("%d calls before the quiet period ended", r.run.calls)
	}
	if next, ok := r.nextAt(t); !ok || !next.Equal(t0.Add(time.Minute+BodyQuiet)) {
		t.Fatalf("Next = %v, %v", next, ok)
	}
	r.now = t0.Add(time.Minute + BodyQuiet)
	r.q.Run(ctx)
	if r.run.calls != 2 {
		t.Fatalf("%d calls at the end of the quiet period", r.run.calls)
	}
}

func TestTwoEditsFiveMinutesApartMakeOneBrief(t *testing.T) {
	r := newRig(t, 1)
	r.src.edits = true
	ctx := context.Background()
	r.q.Run(ctx)
	r.editBody(t, 1, t0.Add(time.Minute))
	r.now = t0.Add(6 * time.Minute)
	r.editBody(t, 1, r.now)
	r.q.Run(ctx)
	r.now = t0.Add(6*time.Minute + BodyQuiet - time.Second)
	r.q.Run(ctx)
	r.now = t0.Add(6*time.Minute + BodyQuiet)
	r.q.Run(ctx)
	r.q.Run(ctx)
	if r.run.calls != 2 {
		t.Fatalf("%d agent calls, want 1 for the first brief and 1 for both edits", r.run.calls)
	}
}

func TestAWantedRunIgnoresTheQuietPeriod(t *testing.T) {
	r := newRig(t, 1)
	saveRun(t, r.st, refN(1), `{"goal":"g","kind":"epic","size":"s","risk":"low"}`, "")
	r.now = t0.Add(10 * time.Minute)
	r.editBody(t, 1, r.now.Add(-time.Minute))
	rd := r.ready(t, refN(1), true)
	r.settled()
	runs := runsOf(t, r.st)
	if rd.State != Wait || r.run.calls != 1 || runs[len(runs)-1].Trigger != TriggerWanted {
		t.Fatalf("%+v, %d calls, runs %+v", rd, r.run.calls, runs)
	}
}

func TestAListingAfterAVersionChangeStartsNoRuns(t *testing.T) {
	r := newRig(t, 20)
	ctx := context.Background()
	for i := 1; i <= 20; i++ {
		saveRun(t, r.st, refN(i), validBrief, "")
	}
	for i := 1; i <= 20; i++ {
		p, _, _ := inventorytest.Of(r.st).PR(ctx, refN(i))
		if _, err := viewOf(ctx, r.q, p); err != nil {
			t.Fatal(err)
		}
	}
	r.settled()
	if r.run.calls != 0 {
		t.Fatalf("%d agent calls from reading", r.run.calls)
	}
}

func TestAVersionChangeBriefsNothingInTheBackground(t *testing.T) {
	r := newRig(t, 70)
	ctx := context.Background()
	for i := 1; i <= 70; i++ {
		saveRun(t, r.st, refN(i), validBrief, "")
	}
	r.run.calls = 0
	for range 3 {
		r.q.Run(ctx)
	}
	if err := r.tick(ctx); err != nil {
		t.Fatal(err)
	}
	if r.run.calls != 0 {
		t.Fatalf("%d agent calls", r.run.calls)
	}
	select {
	case <-r.q.Due():
		t.Fatal("the job was told a version change is due")
	default:
	}
}

func TestAFirstTurnTakesAnOlderFormatBriefAndRefreshesIt(t *testing.T) {
	r := newRig(t, 1)
	saveRun(t, r.st, refN(1), validBrief, "")
	rd := r.ready(t, refN(1), true)
	r.settled()
	if rd.State != Ready || rd.Brief == nil || rd.Brief.Goal != "g" {
		t.Fatalf("%+v", rd)
	}
	runs := runsOf(t, r.st)
	if r.run.calls != 1 || runs[len(runs)-1].Trigger != TriggerRefresh {
		t.Fatalf("%d calls, runs %+v", r.run.calls, runs)
	}
	if rec := r.record(t, refN(1)); rec.Version != Version(DefaultTaxonomy) || !rec.WantedAt.IsZero() {
		t.Fatalf("%+v", rec)
	}
}

func TestAnOlderBriefThatNoLongerValidatesIsWaitedOn(t *testing.T) {
	r := newRig(t, 1)
	saveRun(t, r.st, refN(1), `{"goal":"g","kind":"epic","size":"s","risk":"low"}`, "")
	rd := r.ready(t, refN(1), true)
	r.settled()
	if rd.State != Wait || rd.Wait != WaitBriefing {
		t.Fatalf("%+v", rd)
	}
	runs := runsOf(t, r.st)
	if runs[len(runs)-1].Trigger != TriggerWanted {
		t.Fatalf("runs %+v", runs)
	}
}

func TestViewRefreshesAStaleBriefOnce(t *testing.T) {
	r := newRig(t, 1)
	ctx := context.Background()
	saveRun(t, r.st, refN(1), validBrief, "")
	p, _, _ := inventorytest.Of(r.st).PR(ctx, refN(1))
	unblock, entered := make(chan struct{}), make(chan struct{})
	r.run.takes = func() {
		close(entered)
		<-unblock
	}
	v, err := viewOf(ctx, r.q, p)
	if err != nil || !v.Stale || v.Brief == nil {
		t.Fatalf("%+v %v", v, err)
	}
	if r.run.calls != 0 {
		t.Fatal("View started a run")
	}
	if err := r.q.RefreshIfStale(ctx, p); err != nil {
		t.Fatal(err)
	}
	<-entered
	if err := r.q.RefreshIfStale(ctx, p); err != nil {
		t.Fatal(err)
	}
	close(unblock)
	r.settled()
	if r.run.calls != 1 {
		t.Fatalf("%d runs", r.run.calls)
	}
	if v, _ := viewOf(ctx, r.q, p); v.Stale {
		t.Fatalf("still stale after the refresh: %+v", v)
	}
	r.q.RefreshIfStale(ctx, p)
	r.settled()
	if r.run.calls != 1 {
		t.Fatalf("%d runs after the refresh", r.run.calls)
	}
}

func TestABreakerEventNotesTheLoopEventsOnly(t *testing.T) {
	r := newRig(t, 1)
	log := change.NewLog(change.DefaultWindow, nil)
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "noted.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r.q.d.Store = inventory.New(db)
	from := log.Gen()
	r.q.recordBreaker(context.Background(), breakerOpened, dinv.BriefRun{EndedAt: t0, Error: "boom"})
	got := log.Since(from, ^port.Topic(0))
	if !got.Has(port.TopicLoopEvents) || got.Has(port.TopicSourceEvents) {
		t.Fatalf("topics %b", got.Topics)
	}
}
