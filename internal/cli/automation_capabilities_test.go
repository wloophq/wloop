package cli

import (
	"testing"

	"github.com/wloophq/wloop/internal/core/automation"
)

// TestAutomationCapabilitiesAreSnapshotFields: the unit set routes none of
// the engine's optional capabilities; a plugin's units read as snapshot
// fields, host bookkeeping or Notify.
func TestAutomationCapabilitiesAreSnapshotFields(t *testing.T) {
	var set any = (*unitSet)(nil)
	for name, kept := range map[string]bool{
		"Refresher":  implements[automation.Refresher](set),
		"Settler":    implements[automation.Settler](set),
		"Gainer":     implements[automation.Gainer](set),
		"Owner":      implements[automation.Owner](set),
		"Seener":     implements[automation.Seener](set),
		"Scoper":     implements[automation.Scoper](set),
		"EventFacts": implements[automation.EventFacts](set),
	} {
		if kept {
			t.Errorf("the unit set still routes %s", name)
		}
	}
}

func implements[T any](v any) bool {
	_, ok := v.(T)
	return ok
}
