package tui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func planOf(id, wf string, u unit.Unit, decision, reason string) api.PlanItem {
	it := api.PlanItem{ID: id, Workflow: wf, Unit: u, Decision: decision, Reason: reason, Mode: "apply", Steps: []string{"session.close"}}
	switch decision {
	case "pending":
		it.Actions = []api.ActionState{{ID: "plan_apply", Enabled: true, Primary: true}, {ID: "plan_dismiss", Enabled: true}}
	case "blocked":
		it.Actions = []api.ActionState{{ID: "plan_apply", Reason: reason}, {ID: "plan_dismiss", Enabled: true}}
	}
	return it
}

// planItems are a round whose window ends in 42 seconds, a session close
// that waits for the user, a blocked removal and an item that already ran.
func planItems() []api.PlanItem {
	round := planOf("review-follow-up/github/acme%2Fapi%237/aa", "review-follow-up", unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}, "pending", "new commits since your review")
	round.Default, round.Deadline, round.Steps = "run", now.Add(42*time.Second), []string{"turn.start"}
	round.Actions = append(round.Actions, api.ActionState{ID: "guide", Enabled: true})
	manual := planOf("session-manual-quiet/session/term_3/bb", "session-manual-quiet", unit.Unit{Provider: unit.Session, Key: "term_3"}, "pending", "session.idle_for = 30h0m0s")
	manual.Default, manual.Gain = "wait", api.Gain{MemoryBytes: 300 << 20}
	manual.Why.Matched = []api.Fact{{Name: "session.idle_for", Value: "30h0m0s"}}
	removal := planOf("workspace-temporary/workspace/wt-9/cc", "workspace-temporary", unit.Unit{Provider: unit.Workspace, Key: "wt-9"}, "blocked", "modified files")
	ran := planOf("session-duplicate/session/term_1/dd", "session-duplicate", unit.Unit{Provider: unit.Session, Key: "term_1"}, "run", "")
	return []api.PlanItem{removal, ran, manual, round}
}

func TestThePlanViewShowsTimeLeft(t *testing.T) {
	m := newModel(t)
	m.SetSpecs(servedSpecs(t).Specs)
	m.SetPlanSpecs(servedSpecs(t).Plan)
	m.SetData(oneRow(), api.Health{})
	m.SetPlan(planItems(), nil)
	press(m, Key{Name: KeyTab}, Key{Name: KeyTab}, Key{Name: KeyTab})
	screen := strings.Join(m.Render(100, 30, now), "\n")
	for _, want := range []string{"[Plan 3]", "42s  review-follow-up", "wait  session-manual-quiet", "blocked  workspace-temporary", "pending · runs in 42s", "keys", "Enter apply", "X dismiss", "G guide"} {
		if !strings.Contains(screen, want) {
			t.Errorf("missing %q in:\n%s", want, screen)
		}
	}
	if strings.Contains(screen, "session-duplicate") {
		t.Errorf("an item that ran is listed:\n%s", screen)
	}
	lines := strings.Split(screen, "\n")
	order := []int{}
	for _, wf := range []string{"review-follow-up", "session-manual-quiet", "workspace-temporary"} {
		order = append(order, slices.IndexFunc(lines, func(l string) bool { return strings.Contains(l, wf) }))
	}
	if !slices.IsSorted(order) {
		t.Errorf("pending items are not first:\n%s", screen)
	}

	m.Key(Key{Name: KeyDown, Read: 2})
	screen = strings.Join(m.Render(100, 30, now), "\n")
	for _, want := range []string{"pending · waits for you", "session.idle_for = 30h0m0s", "300.0 MiB of memory"} {
		if !strings.Contains(screen, want) {
			t.Errorf("missing %q in:\n%s", want, screen)
		}
	}
	if screen := strings.Join(m.Render(100, 30, now.Add(time.Minute)), "\n"); !strings.Contains(screen, "0s  review-follow-up") {
		t.Errorf("a window past its end does not count down to zero:\n%s", screen)
	}
}

