package tui

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/wloophq/wloop/internal/api"
)

func (f *fakeDaemon) Review(context.Context) (api.Review, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reviewReads++
	if f.review == nil {
		return api.Review{}, errors.New("the review is not served")
	}
	return cloneReview(*f.review), nil
}

func (f *fakeDaemon) ReviewStepDone(_ context.Context, step string) (api.Review, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stepsDone = append(f.stepsDone, step)
	f.review.Done = true
	for i := range f.review.Steps {
		if f.review.Steps[i].ID == step {
			f.review.Steps[i].Done = true
		}
		f.review.Done = f.review.Done && f.review.Steps[i].Done
	}
	return cloneReview(*f.review), nil
}

func (f *fakeDaemon) doneSteps() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.stepsDone)
}

func cloneReview(r api.Review) api.Review {
	r.Steps = slices.Clone(r.Steps)
	return r
}

func reviewStep(id, label, view string, count int, done bool) api.ReviewStep {
	return api.ReviewStep{ID: id, Label: label, View: view, Query: map[string]string{"view": view}, Count: count, Done: done}
}

// reviewFixture is a Review with the Inbox, Waiting and projects steps open.
func reviewFixture() api.Review {
	return api.Review{ID: "r-1", StartedAt: now.Add(-time.Hour), Steps: []api.ReviewStep{
		reviewStep("inbox", "Inbox to zero", "inbox", 2, false),
		reviewStep("outbox", "Outbox", "outbox", 1, true),
		reviewStep("waiting", "Waiting: nudge?", "waiting", 4, false),
		reviewStep("projects", "Projects without a next action or past their review", "review", 1, false),
		reviewStep("someday", "Someday", "someday", 0, true),
		reviewStep("focus", "Prune Focus", "focus", 1, true),
	}}
}

func reviewCatalog() api.ViewsCatalog {
	c := viewsCatalog()
	c.Views = append(c.Views, api.TaskViewSpec{ID: "review", Label: "Review"})
	return c
}

func reviewTasks() map[string][]api.TaskView {
	es := viewTasks()
	p := task("e-p", "Ship the v2 API", "next")
	p.Props, p.ReviewDue, p.ReviewEvery = map[string]json.RawMessage{"kind": json.RawMessage(`"project"`)}, true, "168h0m0s"
	es["review"] = []api.TaskView{p}
	return es
}

// reviewModel is the golden model on the Review, its Inbox step shown.
func reviewModel(t testing.TB, color bool) *Model {
	t.Helper()
	m := newModel(t)
	m.SetColor(color)
	m.SetViews(reviewCatalog())
	for v, es := range reviewTasks() {
		m.SetTasks(v, es, viewCounters())
	}
	m.SetReview(reviewFixture())
	m.navigate("review")
	return m
}

func TestReviewKeysTUI(t *testing.T) {
	m := newModel(t)
	m.SetViews(reviewCatalog())
	for v, es := range reviewTasks() {
		m.SetTasks(v, es, viewCounters())
	}
	chord(m, "gr")
	if !m.inReview() {
		t.Fatalf("g r: view %d", m.view)
	}
	m.SetReview(reviewFixture())
	if view, area, ok := m.TaskQuery(); view != "inbox" || area != "" || !ok || m.rv.step != "inbox" || !m.rv.head {
		t.Fatalf("the review opened on %q (query %q %q %v, head %v), want the first open step", m.rv.step, view, area, ok, m.rv.head)
	}
	if _, ok := m.selectedTask(); ok {
		t.Fatal("an entry is selected while the cursor is on the step's header")
	}

	c, ok := press(m, Key{Name: KeyEnter})
	if !ok || c.reviewStep != "inbox" || c.fetch {
		t.Fatalf("Enter on the header: %+v %v", c, ok)
	}
	if m.rv.step != "waiting" || !m.rv.head {
		t.Fatalf("after done the review shows %q, want the next open step", m.rv.step)
	}
	m.Done(c, nil, nil)

	press(m, Key{Rune: ']'})
	if m.rv.step != "projects" {
		t.Fatalf("] moved to %q", m.rv.step)
	}
	press(m, Key{Rune: '['}, Key{Rune: '['})
	if m.rv.step != "outbox" {
		t.Fatalf("[ [ moved to %q", m.rv.step)
	}
	press(m, Key{Rune: ']'})

	press(m, Key{Rune: 'j'})
	e, ok := m.selectedTask()
	if !ok || e.ID != "e9" || m.rv.head {
		t.Fatalf("j from the header selected %+v %v", e, ok)
	}
	c, ok = press(m, Key{Rune: 'x'})
	if !ok || c.verb != "next" || c.task != "e9" {
		t.Fatalf("x on the step's entry: %+v %v", c, ok)
	}
	m.Done(c, nil, nil)
	c, ok = press(m, Key{Name: KeyEnter})
	if !ok || !c.fetch || c.task != "e9" || c.reviewStep != "" {
		t.Fatalf("Enter on an entry opens it, got %+v %v", c, ok)
	}
	press(m, Key{Rune: 'k'})
	if !m.rv.head {
		t.Fatal("k from the first entry did not return to the header")
	}
	if c, ok := press(m, Key{Rune: ']'}); ok || m.rv.step != "projects" {
		t.Fatalf("] sent %+v or showed %q", c, m.rv.step)
	}
	if view, _, _ := m.TaskQuery(); view != "review" {
		t.Errorf("the projects step queries %q", view)
	}

	chord(m, "gi")
	if c, ok := press(m, Key{Rune: ']'}); ok || c.reviewStep != "" {
		t.Errorf("] outside the review: %+v", c)
	}
	press(m, Key{Name: KeyEnter})
	if m.detailID == "" {
		t.Error("Enter in the Inbox no longer opens the detail")
	}
}

