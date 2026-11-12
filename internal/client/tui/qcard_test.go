package tui

import (
	"testing"

	"github.com/wloophq/wloop/internal/api"
)

// TestTheDetailListsNoPinnedDraft leaves out of an open task's drafts those an open question pins, which its
// question shows under the option that pins each.
func TestTheDetailListsNoPinnedDraft(t *testing.T) {
	m := newModel(t)
	m.detailID = "t-1"
	m.SetDrafts("t-1", []api.Draft{{ID: 42, PinnedBy: 3}, {ID: 44}})
	if got := m.listedDrafts(); len(got) != 1 || got[0].ID != 44 {
		t.Fatalf("listed %+v, want only draft 44", got)
	}
}
