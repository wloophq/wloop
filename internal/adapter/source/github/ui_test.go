package ghsource

import (
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestGitHubUIKeysLoad(t *testing.T) {
	if err := catalog.CheckPluginKeys(unit.GitHub, UI()); err != nil {
		t.Fatal(err)
	}
}

func TestTheMenubarListsOnlyInLoop(t *testing.T) {
	got := UI().Menubar
	if len(got) != 1 || got[0] != (api.MenubarSection{Label: "In loop", View: api.ViewLoop}) {
		t.Fatalf("menu bar sections %+v; the Inbox and Next replace Needs me", got)
	}
}
