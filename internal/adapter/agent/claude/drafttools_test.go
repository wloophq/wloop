package claude

import (
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/plugins/bundled"
	"github.com/wloophq/wloop/internal/transport/mcp"
)

// proposingTools are the tools the bundled manifests mark proposes = true, as an agent calls them.
func proposingTools(t *testing.T) (proposes, others []string) {
	t.Helper()
	ms, err := bundled.Manifests()
	if err != nil || len(ms) == 0 {
		t.Fatalf("manifests %d: %v", len(ms), err)
	}
	for _, m := range ms {
		s, err := m.Source()
		if err != nil {
			t.Fatalf("plugin %s: %v", m.ID, err)
		}
		for _, tool := range s.Tools {
			if tool.Proposes {
				proposes = append(proposes, brand.ToolPrefix()+tool.Name)
			} else {
				others = append(others, brand.ToolPrefix()+tool.Name)
			}
		}
	}
	return proposes, others
}

func TestDraftToolsAllowedFromManifests(t *testing.T) {
	proposes, others := proposingTools(t)
	if len(proposes) < 2 {
		t.Fatalf("the bundled manifests propose through %v", proposes)
	}
	want := append(slices.Clone(proposes), mcp.DraftTools()...)
	allow := settingsAllow(t, turns.Policy{TurnEnd: true, DraftTools: want})
	for _, tool := range want {
		if !slices.Contains(allow, tool) {
			t.Errorf("allow %v lacks %s", allow, tool)
		}
	}
	for _, tool := range others {
		if slices.Contains(allow, tool) {
			t.Errorf("allow %v holds %s, which posts or reads without proposing", allow, tool)
		}
	}
	for _, tool := range want {
		if slices.Contains(settingsAllow(t, turns.Policy{TurnEnd: true}), tool) {
			t.Errorf("a policy with no DraftTools allows %s", tool)
		}
	}
}
