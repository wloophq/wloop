package cli

import (
	"io/fs"
	"slices"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/turn"
)

func TestTheCatalogWordsTheItemsResolveRefusalsAPlanItemShows(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"strings.toml", "locales/pt-BR/strings.toml"} {
		raw, err := fs.ReadFile(turn.BundledFS(), path)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct{ Strings map[string]any }
		if err := toml.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		for _, key := range builtin.StepRefusals {
			if _, ok := doc.Strings[key]; !ok {
				t.Errorf("%s does not word %q", path, key)
			}
		}
	}
	for _, key := range builtin.StepRefusals {
		if !slices.Contains(api.PlanReasons, key) {
			t.Errorf("api.PlanReasons lacks %q, so no client reads it through its catalog", key)
		}
	}
	if !slices.Contains(builtin.StepRefusals, builtin.RefusalNoOpenItem) || !slices.Contains(builtin.StepRefusals, builtin.RefusalNotDerived) {
		t.Errorf("StepRefusals %q lack items.resolve's", builtin.StepRefusals)
	}
}
