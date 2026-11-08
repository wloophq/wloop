package daemon

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// BenchmarkReviewStepQueue serves the review-step queue of 200 open step gates, each on a task holding one unit,
// among 5000 tasks.
func BenchmarkReviewStepQueue(b *testing.B) {
	ctx := context.Background()
	r := newTasksRig(b, nil)
	var ts []task.Task
	for i := range 5000 {
		ts = append(ts, task.Task{ID: task.ID(fmt.Sprintf("t-%016x", i+1)), State: "next", Category: task.CategoryReady, Title: fmt.Sprintf("task %d", i),
			Units: []unit.Unit{{Provider: "thread", Key: fmt.Sprint(i)}}})
	}
	r.put(b, ts...)
	r.withGates(b, ts[0].ID)
	for _, e := range ts[1:201] {
		if _, err := r.s.deps.Gates.Open(ctx, gate.Gate{Name: "review-diff", Unit: task.UnitOf(e.ID), Subject: "start-review-diff"}, gates.Policy(nil)); err != nil {
			b.Fatal(err)
		}
	}
	h := r.s.Handler()
	for b.Loop() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, apiRequest("GET", queuePath("review-step"), nil))
		if rec.Code != 200 {
			b.Fatal(rec.Code)
		}
	}
}
