package task

import (
	"slices"
	"testing"
	"time"
)

func TestReviewDue(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	for _, c := range []struct {
		name string
		p    Task
		want bool
	}{
		{"reviewed 6 days ago, default interval", Task{ReviewedAt: now.Add(-6 * day)}, false},
		{"reviewed exactly 7 days ago", Task{ReviewedAt: now.Add(-7 * day)}, true},
		{"reviewed 8 days ago", Task{ReviewedAt: now.Add(-8 * day)}, true},
		{"own interval of 14 days", Task{ReviewEvery: 14 * day, ReviewedAt: now.Add(-8 * day)}, false},
		{"own interval of 1 day", Task{ReviewEvery: day, ReviewedAt: now.Add(-day)}, true},
		{"never reviewed, created 2 days ago", Task{Created: now.Add(-2 * day)}, false},
		{"never reviewed, created 9 days ago", Task{Created: now.Add(-9 * day)}, true},
	} {
		c.p.Props, c.p.Category = Props{PropKind: {Text: KindProject}}, CategoryReady
		if got := c.p.ReviewDue(now); got != c.want {
			t.Errorf("%s: due %v, want %v", c.name, got, c.want)
		}
	}
	if (Task{}).ReviewInterval() != DefaultReviewEvery || DefaultReviewEvery != 7*day {
		t.Errorf("default interval %v", DefaultReviewEvery)
	}
}

func TestProjectsOf(t *testing.T) {
	seq := Task{ID: "p-seq", Props: Props{PropKind: {Text: KindProject}}, Category: CategoryReady, Sequential: true}
	par := Task{ID: "p-par", Props: Props{PropKind: {Text: KindProject}}, Category: CategoryReady}
	empty := Task{ID: "p-empty", Props: Props{PropKind: {Text: KindProject}}, Category: CategoryReady}
	all := []Task{seq, par, empty,
		{ID: "a1", Category: CategoryWaiting, Parent: "p-seq", Position: 1},
		{ID: "a2", Category: CategoryReady, Parent: "p-seq", Position: 2},
		{ID: "b1", Category: CategoryDeferred, Parent: "p-par", Position: 1},
		{ID: "b2", Category: CategoryReady, Parent: "p-par", Position: 3},
		{ID: "b3", Category: CategoryReady, Title: "Draft the plan", Parent: "p-par", Position: 2},
		{ID: "b4", Category: CategoryDone, Parent: "p-par", Position: 0},
		{ID: "c1", Category: CategoryDone, Parent: "p-empty", Position: 1},
	}
	got := ProjectsOf(all)
	want := map[ID]ProjectStatus{"p-seq": {Next: "", Open: 2}, "p-par": {Next: "b3", NextTitle: "Draft the plan", Open: 3}, "p-empty": {}}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: %+v, want %+v", id, got[id], w)
		}
	}
}

func TestPlace(t *testing.T) {
	acts := []Task{{ID: "a", Position: 1}, {ID: "b", Position: 2}, {ID: "c", Position: 3}}
	pos := func(es []Task) []string {
		var out []string
		for _, e := range es {
			out = append(out, string(e.ID)+":"+string(rune('0'+e.Position)))
		}
		return out
	}
	got := pos(Place(acts, Task{ID: "x", Position: 9}, 2))
	if want := []string{"a:1", "x:2", "b:3", "c:4"}; !slices.Equal(got, want) {
		t.Errorf("insert at 2: %v, want %v", got, want)
	}
	got = pos(Place(acts, Task{ID: "x"}, 99))
	if want := []string{"a:1", "b:2", "c:3", "x:4"}; !slices.Equal(got, want) {
		t.Errorf("insert past the end: %v, want %v", got, want)
	}
	got = pos(Place(acts[:2], acts[2], 1))
	if want := []string{"c:1", "a:2", "b:3"}; !slices.Equal(got, want) {
		t.Errorf("move to the front: %v, want %v", got, want)
	}
}
