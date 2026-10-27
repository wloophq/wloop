package intake_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/intake/intaketest"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestServiceOnTheFake(t *testing.T) {
	intaketest.Service(t, func(*testing.T) intake.Store { return intaketest.NewStore() })
}

type countingStore struct {
	*intaketest.Store
	saves atomic.Int32
}

func (s *countingStore) Save(ctx context.Context, i intake.Intake, evs ...port.Event) error {
	s.saves.Add(1)
	return s.Store.Save(ctx, i, evs...)
}

type written struct {
	pub    *intaketest.Publisher
	row    intake.Intake
	during int32
}

func writeLines(t *testing.T, lines int, step time.Duration, tail string, fail error) written {
	t.Helper()
	store := &countingStore{Store: intaketest.NewStore()}
	pub := &intaketest.Publisher{}
	clock := intaketest.NewClock(time.Date(2026, 10, 18, 13, 0, 0, 0, time.UTC))
	var before, during int32
	starter := &intaketest.Starter{Fn: func(_ context.Context, _ intake.Intake, out io.Writer) (intake.StartedTurn, error) {
		before = store.saves.Load()
		for n := range lines {
			fmt.Fprintf(out, "line %d\n", n)
			clock.Advance(step)
		}
		during = store.saves.Load() - before
		fmt.Fprint(out, tail)
		return intake.StartedTurn{TurnSeq: 1, TurnID: "r-1"}, fail
	}}
	svc := &intake.Service{Store: store, Briefs: intaketest.NewBriefs(intake.Readiness{Go: true}), Firsts: intaketest.Firsts(true),
		Starter: starter, Payloads: intaketest.Payloads{}, Clock: clock, Pub: pub, After: (&intaketest.Timers{}).After}
	i, _, err := svc.Accept(context.Background(), intake.Request{
		Unit: unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}, Intent: "review", Source: "cli", NoBrief: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Drive(context.Background(), i.Seq); err != nil {
		t.Fatal(err)
	}
	row, _, err := store.Intake(context.Background(), i.Seq)
	if err != nil {
		t.Fatal(err)
	}
	return written{pub: pub, row: row, during: during}
}

func TestALogOfManyLinesSavesAtMostOncePerSecond(t *testing.T) {
	w := writeLines(t, 100, 5*time.Millisecond, "", nil)
	if w.during != 1 {
		t.Fatalf("%d saves while the action wrote 100 lines in 500 ms, want 1", w.during)
	}
	n := 0
	for _, ev := range w.pub.Events() {
		if ev.Kind == intake.KindLog && strings.HasPrefix(ev.Data, "line ") {
			n++
		}
	}
	if n != 100 {
		t.Fatalf("%d log notices published, want 100", n)
	}
	if c := countPrefix(w.row.Log, "line "); c != 100 {
		t.Fatalf("the final row holds %d lines, want 100", c)
	}
}

func TestTheLastLinesAreSavedAtTheEnd(t *testing.T) {
	w := writeLines(t, 7, 100*time.Millisecond, "tail", nil)
	if w.during >= 7 {
		t.Fatalf("%d saves for 7 lines, the gate skipped none", w.during)
	}
	if c := countPrefix(w.row.Log, "line "); c != 7 || w.row.Log[len(w.row.Log)-1] != "tail" {
		t.Fatalf("the final row's log is %q", w.row.Log)
	}
}

func TestAFailedActionKeepsEveryLineItWrote(t *testing.T) {
	w := writeLines(t, 7, 100*time.Millisecond, "tail", errors.New("boom"))
	if w.row.State != intake.Failed {
		t.Fatalf("state %v, want failed", w.row.State)
	}
	if c := countPrefix(w.row.Log, "line "); c != 7 || w.row.Log[len(w.row.Log)-1] != "tail" {
		t.Fatalf("the failed row's log is %q", w.row.Log)
	}
}

func TestADriveLeavesNoTrailingSaveRunning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		writeLines(t, 100, 5*time.Millisecond, "", nil)
	})
}

func countPrefix(log []string, p string) int {
	n := 0
	for _, l := range log {
		if strings.HasPrefix(l, p) {
			n++
		}
	}
	return n
}
