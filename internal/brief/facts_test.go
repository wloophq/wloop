package brief

import (
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/domain/jsonschema"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
)

func TestBriefFactsReadTheExtrasAndWhatIsMissing(t *testing.T) {
	c := composed(t)
	none := Facts(nil, c)
	if none["brief.goal"] != "" || !slices.Equal(none["brief.missing"].([]string), []string{"approach", "solution"}) || none["brief.extra.approach"] != "" {
		t.Fatalf("no brief: %v", none)
	}
	b, err := Parse(`{"goal":"g","kind":"incident","risk":"high","tags":["domain:billing"],"extra":{"approach":"patch"},"extra_versions":{"approach":"` +
		c.FieldVersions()["approach"] + `"}}`)
	if err != nil {
		t.Fatal(err)
	}
	f := Facts(b, c)
	if f["brief.kind"] != "incident" || f["brief.extra.approach"] != "patch" || !slices.Equal(f["brief.missing"].([]string), []string{"solution"}) {
		t.Fatalf("%v", f)
	}
	s, _ := jsonschema.Compile([]byte(extraSchema), jsonschema.Options{Flat: true})
	env := builtin.WorkflowEnv("slack")
	for k, v := range builtin.ExtraFacts(s.Properties()) {
		env[k] = v
	}
	p, err := expr.CompileWorkflow(`brief.kind == "incident" && brief.extra.approach == "patch" && "solution" in brief.missing`, env)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := p.Match(workflow.Vars{Facts: f}); err != nil || !ok {
		t.Fatalf("the predicate over the facts: %v %v", ok, err)
	}
}
