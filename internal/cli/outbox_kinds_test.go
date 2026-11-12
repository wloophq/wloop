package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/intent/builtin"
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

// TestVocabularyNamesTheKindOfADelivery checks a draft with no kind takes its provider's first kind of its
// delivery, or its first kind with none, and that an undeclared delivery has none.
func TestVocabularyNamesTheKindOfADelivery(t *testing.T) {
	var k outbox.Kinds = draftKinds
	first := map[string]draft.KindSpec{}
	for _, s := range builtin.DraftKinds.Kinds() {
		if _, ok := first[s.Provider+"/"+s.Delivery]; !ok {
			first[s.Provider+"/"+s.Delivery] = s
		}
		if _, ok := first[s.Provider+"/"]; !ok {
			first[s.Provider+"/"] = s
		}
	}
	if len(first) == 0 {
		t.Fatal("no kinds installed")
	}
	for key, want := range first {
		got, ok := k.ForDelivery(want.Provider, want.Delivery)
		if key == want.Provider+"/" {
			got, ok = k.ForDelivery(want.Provider, "")
		}
		if !ok || got.Name != want.Name {
			t.Errorf("%s: %+v %v, want %s", key, got, ok, want.Name)
		}
	}
	if _, ok := k.ForDelivery("slack", "carrier_pigeon"); ok {
		t.Error("an undeclared delivery has a kind")
	}
}
