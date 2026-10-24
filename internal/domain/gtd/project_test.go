package gtd

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
		p    Entry
		want bool
	}{
		{"reviewed 6 days ago, default interval", Entry{ReviewedAt: now.Add(-6 * day)}, false},
		{"reviewed exactly 7 days ago", Entry{ReviewedAt: now.Add(-7 * day)}, true},
		{"reviewed 8 days ago", Entry{ReviewedAt: now.Add(-8 * day)}, true},
		{"own interval of 14 days", Entry{ReviewEvery: 14 * day, ReviewedAt: now.Add(-8 * day)}, false},
		{"own interval of 1 day", Entry{ReviewEvery: day, ReviewedAt: now.Add(-day)}, true},
		{"never reviewed, created 2 days ago", Entry{Created: now.Add(-2 * day)}, false},
		{"never reviewed, created 9 days ago", Entry{Created: now.Add(-9 * day)}, true},
	} {
		c.p.Kind, c.p.State = KindProject, Next
		if got := c.p.ReviewDue(now); got != c.want {
			t.Errorf("%s: due %v, want %v", c.name, got, c.want)
		}
	}
	if (Entry{}).ReviewInterval() != DefaultReviewEvery || DefaultReviewEvery != 7*day {
		t.Errorf("default interval %v", DefaultReviewEvery)
	}
}

func TestProjectsOf(t *testing.T) {
	seq := Entry{ID: "p-seq", Kind: KindProject, State: Next, Sequential: true}
	par := Entry{ID: "p-par", Kind: KindProject, State: Next}
	empty := Entry{ID: "p-empty", Kind: KindProject, State: Next}
	all := []Entry{seq, par, empty,
		{ID: "a1", State: Waiting, Project: "p-seq", Position: 1},
		{ID: "a2", State: Next, Project: "p-seq", Position: 2},
		{ID: "b1", State: Someday, Project: "p-par", Position: 1},
		{ID: "b2", State: Next, Project: "p-par", Position: 3},
		{ID: "b3", State: Next, Title: "Draft the plan", Project: "p-par", Position: 2},
		{ID: "b4", State: Done, Project: "p-par", Position: 0},
		{ID: "c1", State: Done, Project: "p-empty", Position: 1},
	}
	got := ProjectsOf(all)
	want := map[ID]ProjectStatus{"p-seq": {Next: "", Open: 2}, "p-par": {Next: "b3", NextTitle: "Draft the plan", Open: 3}, "p-empty": {}}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: %+v, want %+v", id, got[id], w)
		}
	}
	now := time.Now()
	if !NeedsReview(seq, got["p-seq"], now) || NeedsReview(Entry{ID: "x", Kind: KindProject, State: Next, Created: now}, ProjectStatus{Next: "a", Open: 1}, now) {
		t.Error("a project without a next action needs review; one with a next action and not due does not")
	}
	if NeedsReview(Entry{Kind: KindProject, State: Someday, Created: now}, ProjectStatus{}, now) {
		t.Error("a Someday project needs no next action")
	}
	if NeedsReview(Entry{Kind: KindProject, State: Done}, ProjectStatus{}, now) {
		t.Error("a done project is not reviewed")
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"Ship the v2 API":             "ship-the-v2-api",
		"  Migração do  banco!! ":     "migracao-do-banco",
		"Ação":                        "acao",
		"Ac\u0327a\u0303o decomposed": "acao-decomposed",
		"../etc/passwd":               "etc-passwd",
		"":                            "project",
		"!!!":                         "project",
		"A very long project title that keeps going and going past the cap": "a-very-long-project-title-that-keeps-going-and-g",
	} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNumberedSlugStaysWithinTheCap(t *testing.T) {
	long := Slug("A very long project title that keeps going and going past the cap")
	for n, want := range map[int]string{1: long, 2: "a-very-long-project-title-that-keeps-going-and-2", 12: "a-very-long-project-title-that-keeps-going-an-12"} {
		got := NumberedSlug(long, n)
		if got != want || len(got) > 48 {
			t.Errorf("NumberedSlug(%d) = %q (%d), want %q", n, got, len(got), want)
		}
	}
	if got := NumberedSlug("ship-it", 3); got != "ship-it-3" {
		t.Errorf("short slug: %q", got)
	}
}

func TestPlace(t *testing.T) {
	acts := []Entry{{ID: "a", Position: 1}, {ID: "b", Position: 2}, {ID: "c", Position: 3}}
	pos := func(es []Entry) []string {
		var out []string
		for _, e := range es {
			out = append(out, string(e.ID)+":"+string(rune('0'+e.Position)))
		}
		return out
	}
	got := pos(Place(acts, Entry{ID: "x", Position: 9}, 2))
	if want := []string{"a:1", "x:2", "b:3", "c:4"}; !slices.Equal(got, want) {
		t.Errorf("insert at 2: %v, want %v", got, want)
	}
	got = pos(Place(acts, Entry{ID: "x"}, 99))
	if want := []string{"a:1", "b:2", "c:3", "x:4"}; !slices.Equal(got, want) {
		t.Errorf("insert past the end: %v, want %v", got, want)
	}
	got = pos(Place(acts[:2], acts[2], 1))
	if want := []string{"c:1", "a:2", "b:3"}; !slices.Equal(got, want) {
		t.Errorf("move to the front: %v, want %v", got, want)
	}
}
