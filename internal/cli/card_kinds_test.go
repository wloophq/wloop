package cli

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/wloophq/wloop/internal/intent/builtin"
)

// TestCardKindsAreTheOutboxKinds reads every kind the card shows from the outbox's own catalog, and the schema
// of each kind that has one from the bundled plugins' files.
func TestCardKindsAreTheOutboxKinds(t *testing.T) {
	k := cardDraftKinds()
	kinds := builtin.DraftKinds.Kinds()
	if len(kinds) == 0 {
		t.Fatal("the catalog declares no kind")
	}
	for _, want := range kinds {
		got, ok := k.Lookup(want.Provider, want.Name)
		if !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("%s/%s is %+v, want the outbox's %+v", want.Provider, want.Name, got, want)
		}
		if want.Schema == "" {
			continue
		}
		raw, err := k.Schema(want)
		if err != nil || !json.Valid(raw) {
			t.Errorf("%s/%s schema: %v", want.Provider, want.Name, err)
		}
	}
	if _, ok := k.Lookup("github", "nope"); ok {
		t.Error("an undeclared kind is found")
	}
}

func TestServeNotifiesRuleSent(t *testing.T) {
	t.Skip("T13: M2 of reviews/T8.md")
}
