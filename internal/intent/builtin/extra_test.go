package builtin

import (
	"maps"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/domain/jsonschema"
	"github.com/wloophq/wloop/internal/intent/expr"
)

func TestExtraFactsAreTypedFromTheSchema(t *testing.T) {
	s, err := jsonschema.Compile([]byte(`{"type":"object","properties":{
		"solution":{"type":"string"},
		"approach":{"type":"string","enum":["patch","rewrite"]},
		"files":{"type":"integer"},
		"breaking":{"type":"boolean"},
		"areas":{"type":"array","items":{"type":"string","enum":["api","ui"]}},
		"score":{"type":"number"}}}`), jsonschema.Options{Flat: true})
	if err != nil {
		t.Fatal(err)
	}
	want := expr.Env{"brief.extra.solution": expr.String, "brief.extra.approach": expr.String, "brief.extra.files": expr.Int,
		"brief.extra.breaking": expr.Bool, "brief.extra.areas": expr.List}
	got := ExtraFacts(s.Properties())
	for _, name := range got.Names() {
		if got[name] != want[name] {
			t.Errorf("%s: %v, want %v", name, got[name], want[name])
		}
	}
	if len(got.Names()) != len(want) {
		t.Fatalf("%v", got.Names())
	}
	if e := got.Enum("brief.extra.approach"); !slices.Equal(e, []string{"patch", "rewrite"}) {
		t.Errorf("approach's values %v", e)
	}
	if _, err := expr.Compile(`brief.extra.approach == "patches"`, got); err == nil {
		t.Error("a value the schema does not list compiles")
	}
	env := WorkflowEnv("pr")
	maps.Copy(env, ExtraFacts(s.Properties()))
	if _, err := expr.CompileWorkflow(`brief.extra.approach == "patch" && "api" in brief.extra.areas`, env); err != nil {
		t.Fatalf("a predicate over declared extras: %v", err)
	}
	if _, err := expr.CompileWorkflow(`brief.extra.owner == "ana"`, env); err == nil {
		t.Fatal("a predicate over an undeclared extra compiled")
	}
}
