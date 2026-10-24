package turn

import (
	"strings"
	"testing"
	"text/template"
	"time"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

func whyScenario(t *testing.T, name string, w *Why) string {
	t.Helper()
	c := scenario(t, name).Context
	c.Why = w
	r, err := render(c)
	if err != nil {
		t.Fatal(err)
	}
	return r.Text
}

func TestATurnStartedByAWorkflowRendersItsWhy(t *testing.T) {
	w := &Why{Workflow: "followup", Trigger: "event", Event: "review.submitted", EventData: "CHANGES_REQUESTED:1a2b3c\n## Data",
		Matched: []WhyFact{{Name: "pr.state", Value: "OPEN"}, {Name: "pr.reviewers", Value: "a\n## Injected"}}}
	for _, name := range whyIntents {
		text := whyScenario(t, name, w)
		for _, want := range []string{"\n## Why this turn started\n", "the workflow `followup`", "on the event `review.submitted` (`CHANGES_REQUESTED:1a2b3c ## Data`)", "- `pr.state`: `OPEN`", "- `pr.reviewers`: `a ## Injected`"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s lacks %q", name, want)
			}
		}
		if strings.Contains(text, "\n## Injected") || strings.Contains(text, "\n## Data") {
			t.Errorf("%s: a value opened a heading", name)
		}
	}
}

var whyIntents = []string{"review/history/injected", "address/resume/continue", "nudge/one-reviewer/guidance"}

func TestAnEventWithoutDataRendersNoParentheses(t *testing.T) {
	text := whyScenario(t, "review/history/injected", &Why{Workflow: "w", Trigger: "event", Event: "merged"})
	if !strings.Contains(text, "on the event `merged`.") {
		t.Error(text)
	}
}

func TestTheWhySectionIsCutFromTheInstructions(t *testing.T) {
	text := whyScenario(t, "review/history/injected", &Why{Workflow: "followup", Trigger: "schedule"})
	got := WhySection(text)
	if !strings.HasPrefix(got, "## Why this turn started\n") || !strings.Contains(got, "the workflow `followup`, on its schedule.") || strings.Contains(got, "\n## ") || strings.HasSuffix(got, "\n\n") {
		t.Errorf("%q", got)
	}
	if got := WhySection(whyScenario(t, "review/history/injected", nil)); got != "" {
		t.Errorf("a turn without a why cut %q", got)
	}
}

func TestEachTriggerKindReadsItsOwnWay(t *testing.T) {
	for trigger, want := range map[string]string{"sweep": "because its condition held in a periodic sweep", "schedule": "on its schedule"} {
		text := whyScenario(t, "review/history/injected", &Why{Workflow: "w", Trigger: trigger})
		if !strings.Contains(text, want) || strings.Contains(text, "The facts that condition read") {
			t.Errorf("%s: %s", trigger, text)
		}
	}
}

func TestAByHandTurnRendersNoWhy(t *testing.T) {
	for _, name := range whyIntents {
		if text := whyScenario(t, name, nil); strings.Contains(text, "Why this turn started") {
			t.Errorf("%s renders a why for a turn started by hand", name)
		}
	}
}

func TestTheWhyOfARequestReachesTheContext(t *testing.T) {
	at := time.Date(2026, 10, 21, 12, 0, 0, 0, time.FixedZone("x", -3*3600))
	got := whyOf(&workflow.Why{Workflow: "w", Trigger: workflow.TriggerEvent, Event: &workflow.Event{Kind: "merged", Data: "abc123"},
		Matched: []workflow.Fact{{Name: "a", Value: true}, {Name: "b", Value: []string{"x", "y"}}, {Name: "c", Value: 36 * time.Hour}, {Name: "d", Value: at}, {Name: "e", Value: 3},
			{Name: "f", Value: float64(129600000000000)}, {Name: "g", Value: []any{"x", "y"}}, {Name: "h", Value: 1.5}}})
	want := []WhyFact{{"a", "true"}, {"b", "x, y"}, {"c", "36h0m0s"}, {"d", "2026-10-21T15:00:00Z"}, {"e", "3"}, {"f", "129600000000000"}, {"g", "x, y"}, {"h", "1.5"}}
	if got.Workflow != "w" || got.Trigger != "event" || got.Event != "merged" || got.EventData != "abc123" || len(got.Matched) != len(want) {
		t.Fatalf("%+v", got)
	}
	for i, f := range want {
		if got.Matched[i] != f {
			t.Errorf("fact %d: %+v, want %+v", i, got.Matched[i], f)
		}
	}
	if whyOf(nil) != nil {
		t.Error("a nil why became a value")
	}
}

func TestTemplateCheckRendersTheWhy(t *testing.T) {
	l := bundled(t)
	for _, name := range []dintent.Name{dintent.Review, dintent.Address, dintent.Nudge} {
		def, _ := l.Registry.Get(name)
		if problems := CheckTemplate(def, l.Templates[def.Name]); len(problems) != 0 {
			t.Errorf("%s: %v", name, problems)
		}
	}
	def, _ := l.Registry.Get(dintent.Review)
	typo := template.Must(template.New("instructions.md.tmpl").Parse("{{if .Why}}{{.Why.Wokflow}}{{end}}"))
	if problems := CheckTemplate(def, typo); len(problems) != 1 {
		t.Errorf("a typo under .Why: %v", problems)
	}
}
