package situation

import (
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

func TestKindLabel(t *testing.T) {
	for k, want := range map[Kind]string{KindHeldByCap: "held (cap)", KindRoundPending: "round soon", KindChanges: "changes asked", KindBlocked: "round blocked", Kind("new"): "new"} {
		if got := k.Label(); got != want {
			t.Errorf("%s: %q, want %q", k, got, want)
		}
	}
}

func TestOrderHoldsEveryLabeledKindOnce(t *testing.T) {
	if len(Order) != len(labels) {
		t.Fatalf("Order has %d kinds, labels %d", len(Order), len(labels))
	}
	seen := map[Kind]bool{}
	for _, k := range Order {
		if _, ok := labels[k]; !ok || seen[k] {
			t.Errorf("%s: labeled %v, twice %v", k, ok, seen[k])
		}
		seen[k] = true
	}
}

func TestRowKindEnumIsEveryKind(t *testing.T) {
	var kinds []string
	for _, k := range Order {
		kinds = append(kinds, string(k))
	}
	var row builtin.Fact
	for _, f := range builtin.ActionFacts {
		if f.Name == "row.kind" {
			row = f
		}
	}
	if got := row.Enum[unit.GitHub]; !slices.Equal(got, kinds) {
		t.Errorf("row.kind's GitHub enum is %q, want %q", got, kinds)
	}
}
