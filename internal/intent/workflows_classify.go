package intent

import (
	"encoding/json"
	"maps"

	dunit "github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
)

// classifyEnvs are the classify outputs of units' workflows as facts, by provider: each top-level property of a
// classify step's schema whose type a fact can hold, untrusted. A workflow that does not decode adds none; its
// own load says why.
func classifyEnvs(units []*wUnit) map[dunit.ProviderID]expr.Env {
	out := map[dunit.ProviderID]expr.Env{}
	for _, u := range units {
		src, err := u.files.read("workflow.toml")
		if err != nil {
			continue
		}
		m, _, _, complete := decode[wManifest](u.files.path("workflow.toml"), src)
		if !complete || m.Provider == nil {
			continue
		}
		p := dunit.ProviderID(*m.Provider)
		for _, st := range m.Step {
			if st["use"] != builtin.StepClassify {
				continue
			}
			name, _ := st[schemaArg].(string)
			if name == "" {
				r, _ := builtin.StepArg(builtin.StepClassify, schemaArg)
				name = r.Default
			}
			b, err := u.files.read(name)
			if err != nil {
				continue
			}
			env := schemaFacts(u.name, b)
			if out[p] == nil {
				out[p] = expr.Env{}
			}
			maps.Copy(out[p], env)
		}
	}
	return out
}

// schemaFacts are the facts workflow's classify answers to schema set.
func schemaFacts(workflow string, schema []byte) expr.Env {
	var s struct {
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
	}
	env := expr.Env{}
	if json.Unmarshal(schema, &s) != nil {
		return env
	}
	types := map[string]expr.Type{"string": expr.String, "boolean": expr.Bool, "integer": expr.Int, "array": expr.List}
	for field, p := range s.Properties {
		t, ok := types[p.Type]
		if !ok {
			continue
		}
		name := builtin.ClassifyFact(workflow, field)
		env[name] = t
		env.SetUntrusted(name)
	}
	return env
}
