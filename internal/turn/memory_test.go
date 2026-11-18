package turn

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/brand/brandtest"
	"github.com/wloophq/wloop/internal/core/memory"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const memoryGolden = "testdata/memory.golden"

func TestInstructionsCarryWhatEarlierTurnsLearned(t *testing.T) {
	c := scenario(t, "review/history/injected").Context
	at := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	thread := unit.Unit{Provider: "slack", Key: "C1/1700000000.000100"}
	c.Memory = MemoryOf(memory.Memory{
		Outcome: &memory.Outcome{ReopenedAt: at, Summary: "merged the fix and replied", Items: []memory.Resolved{
			{Unit: thread, Item: "i1", Summary: "fix the login", Resolution: items.Fixed},
		}},
		Learned: []memory.Entry{
			{Unit: c.Unit, At: at.Add(-1 * time.Hour), Text: "The flaky test needs the fixture clock."},
			{Unit: thread, At: at.Add(-2 * time.Hour), Text: "The owner wants it by Friday.\nAsk before changing the API."},
			{Unit: c.Unit, At: at.Add(-3 * time.Hour), Text: "Go 1.24 is required."},
			{Unit: c.Unit, At: at.Add(-4 * time.Hour), Text: "an older entry"},
			{Unit: c.Unit, At: at.Add(-5 * time.Hour), Text: "the oldest entry"},
		},
	})
	r, err := render(c)
	if err != nil {
		t.Fatal(err)
	}
	got := section(r.Text, "### What earlier turns learned\n")
	if *updateGolden {
		brandtest.CanUpdate(t)
		if err := os.WriteFile(memoryGolden, []byte(brandtest.Collapse(got)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(memoryGolden)
	if err != nil {
		t.Fatal(err)
	}
	if want := brandtest.Expand(string(raw)); got != want {
		t.Fatalf("the memory section differs (go test -run %s -update rewrites it):\n%s", t.Name(), firstDifference(want, got))
	}
	for _, want := range []string{"Go 1.24 is required.", "2 earlier entries", "task_read", "merged the fix and replied", "fix the login"} {
		if !strings.Contains(got, want) {
			t.Errorf("the section lacks %q", want)
		}
	}
	if strings.Contains(got, "an older entry") {
		t.Error("an entry past the recent ones is injected")
	}
}

func TestMemoryOfCapsTheInjectedEntries(t *testing.T) {
	var all []memory.Entry
	for range memory.Recent + 4 {
		all = append(all, memory.Entry{Text: "x"})
	}
	m := MemoryOf(memory.Memory{Learned: all})
	if m == nil || len(m.Recent) != memory.Recent || m.Earlier != 4 {
		t.Fatalf("memory %+v", m)
	}
	if MemoryOf(memory.Memory{}) != nil {
		t.Error("an empty memory renders a section")
	}
}

func TestInstructionsWithoutMemoryUnchanged(t *testing.T) {
	for _, s := range Scenarios() {
		if s.Context.Memory != nil {
			t.Fatalf("%s carries a memory", s.Name)
		}
		r, err := render(s.Context)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(r.Text, "What earlier turns learned") {
			t.Errorf("%s renders a memory section", s.Name)
		}
	}
	TestScenariosRenderTheirGolden(t)
}
