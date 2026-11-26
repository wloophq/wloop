package catalog

import (
	"slices"
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

func TestAnAskThatWaitsOnMeIsANextActionGroupedByWhoAsked(t *testing.T) {
	l := bundled(t)
	for name, c := range map[string]struct {
		c    task.Category
		f    workflow.Facts
		want bool
	}{
		"Bruno asked me":           {task.CategoryReady, workflow.Facts{"task.link_owes": []string{"me"}, "task.askers": []string{"slack:U0BRUNO"}}, true},
		"Bruno asked, Ana owes it": {task.CategoryWaiting, workflow.Facts{"task.link_owes": []string{"slack:U0ANA"}, "task.askers": []string{"slack:U0BRUNO"}}, false},
		"Bruno asked, snoozed":     {task.CategoryDeferred, workflow.Facts{"task.link_owes": []string{"me"}, "task.askers": []string{"slack:U0BRUNO"}}, false},
		"Bruno asked, a gate open": {task.CategoryReady, workflow.Facts{"task.link_owes": []string{"me"}, "task.askers": []string{"slack:U0BRUNO"}, "task.gates_open": 1, "task.decisions": []string{"answer"}}, false},
	} {
		if got := inView(t, l, "next", lensFacts(t, l, c.c, c.f)); got != c.want {
			t.Errorf("%s: in next %v, want %v", name, got, c.want)
		}
	}
	i := slices.IndexFunc(l.Views.Facets, func(f intentload.FacetDef) bool { return f.ID == "asked-by" })
	if i < 0 || !l.Views.Facets[i].Group || l.Views.Facets[i].Fact != "task.asker_names" {
		t.Errorf("no asked-by facet to group Next actions by who asked: %+v", l.Views.Facets)
	}
}

// A task of yours with a decision and no open gate, a failed delivery or a source's situation, waits in Needs you
// alone, as a gated one does: Next actions lists only what waits on no decision.
func TestADecisionWithoutAGateKeepsATaskOutOfNextActions(t *testing.T) {
	l := bundled(t)
	for name, decisions := range map[string][]string{
		"a failed delivery":         {"send-draft"},
		"an approved pull request":  {"last-step"},
		"a thread waiting on you":   {"answer"},
		"nothing waits on a choice": {},
	} {
		f := lensFacts(t, l, task.CategoryReady, workflow.Facts{"task.link_owes": []string{"me"}, "task.decisions": decisions})
		next, needs := inView(t, l, "next", f), inView(t, l, "needs-you", f)
		if want := len(decisions) == 0; next != want || needs == want {
			t.Errorf("%s: in next %v, in needs-you %v; want exactly one, next only when no decision waits", name, next, needs)
		}
	}
}
