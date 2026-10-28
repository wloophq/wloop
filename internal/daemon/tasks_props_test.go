package daemon

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/domain/task"
)

func TestTaskViewServesPropsAndParent(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	due := tasksNow.Add(48 * time.Hour)
	child := task.Task{ID: "e-c", State: "next", Category: task.CategoryReady, Title: "c", Parent: "e-p", Position: 1}
	child.SetProp("area", task.Value{Text: "OSS"}, task.WriterPerson, tasksNow)
	child.SetProp("due", task.Value{Time: due}, task.WriterPlugin, tasksNow)
	child.SetProp("contexts", task.Value{List: []string{"@reply"}}, task.WriterPerson, tasksNow)
	child.SetProp("size", task.Value{Dur: 30 * time.Minute}, task.WriterPerson, tasksNow)
	second := task.Task{ID: "e-d", State: "waiting", Category: task.CategoryWaiting, Title: "d", Parent: "e-p", Position: 2}
	r.put(t, task.Task{ID: "e-p", Kind: task.KindProject, State: "next", Category: task.CategoryReady, Title: "P", Slug: "p"}, second, child)
	var got api.TaskView
	if code := get(t, r.s, api.TaskPath("e-c"), &got); code != 200 {
		t.Fatalf("GET the task: %d", code)
	}
	if got.Category != "ready" || got.Parent != "e-p" {
		t.Errorf("category %q parent %q", got.Category, got.Parent)
	}
	want := map[string]string{"area": `"OSS"`, "due": `"` + due.Format(time.RFC3339) + `"`, "contexts": `["@reply"]`, "size": `"30m0s"`}
	for name, w := range want {
		if raw := string(got.Props[name]); raw != w {
			t.Errorf("prop %s %s, want %s", name, raw, w)
		}
	}
	if !got.PropTime("due").Equal(due) || !slices.Equal(got.PropList("contexts"), []string{"@reply"}) {
		t.Errorf("props %v", got.Props)
	}
	p := r.page(t, "view=next")
	i := slices.IndexFunc(p.Tasks, func(v api.TaskView) bool { return v.ID == "e-p" })
	if i < 0 || !slices.Equal(p.Tasks[i].Children, []string{"e-c", "e-d"}) {
		t.Fatalf("the project's children in %+v", p.Tasks)
	}
	var raw map[string]json.RawMessage
	b, _ := json.Marshal(got)
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"focus", "focus_auto", "contexts", "area", "project", "project_title", "deadline"} {
		if _, ok := raw[gone]; ok {
			t.Errorf("the wire still serves %s", gone)
		}
	}
}

// profileStates are a profile's states a test changes, as a reload would.
type profileStates struct {
	mu sync.Mutex
	m  map[string]task.Category
}

func (p *profileStates) set(m map[string]task.Category) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.m = m
}

func (p *profileStates) CategoryOf(s string) (task.Category, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.m[s]
	return c, ok
}

func (p *profileStates) FirstOf(c task.Category) string { return taskstest.BundledStates.FirstOf(c) }

func (p *profileStates) Move(verb string) ([]string, string, bool) {
	return taskstest.BundledStates.Move(verb)
}

func TestTheSyncWorkerRecategorizesAtStartAndOnAReload(t *testing.T) {
	t.Parallel()
	states := &profileStates{m: map[string]task.Category{"next": task.CategoryReady, "blocked": task.CategoryReady}}
	r := newTasksRig(t, nil)
	r.g = coretasks.New(coretasks.Deps{States: states, Store: r.db.Tasks(), Items: r.ledger, Facts: coretasks.CardFacts{Cards: r.db.Cards()},
		Clock: fixedClock(tasksNow)})
	r.s.deps.Tasks = r.g
	r.put(t, task.Task{ID: "e-b", State: "blocked", Category: task.CategoryWaiting, Title: "b"})
	category := func() task.Category {
		e, err := r.db.Tasks().Task(context.Background(), "e-b")
		if err != nil {
			t.Fatal(err)
		}
		return e.Category
	}
	r.startSync(t)
	waitFor(t, func() bool { return category() == task.CategoryReady })
	states.set(map[string]task.Category{"next": task.CategoryReady})
	r.log.Note(port.TopicConfig)
	waitFor(t, func() bool {
		var h api.Health
		get(t, r.s, "/api/health", &h)
		return slices.ContainsFunc(h.Warnings, func(w string) bool { return strings.Contains(w, "states.blocked") })
	})
	if c := category(); c != task.CategoryReady {
		t.Errorf("a task in a removed state changed category to %s", c)
	}
}
