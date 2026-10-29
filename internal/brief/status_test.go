package brief

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory/inventorytest"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

func statusOf(t *testing.T, ctx context.Context, q *Queue) Status {
	t.Helper()
	st, err := q.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestStatusReadsTheBriefStatesOnce(t *testing.T) {
	r := newRig(t, 3)
	r.q.Run(context.Background())
	for range 2 {
		ctx, sc := metertest.Start(t, context.Background())
		if st := statusOf(t, ctx, r.q); st.Ready != 3 {
			t.Fatalf("%+v", st)
		}
		metertest.Within(t, sc, metertest.Budget{{Res: meter.SQL}: 2})
	}
}

func TestStatusRecomputesOnlyOnBriefOrPRNotes(t *testing.T) {
	r := newRig(t, 3)
	r.q.d.Changes = r.changes
	ctx := context.Background()
	if st := statusOf(t, ctx, r.q); st.Pending != 3 {
		t.Fatalf("before any brief: %+v", st)
	}

	mctx, sc := metertest.Start(t, ctx)
	r.changes.Note(port.TopicLoopEvents|port.TopicSessions, refN(1).Unit())
	for range 10 {
		statusOf(t, mctx, r.q)
	}
	metertest.Within(t, sc, metertest.Budget{{Res: meter.SQL}: 0})

	if _, err := r.q.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if st := statusOf(t, ctx, r.q); st.Ready != 3 || st.Pending != 0 {
		t.Fatalf("after the briefs were stored: %+v", st)
	}

	pr, _, err := inventorytest.Of(r.st).PR(ctx, refN(2))
	if err != nil {
		t.Fatal(err)
	}
	pr.ChangedFiles += RefreshFiles + 1
	if err := inventorytest.Of(r.st).UpsertPR(ctx, pr); err != nil {
		t.Fatal(err)
	}
	mctx, sc = metertest.Start(t, ctx)
	if st := statusOf(t, mctx, r.q); st.Ready != 2 || st.Pending != 1 {
		t.Fatalf("after a pull request moved: %+v", st)
	}
	statusOf(t, mctx, r.q)
	metertest.Within(t, sc, metertest.Budget{{Res: meter.SQL}: 2})
}

func TestStatusRecomputesOnTheMinute(t *testing.T) {
	r := newRig(t, 2)
	r.q.d.Changes = r.changes
	ctx := context.Background()
	r.src.fail["acme/api#1"] = errors.New("gh pr view: GraphQL: Could not resolve to a PullRequest with the number of 1.")
	if _, err := r.q.Run(ctx); err != nil {
		t.Fatal(err)
	}
	pr, _, err := inventorytest.Of(r.st).PR(ctx, refN(2))
	if err != nil {
		t.Fatal(err)
	}
	pr.BodyEditedAt = r.now
	if err := inventorytest.Of(r.st).UpsertPR(ctx, pr); err != nil {
		t.Fatal(err)
	}
	if st := statusOf(t, ctx, r.q); st.Failed != 1 || st.Ready != 1 {
		t.Fatalf("an edit still quiet: %+v", st)
	}

	r.now = t0.Add(BodyQuiet - 30*time.Second)
	mctx, sc := metertest.Start(t, ctx)
	if st := statusOf(t, mctx, r.q); st.Ready != 1 {
		t.Fatalf("minutes later, still quiet: %+v", st)
	}
	metertest.Within(t, sc, metertest.Budget{{Res: meter.SQL}: 2})

	r.now = t0.Add(BodyQuiet - 10*time.Second)
	mctx, sc = metertest.Start(t, ctx)
	if st := statusOf(t, mctx, r.q); st.Ready != 1 {
		t.Fatalf("the same minute keeps the counts: %+v", st)
	}
	metertest.Within(t, sc, metertest.Budget{{Res: meter.SQL}: 0})

	r.now = t0.Add(BodyQuiet)
	if st := statusOf(t, ctx, r.q); st.Ready != 0 || st.Pending != 1 || st.Failed != 1 {
		t.Fatalf("past the quiet period: %+v", st)
	}
}

func TestStatusKeepsTheBreakerLive(t *testing.T) {
	r := newRig(t, 1)
	r.q.d.Changes = r.changes
	ctx := context.Background()
	statusOf(t, ctx, r.q)
	r.q.breaker = breaker{open: true, since: t0, next: t0.Add(time.Hour), lastErr: "no agent"}
	if st := statusOf(t, ctx, r.q); !st.PausedUntil.Equal(t0.Add(time.Hour)) || st.LastError != "no agent" {
		t.Fatalf("%+v", st)
	}
}
