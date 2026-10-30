package tui

import (
	"testing"

	"github.com/wloophq/wloop/internal/api"
)

func TestDraftTargetSaysWhenTheDestinationIsUnknown(t *testing.T) {
	for _, tc := range []struct {
		d    api.Draft
		want string
	}{
		{api.Draft{Target: "C0REL", Where: "#releases in the thread \"Ship\""}, `#releases in the thread "Ship"`},
		{api.Draft{Target: "C0REL"}, "C0REL (destination unknown)"},
		{api.Draft{Target: "C0REL", Permalink: "https://example.test/p/1"}, "C0REL"},
	} {
		d := tc.d
		if got := draftTarget(draftTask(api.ViewsCatalog{}, d, "")); got != tc.want {
			t.Errorf("%+v: %q, want %q", d, got, tc.want)
		}
	}
}
