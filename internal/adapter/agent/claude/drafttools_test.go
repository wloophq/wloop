package claude

import (
	"io/fs"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/transport/mcp"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	"github.com/wloophq/wloop/plugins"
)

// proposingTools are the tools the bundled manifests mark proposes = true, as an agent calls them.
func proposingTools(t *testing.T) (proposes, others []string) {
	t.Helper()
	paths, err := fs.Glob(plugins.Manifests, "*/plugin.toml")
	if err != nil || len(paths) == 0 {
		t.Fatalf("manifests %v: %v", paths, err)
	}
	for _, p := range paths {
		raw, err := fs.ReadFile(plugins.Manifests, p)
		if err != nil {
			t.Fatal(err)
		}
		m, err := manifest.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		s, err := m.Source()
		if err != nil {
			continue
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
		if slices.Contains(settingsAllow(t, turns.Policy{TurnEnd: true, Drafts: true}), tool) {
			t.Errorf("a policy with no DraftTools allows %s", tool)
		}
	}
}
