package omnibox_test

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/core/omnibox"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var (
	api8  = unit.Unit{Provider: "alpha", Key: "acme/api#8"}
	api9  = unit.Unit{Provider: "alpha", Key: "acme/api#9"}
	api5  = unit.Unit{Provider: "alpha", Key: "acme/api#5"}
	zeta5 = unit.Unit{Provider: "alpha", Key: "zeta/api#5"}
)

// fakeRefs claims the addresses of addresses, and finds tasks by id prefix and units by label or short label.
type fakeRefs struct {
	addresses map[string]unit.Unit
	refs      map[string][]omnibox.Ref
	created   int
}

func (f *fakeRefs) Resolve(_ context.Context, address string) (unit.Unit, bool, error) {
	u, ok := f.addresses[address]
	return u, ok, nil
}

func (f *fakeRefs) Find(_ context.Context, token string) ([]omnibox.Ref, error) {
	return f.refs[token], nil
}

func ref(u unit.Unit, id task.ID) omnibox.Ref { return omnibox.Ref{Unit: &u, Task: id} }

func resolver() (omnibox.Resolver, *fakeRefs) {
	f := &fakeRefs{
		addresses: map[string]unit.Unit{"https://forge.example/acme/api/pull/8": api8},
		refs: map[string][]omnibox.Ref{
			"acme/api#8": {ref(api8, "t-0000000000000008")}, "acme/api#9": {ref(api9, "t-0000000000000009")},
			"api#5":  {ref(api5, "t-0000000000000005"), ref(zeta5, "t-00000000000000f5")},
			"t-3f9a": {{Task: "t-3f9a000000000000"}},
		},
	}
	return omnibox.Resolver{Refs: f, Vocabulary: vocabulary}, f
}

func TestResolvePastedAddress(t *testing.T) {
	r, _ := resolver()
	got, err := r.Resolve(context.Background(), "  https://forge.example/acme/api/pull/8 ")
	if err != nil || got.Kind != omnibox.KindUnit || got.Unit == nil || *got.Unit != api8 {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestResolveUnclaimedAddressFallsToSearch(t *testing.T) {
	r, f := resolver()
	got, _ := r.Resolve(context.Background(), "https://elsewhere.example/x/y/1")
	if got.Kind != omnibox.KindSearch || got.Why != omnibox.WhyUnclaimed || got.Params["address"] != "https://elsewhere.example/x/y/1" || len(got.Operations) != 0 || f.created != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestResolveVerbWithRefs(t *testing.T) {
	r, _ := resolver()
	got, _ := r.Resolve(context.Background(), "revise acme/api#8 and acme/api#9")
	if got.Kind != omnibox.KindVerb || len(got.Operations) != 2 {
		t.Fatalf("%+v", got)
	}
	for i, u := range []unit.Unit{api8, api9} {
		op := got.Operations[i]
		if op.Op != omnibox.OpAction || op.Action != "review" || op.Unit == nil || *op.Unit != u {
			t.Errorf("operation %d %+v", i, op)
		}
	}
}

func TestResolveAmbiguousShortRef(t *testing.T) {
	r, _ := resolver()
	got, _ := r.Resolve(context.Background(), "review api#5")
	if got.Kind != omnibox.KindAgent || got.Why != omnibox.WhyAmbiguous || got.Params["ref"] != "api#5" || len(got.Operations) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestResolveUnknownRef(t *testing.T) {
	r, _ := resolver()
	got, _ := r.Resolve(context.Background(), "review nope#1")
	if got.Kind != omnibox.KindSearch || got.Why != omnibox.WhyNoRef || got.Params["ref"] != "nope#1" {
		t.Fatalf("%+v", got)
	}
}

func TestResolveDelegateExistingTask(t *testing.T) {
	r, _ := resolver()
	got, _ := r.Resolve(context.Background(), "delegate t-3f9a")
	if got.Kind != omnibox.KindVerb || len(got.Operations) != 1 || got.Operations[0] != (omnibox.AskOp{Op: omnibox.OpVerb, Task: "t-3f9a000000000000", Verb: "delegate"}) {
		t.Fatalf("%+v", got)
	}
}

func TestResolveDelegateNewWork(t *testing.T) {
	r, _ := resolver()
	got, _ := r.Resolve(context.Background(), "delegue add retries to the sender")
	if got.Kind != omnibox.KindVerb || len(got.Operations) != 1 || got.Operations[0] != (omnibox.AskOp{Op: omnibox.OpStart, Brief: "add retries to the sender"}) {
		t.Fatalf("%+v", got)
	}
}

func TestResolveCapture(t *testing.T) {
	r, _ := resolver()
	got, _ := r.Resolve(context.Background(), "capture call Ada")
	if got.Kind != omnibox.KindVerb || len(got.Operations) != 1 || got.Operations[0] != (omnibox.AskOp{Op: omnibox.OpCapture, Title: "call Ada"}) {
		t.Fatalf("%+v", got)
	}
}

func TestResolveAVerbWithNothingToRunIsSearch(t *testing.T) {
	r, _ := resolver()
	for _, text := range []string{"review", "review the retries", "capture"} {
		got, _ := r.Resolve(context.Background(), text)
		if got.Kind != omnibox.KindSearch || len(got.Operations) != 0 {
			t.Errorf("%q: %+v", text, got)
		}
	}
}

func TestResolveOtherTextIsSearch(t *testing.T) {
	r, _ := resolver()
	got, _ := r.Resolve(context.Background(), "what did Ada ask")
	if got.Kind != omnibox.KindSearch || got.Why != "" {
		t.Fatalf("%+v", got)
	}
}
