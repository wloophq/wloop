package cli

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestAProviderKeyTargetNamesEveryCompiledInProvider(t *testing.T) {
	e := &Env{}
	for _, p := range e.sourcePlugins() {
		arg := string(p.ID) + ":acme-key"
		u, err := e.unitTarget(context.Background(), arg)
		if err != nil || u != (unit.Unit{Provider: p.ID, Key: "acme-key"}) {
			t.Errorf("%s: %+v, %v", arg, u, err)
		}
	}
	if _, err := e.unitTarget(context.Background(), "nowhere:acme-key"); err == nil {
		t.Error("an unknown provider's key named a unit")
	}
}
