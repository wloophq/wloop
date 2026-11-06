package tui

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/wloophq/wloop/internal/api"
	dtask "github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

var pickVerbs = []api.VerbSpec{
	{Verb: "merge", Handler: "merge", Label: "Merge", Summary: "Fold another task into this one", Input: "Merge with which task", Pick: builtin.PickOpen},
	{Verb: "split", Handler: "split", Label: "Split", Input: "Split which task out", Pick: builtin.PickMerged},
	{Verb: "relate", Handler: "relate", Label: "Relate", Input: "Relate to which task", Pick: builtin.PickOpen},
	{Verb: "unrelate", Handler: "unrelate", Label: "Unrelate", Input: "Unrelate which task", Pick: builtin.PickRelated},
}

// pickModel is viewsModel whose catalog serves the verbs that pick another task, every task offering them,
// with e1 edited by edit.
func pickModel(t *testing.T, edit func(*api.TaskView)) (*Model, map[string][]api.TaskView) {
	t.Helper()
	m := viewsModel(t)
	cat := viewsCatalog()
	cat.Verbs = append(cat.Verbs, pickVerbs...)
	cat.Props = append(cat.Props, api.PropSpec{Name: "related", Type: "string[]", Role: string(dtask.RoleRelated)},
		api.PropSpec{Name: "merged", Type: "string[]", Role: string(dtask.RoleMerged)})
	cat.States = []api.StateSpec{{Name: "inbox", Label: "Inbox"}, {Name: "next", Label: "Next"}, {Name: "waiting", Label: "Waiting"}}
	m.SetViews(cat)
	tasks := viewTasks()
	for v, es := range tasks {
		for i := range es {
			for _, p := range pickVerbs {
				es[i].Verbs = append(es[i].Verbs, api.VerbState{Verb: p.Verb, Enabled: true})
			}
			if es[i].ID == "e1" && edit != nil {
				edit(&es[i])
			}
		}
		m.SetTasks(v, es, viewCounters())
	}
	return m, tasks
}

func paletteLabels(m *Model) []string {
	var out []string
	for _, it := range m.overlay.items {
		out = append(out, it.label)
	}
	return out
}

// runPalette opens the palette and runs its item label.
func runPalette(t *testing.T, m *Model, label string) (Command, bool) {
	t.Helper()
	press(m, Key{Name: KeyCtrlK})
	i := slices.Index(paletteLabels(m), label)
	if i < 0 {
		t.Fatalf("the palette lacks %q: %v", label, paletteLabels(m))
	}
	m.overlay.at = i
	return press(m, Key{Name: KeyEnter})
}

func TestThePicksMirrorTheBuiltinTable(t *testing.T) {
	if pickOpen != builtin.PickOpen || pickMerged != builtin.PickMerged || pickRelated != builtin.PickRelated {
		t.Fatal("the TUI's picks drifted from builtin's")
	}
}

func TestTheTUIPicksTheOtherTaskOfAMergeFromTheOpenTasks(t *testing.T) {
	m, tasks := pickModel(t, nil)
	press(m, Key{Name: KeyCtrlK})
	if got := paletteLabels(m); !slices.Contains(got, "Merge") || !slices.Contains(got, "Relate") || slices.Contains(got, "Split") || slices.Contains(got, "Unrelate") {
		t.Fatalf("the palette of a task with nothing merged or related: %v", got)
	}
	press(m, Key{Name: KeyEsc})

	d := &fakeDaemon{tasks: tasks}
	answer := func(c Command, ok bool) {
		t.Helper()
		if !ok || !c.matching {
			t.Fatalf("the picker asked nothing of the daemon: %+v %v", c, ok)
		}
		got, err := carry(context.Background(), Config{Daemon: d}, c)
		m.SetMatches(c.match, got.matched, err)
	}
	answer(runPalette(t, m, "Merge"))
	if m.overlay == nil || m.overlay.pick == nil || !strings.HasPrefix(m.overlay.title, "Merge with which task") {
		t.Fatalf("Merge opened %+v", m.overlay)
	}
	if got := paletteLabels(m); len(got) != 11 || slices.Contains(got, "Reply on the batch export review") {
		t.Fatalf("the picker opens on %v: every open task but e1", got)
	}
	var stale Command
	for i, k := range typed("Wri") {
		c, ok := press(m, k)
		if i == 0 {
			stale = c
		}
		answer(c, ok)
	}
	got, _ := carry(context.Background(), Config{Daemon: d}, stale)
	m.SetMatches(stale.match, got.matched, nil)
	if got := paletteLabels(m); !reflect.DeepEqual(got, []string{"Write the runbook"}) || m.overlay.items[0].key != "Next" {
		t.Fatalf("typing Wri, then a late answer for W: %v", got)
	}
	if !reflect.DeepEqual(d.matches, []string{"", "W", "Wr", "Wri", "W"}) {
		t.Errorf("the daemon was asked %q", d.matches)
	}
	c, ok := press(m, Key{Name: KeyEnter})
	if !ok || c.task != "e1" || c.verb != "merge" || !reflect.DeepEqual(c.args, map[string]string{dtask.ArgTask: "e7"}) || m.overlay != nil {
		t.Fatalf("Enter sent %+v %v", c, ok)
	}
	if _, err := carry(context.Background(), Config{Daemon: d}, c); err != nil {
		t.Fatal(err)
	}
	if want := []taskDo{{"e1", "merge", "tui", map[string]string{dtask.ArgTask: "e7"}}}; !reflect.DeepEqual(d.dos(), want) {
		t.Fatalf("sent %+v", d.dos())
	}
	m.Done(c, nil, nil)

	answer(runPalette(t, m, "Relate"))
	if _, ok := press(m, Key{Name: KeyEsc}); ok || m.overlay != nil {
		t.Fatal("Esc did not close the picker")
	}
}

