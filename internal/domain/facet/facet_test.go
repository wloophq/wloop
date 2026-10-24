package facet_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/domain/facet"
)

func TestTheBriefSchemaListsTheSameValues(t *testing.T) {
	var s struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(brief.Schema), &s); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string][]string{"kind": facet.Kinds, "size": facet.Sizes, "risk": facet.Risks} {
		if got := s.Properties[key].Enum; !slices.Equal(got, want) {
			t.Errorf("%s: the schema lists %v, facet %v", key, got, want)
		}
	}
}
