package builtin

import (
	"maps"
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
	if got := ExtraFacts(s.Properties()); !maps.Equal(got, want) {
		t.Fatalf("%v", got)
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
