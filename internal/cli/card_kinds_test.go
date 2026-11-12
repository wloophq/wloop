package cli

import (
	"testing"
	"testing/fstest"
)

func TestCardKindsReadTheManifestsKindAndItsSchema(t *testing.T) {
	k := cardKinds{files: fstest.MapFS{"github/drafts/review/schema.json": {Data: []byte(`{"type":"object"}`)}}}
	spec, ok := k.Lookup("github", "review")
	if !ok || spec.Delivery != "review" || spec.Schema != "drafts/review/schema.json" || len(spec.Editable) != 2 {
		t.Fatalf("review is %+v, %v", spec, ok)
	}
	if raw, err := k.Schema(spec); err != nil || string(raw) != `{"type":"object"}` {
		t.Fatalf("schema %s, %v", raw, err)
	}
	if _, ok := k.Lookup("github", "nope"); ok {
		t.Fatal("an undeclared kind is found")
	}
}
