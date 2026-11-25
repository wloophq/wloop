package daemon

import (
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/task"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/internal/turn"
)

func TestHomeEndpoint(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, task.Task{ID: "t-0000000000000001", State: "next", Category: task.CategoryReady, Title: "ship the retries"},
		task.Task{ID: "t-0000000000000002", State: "next", Category: task.CategoryReady, Title: "call Ada"})
	r.withGates(t, "t-0000000000000001")
	var h api.Home
	if code := get(t, r.s, api.HomePath, &h); code != 200 {
		t.Fatalf("%d", code)
	}
	var ids []string
	for _, c := range h.Briefing.Counters {
		ids = append(ids, c.ID)
	}
	if want := []string{"needs-you", "loop", "others"}; !slices.Equal(ids, want) {
		t.Fatalf("counters %v, want %v", ids, want)
	}
	needs := h.Briefing.Counters[0]
	if needs.View != "needs-you" || needs.Count != 1 || needs.Label != "1 task" {
		t.Fatalf("needs-you counter %+v", needs)
	}
	if len(h.Briefing.Sentences) != 1 {
		t.Fatalf("sentences %+v", h.Briefing.Sentences)
	}
	var b strings.Builder
	for _, p := range h.Briefing.Sentences[0] {
		if p.Counter != nil {
			b.WriteString("[" + h.Briefing.Counters[*p.Counter].Label + "]")
		} else {
			b.WriteString(p.Text)
		}
	}
	if got := b.String(); got != "[1 task] needs you." {
		t.Fatalf("sentence %q", got)
	}
	var sections []string
	for _, s := range h.Sections {
		sections = append(sections, s.ID)
	}
	if !slices.Equal(sections, []string{"needs-you", "stalled"}) || h.Sections[0].Label != "Needs you" {
		t.Fatalf("sections %+v", h.Sections)
	}
	if needs := h.Sections[0]; needs.Total != 1 || len(needs.Items) != 1 || needs.Items[0].Task.ID != "t-0000000000000001" {
		t.Fatalf("needs you %+v", needs)
	}
	if stalled := h.Sections[1]; stalled.Total != 2 || len(stalled.Rows) != 2 {
		t.Fatalf("stalled %+v", stalled)
	}
	if h.Capture.Hint != "Capture what's on your mind" || h.Capture.Key != "n" {
		t.Fatalf("capture %+v", h.Capture)
	}
}

// loadedIn is the bundled profile and plugins loaded in locale.
func loadedIn(t testing.TB, locale string) *intentload.Loaded {
	t.Helper()
	base, err := turn.Base()
	if err != nil {
		t.Fatal(err)
	}
	l, problems := intentload.LoadIn(locale, profile.BundledChain(), turn.BundledFS(), base, turn.CheckTemplate)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	return l
}

func TestHomeInPtBR(t *testing.T) {
	t.Parallel()
	pt := loadedIn(t, "pt-BR")
	r := newTasksRig(t, func(d *Deps) { d.Intents = func() *intentload.Loaded { return pt } })
	r.put(t, task.Task{ID: "t-0000000000000001", State: "next", Category: task.CategoryReady, Title: "ship the retries"})
	r.withGates(t, "t-0000000000000001")
	var h api.Home
	if code := get(t, r.s, api.HomePath, &h); code != 200 {
		t.Fatalf("%d", code)
	}
	if got := h.Briefing.Counters[0].Label; got != "1 tarefa" {
		t.Fatalf("label %q", got)
	}
	if h.Sections[0].Label != "Precisa de você" || h.Sections[0].Empty != "Nada precisa de você." || h.Sections[1].Label != "Paradas há mais tempo" || h.Capture.Hint != "Anote o que está na sua cabeça" {
		t.Fatalf("home %+v", h)
	}
}
