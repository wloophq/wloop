package builtin

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
)

// anyPayloadFact is a payload fact a bundled plugin declares and the
// delivery of its kind.
func anyPayloadFact(t *testing.T) (fact, delivery string) {
	t.Helper()
	for _, k := range DraftKinds.Kinds() {
		if len(k.Facts) > 0 {
			return k.Facts[0].Name, k.Delivery
		}
	}
	t.Fatal("no bundled plugin declares a payload fact")
	return "", ""
}

func TestEveryPayloadFactIsATrustedApprovalFact(t *testing.T) {
	n := 0
	for _, k := range DraftKinds.Kinds() {
		p := k.Provider
		{
			for _, f := range k.Facts {
				n++
				if !PayloadFact(f.Name) || IsUntrusted(f.Name) {
					t.Errorf("%s: payload fact %v, untrusted %v", f.Name, PayloadFact(f.Name), IsUntrusted(f.Name))
				}
				for _, auto := range []bool{false, true} {
					if typ, ok := ApprovalEnv(auto)[f.Name]; !ok || typ != expr.String {
						t.Errorf("%s: approval env (auto %v) types it %v, %v", f.Name, auto, typ, ok)
					}
				}
				if !strings.HasPrefix(f.Name, DraftSlot+"."+string(p)+"."+k.Name+".") {
					t.Errorf("%s is not named for its kind %s of %s", f.Name, k.Name, p)
				}
			}
		}
	}
	if n == 0 {
		t.Fatal("no payload fact")
	}
	if PayloadFact(DraftDelivery) {
		t.Errorf("%s reads as a payload fact", DraftDelivery)
	}
}

func TestADraftKindFactIsNamedForItsKind(t *testing.T) {
	src := func(name string) *manifest.Source {
		s := &manifest.Source{}
		s.Provider = "forge"
		s.Slot = "forge"
		s.Deliveries = []manifest.Delivery{{Name: "review"}}
		s.DraftKinds = []manifest.DraftKind{{Name: "review", Delivery: "review", Facts: []manifest.DraftKindFact{{Name: name, Pointer: "/event"}},
			SchemaJSON: []byte(`{"type": "object", "required": ["event"], "properties": {"event": {"type": "string", "enum": ["COMMENT"]}}}`)}}
		return s
	}
	if _, err := Merge(src("draft.forge.review.event")); err != nil {
		t.Fatalf("a fact named for its kind: %v", err)
	}
	for _, name := range []string{"draft.forge.comment.event", "draft.other.review.event", "forge.review.event", "draft.forge.review.", "draft.delivery", "draft.forge.review.a.b"} {
		if _, err := Merge(src(name)); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestAReleasingRuleReadsAPayloadFactOnlyAsAnAllowList(t *testing.T) {
	f, delivery := anyPayloadFact(t)
	env := ApprovalEnv(false, UnitSlots()...)
	on := `draft.delivery == "` + delivery + `" && `
	send := draft.Approval{Mode: draft.Send}
	for _, when := range []string{
		on + f + ` == "COMMENT"`,
		on + f + ` in ["COMMENT", "REQUEST_CHANGES"]`,
		f + ` in ["COMMENT"] && ` + strings.TrimSuffix(on, " && "),
		on + f + ` in []`,
	} {
		r, err := ApprovalRule(when, send, env)
		if err != nil || !slices.Equal(r.Payload, []string{f}) {
			t.Errorf("%s: %+v, %v", when, r.Payload, err)
		}
	}
	for _, when := range []string{
		on + f + ` != "APPROVE"`,
		on + `!(` + f + ` in ["APPROVE"])`,
		on + `(` + f + ` == "COMMENT" || true)`,
		f + ` == "COMMENT" || ` + strings.TrimSuffix(on, " && "),
		on + f + ` in ["", "COMMENT"]`,
		on + f + ` == ""`,
		on + `size(` + f + `) > 0`,
		on + f + `.startsWith("C")`,
	} {
		for _, mode := range []draft.ApprovalMode{draft.Send, draft.Delay} {
			a := draft.Approval{Mode: mode}
			if mode == draft.Delay {
				a.Window = 1
			}
			var al *AllowListError
			if _, err := ApprovalRule(when, a, env); !errors.As(err, &al) || al.Fact != f || !strings.Contains(err.Error(), f) {
				t.Errorf("%s, mode %s: %v", when, mode, err)
			}
		}
		r, err := ApprovalRule(when, draft.Approval{Mode: draft.Wait}, env)
		if err != nil || !slices.Equal(r.Payload, []string{f}) {
			t.Errorf("a wait rule %s: %+v, %v", when, r.Payload, err)
		}
	}
}

func TestEveryPluginUnitSlotReadsDraftFailedAndStale(t *testing.T) {
	var slots []string
	for p, slot := range Slots {
		if _, core := coreSlots()[p]; !core {
			slots = append(slots, slot)
		}
	}
	if len(slots) == 0 {
		t.Fatal("no plugin unit slot")
	}
	for _, slot := range slots {
		env := WorkflowEnv(slot)
		for _, name := range []string{DraftFailed, DraftStale} {
			if typ, ok := env[name]; !ok || typ != expr.Bool || IsUntrusted(name) {
				t.Errorf("slot %s reads %s as %v, %v; untrusted %v", slot, name, typ, ok, IsUntrusted(name))
			}
		}
	}
	if _, ok := ApprovalEnv(true)[DraftFailed]; ok {
		t.Errorf("%s is an approval fact", DraftFailed)
	}
}
