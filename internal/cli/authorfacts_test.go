package cli

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/views"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/view"
)

type forgeAuthorFacts struct{ automation.Units }

func (forgeAuthorFacts) Author(context.Context, unit.Unit, bool) (*view.AuthorFacts, error) {
	return nil, nil
}

func TestTheViewsReadTheAuthorFactsOfTheModuleThatServesThem(t *testing.T) {
	served := forgeAuthorFacts{}
	mods := []pluginModule{{ID: "chat"}, {ID: "forge", Facts: served}}
	if got := authorFacts(mods); got != views.Facts(served) {
		t.Fatalf("author facts %v, want the forge module's", got)
	}
	if got := authorFacts(mods[:1]); got != nil {
		t.Fatalf("author facts %v without a module serving them", got)
	}
}
