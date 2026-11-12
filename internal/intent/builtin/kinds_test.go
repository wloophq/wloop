package builtin

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
)

// legacyKinds are the kinds the core named before the manifests declared them.
var legacyKinds = []draft.Kind{"reply", "nudge", "close", "react", "ask_owner", "comment", "edit", "delete"}

// shapeOf is the purpose and delivery the core gave a draft of kind k before the manifests declared kinds.
func shapeOf(k draft.Kind) (string, draft.Delivery) {
	switch k {
	case "react":
		return string(k), "reaction"
	case "comment", "edit", "delete":
		return string(k), draft.Delivery(k)
	}
	return string(k), "message"
}

// kindOf is the kind the core read for a purpose and a delivery before the manifests declared kinds.
func kindOf(purpose string, d draft.Delivery) draft.Kind {
	switch d {
	case "reaction":
		return "react"
	case "comment", "edit", "delete":
		return draft.Kind(d)
	}
	switch k := draft.Kind(purpose); k {
	case "nudge", "close", "ask_owner":
		return k
	}
	return "reply"
}

// legacyShape is how the outbox shaped a draft before the manifests declared kinds: shapeOf and kindOf, then
// the delivery the provider's manifest gives the kind.
func legacyShape(c *draft.Catalog, provider string, d draft.Draft) draft.Draft {
	if d.Purpose == "" && d.Kind != "" {
		d.Purpose, d.Delivery = shapeOf(d.Kind)
	}
	d.Delivery = cmp.Or(d.Delivery, draft.Delivery("message"))
	d.Kind = kindOf(d.Purpose, d.Delivery)
	got := legacyDelivers(c, provider, d.Kind)
	if got != d.Delivery {
		d.Delivery, d.Kind = got, kindOf(d.Purpose, got)
	}
	return d
}

func legacyDelivers(c *draft.Catalog, provider string, k draft.Kind) draft.Delivery {
	if spec, ok := c.Lookup(provider, string(k)); ok {
		return draft.Delivery(spec.Delivery)
	}
	defaults := map[string]draft.Delivery{"github": "comment", "slack": "message"}
	return defaults[provider]
}

// TestKindsFromManifests checks the catalog holds every kind the bundled manifests declare, and shapes every
// draft the core shaped before as it did: the same purpose and delivery, and the same kind but where the
// provider declares the draft's purpose as a kind of its own.
func TestKindsFromManifests(t *testing.T) {
	c := Installed().DraftKinds()
	want := map[string][]string{
		"github": {"comment", "reply", "nudge", "close", "ask_owner", "react", "edit", "review", "thread_reply", "resolve"},
		"slack":  {"reply", "nudge", "close", "ask_owner", "react", "unreact", "edit", "delete"},
	}
	for p, names := range want {
		for _, n := range names {
			k, ok := c.Lookup(p, n)
			if !ok || len(k.SchemaJSON) == 0 || k.Delivery == "" {
				t.Errorf("%s of %s: %+v, %v", n, p, k, ok)
			}
		}
	}
	review, _ := c.Lookup("github", "review")
	if review.Basis != draft.BasisAgent || !review.Posts || !slices.Equal(review.Facts, []draft.FactSpec{{Name: "draft.github.review.event", Pointer: "/event"}}) {
		t.Errorf("review %+v", review)
	}
	if !c.Posts("github", "comment") || c.Posts("github", "reaction") || !c.Posts("slack", "message") || c.Posts("slack", "edit") {
		t.Error("posts")
	}
	if DraftKinds != c {
		t.Error("the installed catalog is not the installed vocabulary's")
	}
	var inputs []draft.Draft
	for _, k := range legacyKinds {
		inputs = append(inputs, draft.Draft{Kind: k})
	}
	for _, purpose := range []string{"reply", "nudge", "close", "ask_owner", "thanks"} {
		for _, d := range []draft.Delivery{"", "message", "reaction"} {
			inputs = append(inputs, draft.Draft{Purpose: purpose, Delivery: d})
		}
	}
	for _, p := range []string{"github", "slack"} {
		for _, in := range inputs {
			name := fmt.Sprintf("%s/kind=%s,purpose=%s,delivery=%s", p, in.Kind, in.Purpose, in.Delivery)
			old := legacyShape(c, p, in)
			got, err := c.Shape(p, in)
			if err != nil {
				if _, declared := c.Lookup(p, string(in.Kind)); in.Kind != "" && !declared {
					continue
				}
				t.Errorf("%s: %v", name, err)
				continue
			}
			if got.Purpose != old.Purpose || got.Delivery != old.Delivery {
				t.Errorf("%s: shaped %s/%s, before %s/%s", name, got.Purpose, got.Delivery, old.Purpose, old.Delivery)
			}
			if _, own := c.Lookup(p, got.Purpose); got.Kind != old.Kind && !(own && got.Kind == draft.Kind(got.Purpose)) {
				t.Errorf("%s: kind %s, before %s", name, got.Kind, old.Kind)
			}
		}
	}
}

