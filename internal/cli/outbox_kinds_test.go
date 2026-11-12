package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
)

// TestVocabularyIsOutboxKinds checks the outbox's kinds are the bundled manifests': each payload fixture fits
// its kind's schema, read from the embedded drafts/, and a payload that does not is refused as invalid.
func TestVocabularyIsOutboxKinds(t *testing.T) {
	var k outbox.Kinds = draftKinds
	fixtures, _ := filepath.Glob(filepath.Join("..", "..", "plugins", "*", "drafts", "testdata", "*.json"))
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
			t.Fatal(err)
		}
		provider := filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(p))))
		spec, ok := k.Lookup(provider, fx.Kind)
		if !ok {
			t.Fatalf("%s: no kind %s of %s", p, fx.Kind, provider)
		}
		if err := k.Validate(spec, fx.Payload); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	review, _ := k.Lookup("github", "review")
	for _, bad := range []string{`{}`, `{"event": "MERGE"}`, `{"event": "COMMENT", "comments": [{"path": "a.go"}]}`, `{"event": "COMMENT", "extra": true}`} {
		if err := k.Validate(review, json.RawMessage(bad)); !errors.Is(err, outbox.ErrInvalid) || !errors.Is(err, draft.ErrPayload) {
			t.Errorf("payload %s: %v", bad, err)
		}
	}
	if _, ok := k.Lookup("slack", "review"); ok {
		t.Error("slack declares a review")
	}
	if _, err := draftKinds.Shape("slack", draft.Draft{Kind: "review"}); !errors.Is(err, outbox.ErrInvalid) || !errors.Is(err, draft.ErrUnknownKind) {
		t.Errorf("an undeclared kind shaped: %v", err)
	}
}
