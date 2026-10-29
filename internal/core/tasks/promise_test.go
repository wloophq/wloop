package tasks_test

import (
	"context"
	"errors"
	"testing"
	"time"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
)

func TestAPromiseSetsThePropertyOfRolePromise(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	day := func(h, m int) time.Time { return time.Date(t0.Year(), t0.Month(), t0.Day(), h, m, 0, 0, t0.Location()) }
	for by, want := range map[string]time.Time{
		day(t0.Hour()+2, 0).Format("15:04"): day(t0.Hour()+2, 0),
		day(t0.Hour()-1, 0).Format("15:04"): day(t0.Hour()-1, 0).AddDate(0, 0, 1),
		"2026-12-01":                        deadline(t, "2026-12-01"),
		"":                                  {},
	} {
		r := newRig()
		r.put(t, task.Task{ID: "t-1", State: "inbox", Title: "Answer Ana", Origin: task.OriginUser})
		if _, err := r.svc.Do(ctx, "t-1", coretasks.Request{Verb: "acknowledge", Args: map[string]string{"by": by}, Actor: task.ActorPerson}); err != nil {
			t.Fatalf("by %q: %v", by, err)
		}
		got := r.task(t, "t-1")
		if !got.Prop("promised").Time.Equal(want) || got.State != "next" {
			t.Errorf("by %q: promised %v in %s, want %v in next", by, got.Prop("promised").Time, got.State, want)
		}
	}
	r := newRig()
	r.put(t, task.Task{ID: "t-1", State: "inbox", Title: "Answer Ana", Origin: task.OriginUser})
	if _, err := r.svc.Do(ctx, "t-1", coretasks.Request{Verb: "acknowledge", Args: map[string]string{"by": "soon"}, Actor: task.ActorPerson}); !errors.Is(err, coretasks.ErrArgs) {
		t.Errorf("by soon: %v, want ErrArgs", err)
	}
}

func deadline(t *testing.T, s string) time.Time {
	t.Helper()
	d, ok := task.ParseDeadline(s, t0.Location())
	if !ok {
		t.Fatalf("no deadline %q", s)
	}
	return d
}
