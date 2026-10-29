package daemon

import (
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

func digestRig(t *testing.T) (*tasksRig, time.Time) {
	t.Helper()
	r := newTasksRig(t, func(d *Deps) {
		d.Cfg.Notifications.DigestAt = "09:00"
		d.PluginEnabled = func(p unit.ProviderID) bool { return p != "slack" }
	})
	return r, time.Date(2026, 10, 8, 9, 0, 0, 0, time.Local)
}

func at(e task.Task, t time.Time) task.Task {
	e.Created, e.Updated = t.Add(-48*time.Hour), t
	return e
}

func TestDigestContents(t *testing.T) {
	t.Parallel()
	r, end := digestRig(t)
	r.put(t,
		at(task.Task{ID: "e-next", State: "next", Category: task.CategoryReady}, end.Add(-time.Hour)),
		at(task.Task{ID: "e-wait", State: "waiting", Category: task.CategoryWaiting}, end.Add(-20*time.Hour)),
		at(task.Task{ID: "e-old", State: "next", Category: task.CategoryReady}, end.Add(-25*time.Hour)),
		at(task.Task{ID: "e-later", State: "next", Category: task.CategoryReady}, end.Add(time.Minute)),
		at(task.Task{ID: "e-in", State: "inbox", Category: task.CategoryInbox}, end.Add(-time.Hour)),
		at(task.Task{ID: "e-off", State: "next", Category: task.CategoryReady, Units: []unit.Unit{{Provider: "slack", Key: "T000/C000/1.2"}}}, end.Add(-time.Hour)),
	)
	var d api.Digest
	if code := get(t, r.s, api.DigestPath+"?day=2026-10-08", &d); code != 200 {
		t.Fatalf("%d", code)
	}
	if d.Day != "2026-10-08" || !slices.Equal(slices.Sorted(slices.Values(d.Tasks)), []string{"e-in", "e-next", "e-wait"}) || d.Title == "" ||
		d.Counts.Views["inbox"] != 1 || d.Counts.Views["needs-me"] < 3 || !strings.Contains(d.Body, "3 new since the last digest · 1 in Inbox · ") {
		t.Fatalf("%+v", d)
	}
	if code := get(t, r.s, api.DigestPath+"?day=08/10/2026", &d); code != 400 {
		t.Fatalf("a bad day: %d", code)
	}
}

func TestEmptyDigest(t *testing.T) {
	t.Parallel()
	r, end := digestRig(t)
	r.put(t, at(task.Task{ID: "e-old", State: "next", Category: task.CategoryReady}, end.Add(-30*time.Hour)))
	raw := getRaw(t, r.s, api.DigestPath+"?day=2026-10-08")
	if !strings.Contains(raw, `"tasks":null`) {
		t.Fatalf("%s", raw)
	}
}

func TestDigestReadsViewsAndProfileWords(t *testing.T) {
	t.Parallel()
	l := *bundledLoaded()
	l.Views.Badge = []string{"someday", "logbook"}
	l.Notices = maps.Clone(l.Notices)
	l.Notices[builtin.NoticeDigestTitle] = "Morning"
	l.Notices[builtin.NoticeDigestNew] = "Fresh: {{.Count}}"
	l.Notices[builtin.NoticeDigestView] = "{{.View}} has {{.Count}}"
	r := newTasksRig(t, func(d *Deps) {
		d.Cfg.Notifications.DigestAt = "09:00"
		d.Intents = func() *intentload.Loaded { return &l }
	})
	end := time.Date(2026, 10, 8, 9, 0, 0, 0, time.Local)
	r.put(t,
		at(task.Task{ID: "e-some", State: "someday", Category: task.CategoryDeferred}, end.Add(-time.Hour)),
		at(task.Task{ID: "e-done", State: "done", Category: task.CategoryDone}, end.Add(-2*time.Hour)),
		at(task.Task{ID: "e-in", State: "inbox", Category: task.CategoryInbox}, end.Add(-time.Hour)),
		at(task.Task{ID: "e-old", State: "done", Category: task.CategoryDone}, end.Add(-30*time.Hour)),
	)
	var d api.Digest
	if code := get(t, r.s, api.DigestPath+"?day=2026-10-08", &d); code != 200 {
		t.Fatalf("%d", code)
	}
	if want := "Fresh: 2 · Someday has 1 · Logbook has 2"; d.Title != "Morning" || d.Body != want ||
		!slices.Equal(slices.Sorted(slices.Values(d.Tasks)), []string{"e-done", "e-some"}) {
		t.Fatalf("%+v, want title Morning and body %q", d, want)
	}
}
