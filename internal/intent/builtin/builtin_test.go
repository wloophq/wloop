package builtin

import (
	"slices"
	"testing"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/expr"
)

func TestEnvHasEveryFactOfTheSlotAndTheInputs(t *testing.T) {
	inputs := []dintent.Input{{Name: "take_over", Type: "flag"}, {Name: "note", Type: "text"}, {Name: "who", Type: "list"}}
	for _, slot := range []FactSlot{ActionSlot, TurnSlot} {
		env := Env(slot, inputs)
		want := 0
		for _, f := range Facts {
			if !slices.Contains(f.Slots, slot) {
				if _, ok := env[f.Name]; ok && !slotHas(slot, f.Name) {
					t.Errorf("%s env holds %s, a fact of another slot", slot, f.Name)
				}
				continue
			}
			want++
			if env[f.Name] != f.Type {
				t.Errorf("%s env: %s = %v, want %v", slot, f.Name, env[f.Name], f.Type)
			}
		}
		if slot == TurnSlot {
			want += len(inputs)
			for name, typ := range map[string]expr.Type{"inputs.take_over": expr.Bool, "inputs.note": expr.String, "inputs.who": expr.List} {
				if env[name] != typ {
					t.Errorf("turn env: %s = %v, want %v", name, env[name], typ)
				}
			}
		}
		if n := len(env.Names()); n != want {
			t.Errorf("%s env has %d fields, want %d", slot, n, want)
		}
	}
	if _, ok := Env(ActionSlot, inputs)["inputs.take_over"]; ok {
		t.Error("the action slot reads inputs")
	}
}

func slotHas(slot FactSlot, name string) bool {
	return slices.ContainsFunc(Facts, func(f Fact) bool { return f.Name == name && slices.Contains(f.Slots, slot) })
}

func TestEveryFactHasADoc(t *testing.T) {
	seen := map[FactSlot]map[string]bool{}
	for _, f := range Facts {
		if f.Doc == "" || len(f.Slots) == 0 || f.Type == 0 {
			t.Errorf("%s: doc %q, slots %v, type %v", f.Name, f.Doc, f.Slots, f.Type)
		}
		for _, s := range f.Slots {
			if seen[s] == nil {
				seen[s] = map[string]bool{}
			}
			if seen[s][f.Name] {
				t.Errorf("%s twice in slot %s", f.Name, s)
			}
			seen[s][f.Name] = true
		}
	}
}

func TestReservedNamesHaveNoDuplicates(t *testing.T) {
	seen := map[string]bool{}
	for _, n := range ReservedNames {
		if seen[n] {
			t.Errorf("%s twice", n)
		}
		seen[n] = true
	}
}

func TestBundledIntentNamesAreNotReserved(t *testing.T) {
	for _, n := range []dintent.Name{dintent.Review, dintent.Address, dintent.Nudge, dintent.Probe} {
		if slices.Contains(ReservedNames, string(n)) {
			t.Errorf("%s is reserved", n)
		}
	}
}

func TestEverySlotHasAProvider(t *testing.T) {
	for _, p := range []unit.ProviderID{unit.GitHub, unit.Session, unit.Workspace} {
		if Slots[p] == "" {
			t.Errorf("provider %s has no fact slot", p)
		}
	}
	for _, s := range Steps {
		for _, p := range s.Providers {
			if Slots[p] == "" {
				t.Errorf("step %s names provider %s, which has no fact slot", s.Name, p)
			}
		}
	}
}
