package jsonschema

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestEveryDraftKindSchemaCompilesAndEachFixtureFits runs each bundled plugin's draft kind schemas
// through the subset, and checks every payload fixture against its kind's schema.
func TestEveryDraftKindSchemaCompilesAndEachFixtureFits(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	schemas, _ := filepath.Glob(filepath.Join(root, "plugins/*/drafts/*/schema.json"))
	if len(schemas) < 10 {
		t.Fatalf("found only %v", schemas)
	}
	for _, p := range schemas {
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Compile(src, Options{}); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	fixtures, _ := filepath.Glob(filepath.Join(root, "plugins/*/drafts/testdata/*.json"))
	if len(fixtures) < 4 {
		t.Fatalf("found only %v", fixtures)
	}
	for _, p := range fixtures {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var fx struct {
			Kind    string          `json:"kind"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(raw, &fx); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		src, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(p)), fx.Kind, "schema.json"))
		if err != nil {
			t.Fatalf("%s: kind %q has no schema: %v", p, fx.Kind, err)
		}
		s, err := Compile(src, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.ValidateJSON(fx.Payload); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}
