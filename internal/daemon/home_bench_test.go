package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/task"
)

// homeRig is a tasks rig holding n open tasks, one in every 25 with an open question.
func homeRig(b *testing.B, n int) *tasksRig {
	r := newTasksRig(b, nil)
	es := make([]task.Task, n)
	for i := range es {
		es[i] = task.Task{ID: task.ID(fmt.Sprintf("t-%016x", i+1)), State: "next", Category: task.CategoryReady, Title: fmt.Sprintf("task %d", i)}
	}
	r.put(b, es...)
	for i := 0; i < n; i += 25 {
		r.withGates(b, es[i].ID)
	}
	return r
}

func getB(b *testing.B, s *Server, path string, out any) int {
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, apiRequest("GET", path, nil))
	if rec.Code == 200 {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			b.Fatal(err)
		}
	}
	return rec.Code
}

func benchHome(b *testing.B, n int) {
	r := homeRig(b, n)
	ctx := context.Background()
	b.Run("counts", func(b *testing.B) {
		for b.Loop() {
			if _, err := r.g.Counts(ctx, ""); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("home-views", func(b *testing.B) {
		views := []task.View{"home-blocked", "home-stalled", "decide-answer", "decide-approve-plan", "decide-send-draft",
			"decide-review-step", "decide-last-step", "decide-pick-someone", "decide-act"}
		for b.Loop() {
			if _, err := r.g.Sets(ctx, views, nil); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("api-home", func(b *testing.B) {
		for b.Loop() {
			var h api.Home
			if code := getB(b, r.s, api.HomePath, &h); code != 200 {
				b.Fatal(code)
			}
		}
	})
}

func BenchmarkHome150(b *testing.B)  { benchHome(b, 150) }
func BenchmarkHome5000(b *testing.B) { benchHome(b, 5000) }