func TestThePlanViewKeepsItsSelectionAcrossLoads(t *testing.T) {
	m := newModel(t)
	m.SetPlan(planItems(), nil)
	press(m, Key{Name: KeyTab}, Key{Name: KeyTab}, Key{Name: KeyTab})
	m.Key(Key{Name: KeyDown, Read: 2})
	if it, _ := m.selectedItem(); it.Workflow != "session-manual-quiet" {
		t.Fatalf("selected %s", it.Workflow)
	}
	m.SetPlan(planItems()[2:], nil)
	if it, _ := m.selectedItem(); it.Workflow != "session-manual-quiet" {
		t.Fatalf("selected %s after a load", it.Workflow)
	}
	m.SetPlan(nil, errFake("503 the daemon runs no workflows"))
	if screen := strings.Join(m.Render(100, 30, now), "\n"); !strings.Contains(screen, "plan not loaded: 503 the daemon runs no workflows") {
		t.Fatalf("%s", screen)
	}
}

type errFake string

func (e errFake) Error() string { return string(e) }

func TestThePlanViewAppliesAndDismisses(t *testing.T) {
	d := &fakeDaemon{rows: oneRow(), plan: planItems()}
	term, done := start(t, d)
	eventually(t, "the plan tab", func() bool { return strings.Contains(term.screen(), "Plan 3") })
	term.press(Key{Name: KeyTab}, Key{Name: KeyTab}, Key{Name: KeyTab}, Key{Name: KeyEnter})
	eventually(t, "the apply", func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return len(d.planActs) == 1
	})
	d.mu.Lock()
	first := d.planActs[0]
	d.mu.Unlock()
	if first.Action != "apply" || !slices.Equal(first.IDs, []string{"review-follow-up/github/acme%2Fapi%237/aa"}) {
		t.Fatalf("acted %+v", first)
	}
	eventually(t, "the status and the reload", func() bool {
		s := term.screen()
		return strings.Contains(s, "plan_apply review-follow-up acme/api#7: run") && strings.Contains(s, "Plan 2")
	})
	term.press(Key{Rune: 'X'})
	eventually(t, "the dismiss", func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return len(d.planActs) == 2 && d.planActs[1].Action == "dismiss" && d.planActs[1].IDs[0] == "session-manual-quiet/session/term_3/bb"
	})
	eventually(t, "one item left", func() bool { return strings.Contains(term.screen(), "Plan 1") })
	term.press(Key{Name: KeyEnter})
	eventually(t, "the blocked reason", func() bool { return strings.Contains(term.screen(), "modified files") })
	d.mu.Lock()
	acts := len(d.planActs)
	d.mu.Unlock()
	if acts != 2 {
		t.Fatalf("a blocked item was applied: %d actions", acts)
	}
	quit(t, term, done)
}

func TestThePlanViewNamesAnItemByItsRowsLabel(t *testing.T) {
	m := newModel(t)
	m.SetSpecs(servedSpecs(t).Specs)
	m.SetPlanSpecs(servedSpecs(t).Plan)
	m.SetData(oneRow(), api.Health{})
	thread := planOf("slack-follow-up/slack/T1%2FC1%2F171.1/ee", "slack-follow-up", unit.Unit{Provider: "slack", Key: "T1/C1/171.1"}, "pending", "")
	thread.Label, thread.Default = "#general · launch plan", "wait"
	m.SetPlan([]api.PlanItem{thread}, nil)
	press(m, Key{Name: KeyTab}, Key{Name: KeyTab}, Key{Name: KeyTab})
	screen := strings.Join(m.Render(100, 30, now), "\n")
	if strings.Count(screen, "#general · launch plan") != 2 || strings.Contains(screen, "T1/C1") {
		t.Fatalf("the item is not named by its label in the list and the detail:\n%s", screen)
	}
	if c, ok := press(m, Key{Rune: 'X'}); !ok || c.Label != "slack-follow-up #general · launch plan" {
		t.Fatalf("dismissed %+v", c)
	}
}
