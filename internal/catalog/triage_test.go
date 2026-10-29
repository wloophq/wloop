package catalog

import (
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
)

// inView says whether the bundled view id lists a task whose facts are f.
func inView(t *testing.T, l *intentload.Loaded, id string, f workflow.Facts) bool {
	t.Helper()
	in, err := ViewAt(l.Views, id, task.Task{}, f, lensNow)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func viewDef(t *testing.T, l *intentload.Loaded, id string) intentload.ViewDef {
	t.Helper()
	for _, v := range l.Views.Views {
		if v.ID == id {
			return v
		}
	}
	t.Fatalf("no bundled view %s", id)
	return intentload.ViewDef{}
}

func TestChaseListsStaleDelegations(t *testing.T) {
	l := bundled(t)
	ana := workflow.Facts{"task.prop.owner": "slack:U0ANA"}
	with := func(f workflow.Facts, more workflow.Facts) workflow.Facts {
		out := workflow.Facts{}
		for k, v := range f {
			out[k] = v
		}
		for k, v := range more {
			out[k] = v
		}
		return out
	}
	passed := lensNow.Add(-48 * time.Hour)
	for name, c := range map[string]struct {
		c    task.Category
		f    workflow.Facts
		want bool
	}{
		"Ana's, its check date passed with nothing moving": {task.CategoryWaiting, with(ana, workflow.Facts{"task.check_on": passed, "task.since_movement": 72 * time.Hour}), true},
		"Ana's, moved since its check date":                {task.CategoryWaiting, with(ana, workflow.Facts{"task.check_on": passed, "task.since_movement": time.Hour}), false},
		"Ana's, with no check date":                        {task.CategoryWaiting, with(ana, workflow.Facts{"task.since_movement": 72 * time.Hour}), false},
		"Ana's, its check date still ahead":                {task.CategoryWaiting, with(ana, workflow.Facts{"task.check_on": lensNow.Add(time.Hour), "task.since_movement": 72 * time.Hour}), false},
		"mine, past its check date":                        {task.CategoryReady, workflow.Facts{"task.check_on": passed, "task.since_movement": 72 * time.Hour}, false},
		"Ana's, snoozed":                                   {task.CategoryDeferred, with(ana, workflow.Facts{"task.check_on": passed, "task.since_movement": 72 * time.Hour}), false},
	} {
		if got := inView(t, l, "chase", lensFacts(t, l, c.c, c.f)); got != c.want {
			t.Errorf("%s: in chase %v, want %v", name, got, c.want)
		}
	}
	if g := viewDef(t, l, "chase").Group; g != "task.owed_by" {
		t.Errorf("chase groups by %q, want by who owes the next step", g)
	}
}

func TestFitsNowFiltersByAttentionSize(t *testing.T) {
	l := bundled(t)
	for name, c := range map[string]struct {
		c    task.Category
		f    workflow.Facts
		want bool
	}{
		"mine, 20 minutes":         {task.CategoryReady, workflow.Facts{"task.prop.size": 20 * time.Minute}, true},
		"mine, half an hour":       {task.CategoryReady, workflow.Facts{"task.prop.size": 30 * time.Minute}, true},
		"mine, 45 minutes":         {task.CategoryReady, workflow.Facts{"task.prop.size": 45 * time.Minute}, false},
		"mine, of no size":         {task.CategoryReady, workflow.Facts{"task.prop.size": time.Duration(0)}, false},
		"Ana's, 20 minutes":        {task.CategoryWaiting, workflow.Facts{"task.prop.size": 20 * time.Minute, "task.prop.owner": "slack:U0ANA"}, false},
		"mine, not clarified yet":  {task.CategoryInbox, workflow.Facts{"task.prop.size": 20 * time.Minute}, false},
		"mine, done in 20 minutes": {task.CategoryDone, workflow.Facts{"task.prop.size": 20 * time.Minute}, false},
	} {
		if got := inView(t, l, "fits-now", lensFacts(t, l, c.c, c.f)); got != c.want {
			t.Errorf("%s: in fits-now %v, want %v", name, got, c.want)
		}
	}
}

func TestBlockedByMeGroupsByWhoAsked(t *testing.T) {
	l := bundled(t)
	for name, c := range map[string]struct {
		c    task.Category
		f    workflow.Facts
		want bool
	}{
		"Bruno asked me":           {task.CategoryReady, workflow.Facts{"task.link_owes": []string{"me"}, "task.askers": []string{"slack:U0BRUNO"}}, true},
		"mine, nobody asked":       {task.CategoryReady, workflow.Facts{"task.askers": []string{}}, false},
		"Bruno asked, Ana owes it": {task.CategoryWaiting, workflow.Facts{"task.link_owes": []string{"slack:U0ANA"}, "task.askers": []string{"slack:U0BRUNO"}}, false},
		"Bruno asked, snoozed":     {task.CategoryDeferred, workflow.Facts{"task.link_owes": []string{"me"}, "task.askers": []string{"slack:U0BRUNO"}}, false},
	} {
		if got := inView(t, l, "blocked-by-me", lensFacts(t, l, c.c, c.f)); got != c.want {
			t.Errorf("%s: in blocked-by-me %v, want %v", name, got, c.want)
		}
	}
	if g := viewDef(t, l, "blocked-by-me").Group; g != "task.askers" {
		t.Errorf("blocked-by-me groups by %q, want by who asked", g)
	}
}