func TestReviewThroughTheDaemon(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rv := reviewFixture()
		d := &fakeDaemon{rows: oneRow(), viewsCat: ptr(reviewCatalog()), tasks: reviewTasks(), counters: viewCounters(), review: &rv}
		term, done := start(t, d)
		eventually(t, "Focus", func() bool { return strings.Contains(term.screen(), "[Focus 1]") })
		term.press(Key{Rune: 'g'}, Key{Rune: 'r'})
		eventually(t, "the Inbox step", func() bool {
			s := term.screen()
			return strings.Contains(s, "Inbox to zero (2)") && strings.Contains(s, "Buy cables")
		})
		term.press(Key{Name: KeyEnter})
		eventually(t, "the step done and the Waiting step", func() bool {
			s := term.screen()
			return slices.Equal(d.doneSteps(), []string{"inbox"}) && strings.Contains(s, "Waiting: nudge? (4)") && strings.Contains(s, "Contract signature")
		})
		term.press(Key{Name: KeyEnter}, Key{Name: KeyEnter})
		eventually(t, "the review done", func() bool {
			steps := d.doneSteps()
			slices.Sort(steps)
			return slices.Equal(steps, []string{"inbox", "projects", "waiting"}) && strings.Contains(term.screen(), "Review done.")
		})
		quit(t, term, done)
	})
}

func TestProjectsShowTheirReview(t *testing.T) {
	m := reviewModel(t, false)
	press(m, Key{Rune: ']'}, Key{Rune: ']'}, Key{Rune: ']'})
	if m.rv.step != "projects" {
		t.Fatalf("step %q", m.rv.step)
	}
	screen := screenOf(m, 120, 40)
	if !strings.Contains(screen, "Ship the v2 API") || !strings.Contains(screen, "review due no next action 0 open") {
		t.Fatalf("the projects step:\n%s", screen)
	}
}

func TestPaletteRunsTheProjectVerbs(t *testing.T) {
	m := newModel(t)
	m.SetViews(reviewCatalog())
	p := task("e-p", "Ship the v2 API", "next")
	p.Props = map[string]json.RawMessage{"kind": json.RawMessage(`"project"`)}
	p.Verbs = append(verbs("done"), api.VerbState{Verb: "reviewed", Enabled: true}, api.VerbState{Verb: "sequential", Enabled: true})
	m.SetTasks("projects", []api.TaskView{p}, viewCounters())
	m.navigate("projects")
	var labels []string
	var run func(uint64) (Command, bool)
	for _, it := range m.paletteItems() {
		labels = append(labels, it.label)
		if it.label == "reviewed" {
			run = it.run
		}
	}
	if !slices.Contains(labels, "sequential") || run == nil {
		t.Fatalf("palette %v", labels)
	}
	if c, ok := run(0); !ok || c.verb != "reviewed" || c.task != "e-p" {
		t.Errorf("reviewed from the palette: %+v %v", c, ok)
	}
}

func TestProjectFactsCountTheOpenChildren(t *testing.T) {
	p := task("e-p", "Ship", "next")
	p.Props, p.Children = map[string]json.RawMessage{"kind": json.RawMessage(`"project"`)}, []string{"e-a", "e-b"}
	if got := strings.Join(projectFacts(p), " "); got != "2 open" {
		t.Errorf("facts %q", got)
	}
}
