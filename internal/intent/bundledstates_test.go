package intent

import (
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/domain/task"
)

// TestBundledStatesMatchTheBundledProfile keeps the tests' taskstest.BundledStates the bundled tasks.toml.
func TestBundledStatesMatchTheBundledProfile(t *testing.T) {
	t.Parallel()
	d, problems := loadTasksWith(t, defaultTasks(t), "")
	if problems != "" {
		t.Fatal(problems)
	}
	fake := taskstest.BundledStates
	for _, s := range d.States {
		if c, ok := fake.CategoryOf(s.Name); !ok || c != s.Category {
			t.Errorf("state %s: the fake says %q %v, the profile %q", s.Name, c, ok, s.Category)
		}
	}
	if len(d.States) != 8 {
		t.Errorf("the profile declares %d states; update taskstest.BundledStates", len(d.States))
	}
	for _, c := range task.Categories {
		if got, want := fake.FirstOf(c), d.FirstOf(c); got != want {
			t.Errorf("FirstOf(%s): the fake %q, the profile %q", c, got, want)
		}
	}
	for _, v := range task.Verbs {
		ff, ft, fok := fake.Move(string(v))
		pf, pt, pok := d.Move(string(v))
		if fok != pok || ft != pt || !slices.Equal(ff, pf) {
			t.Errorf("verb %s: the fake moves %v to %q (%v), the profile %v to %q (%v)", v, ff, ft, fok, pf, pt, pok)
		}
	}
}
