package builtin

import (
	"testing"

	"github.com/wloophq/wloop/internal/domain/decision"
)

func TestEveryDecisionKindHasADoc(t *testing.T) {
	vs := DecisionKinds()
	if len(vs) != len(decision.Kinds) {
		t.Fatalf("%d values for %d kinds", len(vs), len(decision.Kinds))
	}
	for i, v := range vs {
		if v.Name != string(decision.Kinds[i]) || v.Doc == "" {
			t.Errorf("value %d: %+v", i, v)
		}
	}
}
