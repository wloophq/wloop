package brief

import (
	"encoding/json"

	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// Parse reads a stored brief, of any provider; empty reads none.
func Parse(stored string) (*Brief, error) {
	if stored == "" {
		return nil, nil
	}
	var b Brief
	if err := json.Unmarshal([]byte(stored), &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// Facts are the brief.* facts of b under c, the brief intent's composed
// schema: empty values, and every extra field missing, when the unit has no
// brief. Extra fields b holds at an older version still read as their value
// and are listed in brief.missing.
func Facts(b *Brief, c Composed) workflow.Facts {
	f := workflow.Facts{"brief.goal": "", "brief.risk": "", "brief.kind": "", "brief.size": "", "brief.tags": []string{},
		"brief.missing": c.Missing(Brief{})}
	if c.Extra != nil {
		for _, p := range c.Extra.Properties() {
			f[builtin.ExtraPrefix+p.Name] = zeroOf(p.Type)
		}
	}
	if b == nil {
		return f
	}
	tags := b.Tags
	if tags == nil {
		tags = []string{}
	}
	f["brief.goal"], f["brief.risk"], f["brief.kind"], f["brief.size"], f["brief.tags"] = b.Goal, b.Risk, b.Kind, b.Size, tags
	f["brief.missing"] = c.Missing(*b)
	for name, v := range b.Extra {
		if _, declared := f[builtin.ExtraPrefix+name]; declared {
			f[builtin.ExtraPrefix+name] = factValue(v)
		}
	}
	return f
}

func zeroOf(t string) any {
	switch t {
	case "integer":
		return int64(0)
	case "boolean":
		return false
	case "array":
		return []string{}
	}
	return ""
}

// factValue is a decoded JSON value as a fact holds it: whole numbers as
// int64, lists as strings.
func factValue(v any) any {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return v
}