func TestTheTUIDetailShowsRelatedTasksAndAMergedFromTraceWithSplit(t *testing.T) {
	m, _ := pickModel(t, func(e *api.TaskView) {
		e.Props["related"] = json.RawMessage(`["e4","t-40"]`)
		e.Props["merged"] = json.RawMessage(`["t-9"]`)
		e.Related = []api.TaskRef{{ID: "e4", Title: "Call the bank first", State: "inbox"}, {ID: "t-40", Title: "Old badge order", State: "next", Merged: true}}
		e.MergedFrom = []api.TaskRef{{ID: "t-9", Title: "Batch export follow-up", State: "inbox", Within: []api.TaskRef{{ID: "t-8", Title: "Export retry", State: "waiting"}}}}
	})
	e, _ := m.selectedTask()
	m.openTask(e, 0)
	m.SetDetail("e1", e, nil)
	screen := screenOf(m, 140, 60)
	for _, want := range []string{"Call the bank · Next", "Old badge order · Next", "merged     Batch export follow-up · Inbox", "↳ Export retry · Waiting"} {
		if !strings.Contains(screen, want) {
			t.Errorf("the detail lacks %q:\n%s", want, screen)
		}
	}
	if strings.Contains(screen, "t-40") || strings.Contains(screen, "t-9") || strings.Contains(screen, "Call the bank first") {
		t.Errorf("the detail lists the property's ids, or a stale title:\n%s", screen)
	}

	press(m, Key{Name: KeyCtrlK})
	if got := paletteLabels(m); !slices.Contains(got, "Split") || !slices.Contains(got, "Unrelate") {
		t.Fatalf("the palette lacks Split or Unrelate: %v", got)
	}
	press(m, Key{Name: KeyEsc})
	if c, ok := runPalette(t, m, "Split"); ok {
		t.Fatalf("Split asked the daemon for the open tasks: %+v", c)
	}
	if got := paletteLabels(m); !reflect.DeepEqual(got, []string{"Batch export follow-up"}) {
		t.Fatalf("Split picks from %v, want the top of the trace alone", got)
	}
	press(m, typed("zz")...)
	if len(m.overlay.items) != 0 {
		t.Fatalf("zz kept %v", paletteLabels(m))
	}
	press(m, Key{Name: KeyBackspace}, Key{Name: KeyBackspace})
	c, ok := press(m, Key{Name: KeyEnter})
	if !ok || c.task != "e1" || c.verb != "split" || !reflect.DeepEqual(c.args, map[string]string{dtask.ArgTask: "t-9"}) {
		t.Fatalf("Split sent %+v %v", c, ok)
	}
	m.Done(c, nil, nil)
	runPalette(t, m, "Unrelate")
	if got := paletteLabels(m); !reflect.DeepEqual(got, []string{"Call the bank", "Old badge order"}) {
		t.Fatalf("Unrelate picks from %v", got)
	}
	press(m, typed("old")...)
	if c, ok := press(m, Key{Name: KeyEnter}); !ok || c.verb != "unrelate" || c.args[dtask.ArgTask] != "t-40" {
		t.Fatalf("Unrelate sent %+v %v", c, ok)
	}
}

func TestRunListsTheDaemonsMatchesWhileTheTextIsTypedAndReadsNoPage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, tasks := pickModel(t, nil)
		press(m, Key{Name: KeyCtrlK})
		at := slices.Index(paletteLabels(m), "Merge")
		cat := m.viewsCat
		d := &fakeDaemon{rows: oneRow(), viewsCat: &cat, tasks: tasks, counters: viewCounters()}
		term, done := start(t, d)
		eventually(t, "Focus", func() bool { return strings.Contains(term.screen(), "Reply on the batch export review") })
		synctest.Wait()
		d.mu.Lock()
		pages := len(d.taskAsks)
		d.mu.Unlock()
		term.press(Key{Name: KeyCtrlK})
		for range at {
			term.press(Key{Name: KeyDown})
		}
		term.press(Key{Name: KeyEnter})
		term.press(typed("Wri")...)
		eventually(t, "the match", func() bool {
			s := term.screen()
			return strings.Contains(s, "> Wri") && strings.Contains(s, "Write the runbook") && !strings.Contains(s, "Call the bank")
		})
		d.mu.Lock()
		after := len(d.taskAsks)
		d.mu.Unlock()
		if after != pages {
			t.Errorf("typing in the picker read %d pages of tasks", after-pages)
		}
		term.press(Key{Name: KeyEnter})
		eventually(t, "the merge", func() bool {
			return reflect.DeepEqual(d.dos(), []taskDo{{"e1", "merge", "tui", map[string]string{dtask.ArgTask: "e7"}}})
		})
		quit(t, term, done)
	})
}
