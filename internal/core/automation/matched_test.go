package automation

import (
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/workflow"
)

type readsOnly []string

func (r readsOnly) Match(workflow.Vars) (bool, error) { return true, nil }
func (r readsOnly) Reads() []string                   { return r }

// TestAMatchedDurationIsKeptToTheMinute keeps a duration a predicate read to the minute, so a plan item whose
// clock fact grows between two passes reads the same why within the minute and its served plan does not move.
func TestAMatchedDurationIsKeptToTheMinute(t *testing.T) {
	v := workflow.Vars{Facts: workflow.Facts{"session.idle_for": 11*time.Hour + 26*time.Minute + 27*time.Second + 22*time.Millisecond},
		Config: workflow.Facts{"config.cleanup.idle": 2 * time.Hour}}
	got := matched(readsOnly{"session.idle_for", "config.cleanup.idle"}, v)
	want := map[string]any{"config.cleanup.idle": 2 * time.Hour, "session.idle_for": 11*time.Hour + 26*time.Minute}
	for _, f := range got {
		if f.Value != want[f.Name] {
			t.Errorf("%s = %v, want %v", f.Name, f.Value, want[f.Name])
		}
	}
}
