package daemon

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/task"
)

func queuePath(kind string) string { return strings.Replace(api.QueuePath, "{kind}", kind, 1) }

func TestQueueServesTheKindsItems(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, task.Task{ID: "t-0000000000000001", State: "next", Category: task.CategoryReady, Title: "ship the retries"})
	g := r.withGates(t, "t-0000000000000001")
	var q api.Queue
	if code := get(t, r.s, queuePath("answer"), &q); code != 200 {
		t.Fatalf("%d", code)
	}
	if q.Kind != "answer" || q.Title != "Agent questions" || q.Total != 1 || len(q.Items) != 1 {
		t.Fatalf("queue %+v", q)
	}
	it := q.Items[0]
	if it.Task.ID != "t-0000000000000001" || it.Gate == nil || it.Gate.ID != g.ID || it.Situation != nil {
		t.Fatalf("item %+v", it)
	}
	if it.Primary.Kind != "gate" || it.Primary.ID != g.Intents[0] || it.Primary.Label == "" || it.Primary.Label == it.Primary.ID {
		t.Fatalf("primary %+v, want the gate's first intent with its label", it.Primary)
	}
}

func TestQueueNarrowedByLens(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, task.Task{ID: "t-0000000000000001", State: "next", Category: task.CategoryReady, Title: "ship the retries"},
		task.Task{ID: "t-0000000000000002", State: "next", Category: task.CategoryReady, Title: "call Ada"})
	r.withGates(t, "t-0000000000000001")
	r.withGates(t, "t-0000000000000002")
	var all, narrowed api.Queue
	get(t, r.s, queuePath("answer"), &all)
	if code := get(t, r.s, queuePath("answer")+"?q="+url.QueryEscape("retries"), &narrowed); code != 200 {
		t.Fatalf("%d", code)
	}
	if all.Total != 2 || narrowed.Total != 1 || narrowed.Items[0].Task.ID != "t-0000000000000001" {
		t.Fatalf("all %d, narrowed %+v", all.Total, narrowed)
	}
}

func TestQueueUnknownKind(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	rec := httptest.NewRecorder()
	r.s.Handler().ServeHTTP(rec, apiRequest("GET", queuePath("merge"), nil))
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), "last-step") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}
