package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/task"
)

// The counters a client reads carry each view's count; no client reads a
// sum of the badge's views, so none is served.
func TestTheCountersServeNoBadgeSum(t *testing.T) {
	t.Parallel()
	b, err := json.Marshal(wireCounters(task.Counters{Views: map[task.View]int{"inbox": 2}}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"badge"`) || !strings.Contains(string(b), `"inbox":2`) {
		t.Fatalf("counters %s, want the views' counts and no badge", b)
	}
}
