package intent

import (
	"context"
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
	for _, v := range d.VerbOrder {
		ff, ft, fok := fake.Move(string(v))
		pf, pt, pok := d.Move(string(v))
		if fok != pok || ft != pt || !slices.Equal(ff, pf) {
			t.Errorf("verb %s: the fake moves %v to %q (%v), the profile %v to %q (%v)", v, ff, ft, fok, pf, pt, pok)
		}
		if got, want := fake.Handler(string(v)), d.Handler(string(v)); got != want {
			t.Errorf("verb %s: the fake's handler %q, the profile's %q", v, got, want)
		}
		for _, a := range task.Actors {
			allow, _, _ := fake.Authority(context.Background(), string(v), a, task.Task{})
			if want := d.Verbs[string(v)].By[string(a)] == "true"; allow != want {
				t.Errorf("verb %s: the fake lets %s run it %v, the profile's by %q", v, a, allow, d.Verbs[string(v)].By[string(a)])
			}
		}
	}
}