func TestUnknownKindRefused(t *testing.T) {
	c := Installed().DraftKinds()
	for _, x := range []struct{ provider, kind string }{{"github", "delete"}, {"github", "unreact"}, {"slack", "review"}, {"slack", "comment"}, {"github", "nope"}} {
		if _, err := c.Shape(x.provider, draft.Draft{Kind: draft.Kind(x.kind)}); !errors.Is(err, draft.ErrUnknownKind) {
			t.Errorf("%s of %s: %v", x.kind, x.provider, err)
		}
		if _, ok := c.Lookup(x.provider, x.kind); ok {
			t.Errorf("%s of %s is declared", x.kind, x.provider)
		}
	}
	src := bundledSource(t, "github")
	src.DraftKinds[0].SchemaJSON = []byte(`{"type": "object", "oneOf": []}`)
	v, err := Merge(src)
	if err == nil {
		t.Fatal("a kind whose schema leaves the subset merged")
	}
	if _, ok := v.DraftKinds().Lookup("github", src.DraftKinds[0].Name); ok {
		t.Error("the plugin of a refused kind kept its kinds")
	}
}

func TestSameKindNameTwoProviders(t *testing.T) {
	c := Installed().DraftKinds()
	gh, _ := c.Lookup("github", "reply")
	sl, _ := c.Lookup("slack", "reply")
	if gh.Delivery != "comment" || sl.Delivery != "message" || gh.Provider != "github" || sl.Provider != "slack" {
		t.Errorf("github %+v, slack %+v", gh, sl)
	}
	for p, want := range map[unit.ProviderID]draft.Delivery{"github": "comment", "slack": "message"} {
		d, err := c.Shape(string(p), draft.Draft{Kind: "reply"})
		if err != nil || d.Delivery != want {
			t.Errorf("a reply of %s: %+v, %v", p, d, err)
		}
	}
}

func bundledSource(t *testing.T, id string) *manifest.Source {
	t.Helper()
	for _, s := range bundledSources() {
		if s.Provider == id {
			return s
		}
	}
	t.Fatalf("no bundled plugin %s", id)
	return nil
}

func BenchmarkShapeAndValidate(b *testing.B) {
	c := Installed().DraftKinds()
	review, _ := c.Lookup("github", "review")
	payload := []byte(`{"event": "REQUEST_CHANGES", "comments": [{"path": "a.go", "line": 4, "body": "x"}]}`)
	b.Run("shape", func(b *testing.B) {
		for b.Loop() {
			if _, err := c.Shape("slack", draft.Draft{Purpose: "thanks", Delivery: "message"}); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("validate", func(b *testing.B) {
		for b.Loop() {
			if err := c.Validate(review, payload); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func TestEmojiDeliveriesComeFromManifests(t *testing.T) {
	got := slices.Sorted(slices.Values(DraftEmojiDeliveries))
	if want := []string{"reaction", "unreaction"}; !slices.Equal(got, want) {
		t.Fatalf("emoji deliveries %v, want the bundled Slack manifest's %v", got, want)
	}
}

func TestVerdictsAreTheServedPluginsKindsVerdicts(t *testing.T) {
	github := func(p unit.ProviderID) bool { return p == "github" }
	if got := Verdicts(github); !slices.Equal(slices.Sorted(slices.Values(got)), []string{"APPROVE", "COMMENT", "REQUEST_CHANGES"}) {
		t.Fatalf("github's verdicts %v", got)
	}
	if got := Verdicts(func(p unit.ProviderID) bool { return p == "slack" }); len(got) != 0 {
		t.Fatalf("slack declares no verdict: %v", got)
	}
}
