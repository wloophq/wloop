package automation

import (
	"context"

	"github.com/wloophq/wloop/internal/domain/mark"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// unitMarks are u's marks; none without a Marks port.
func (e *Engine) unitMarks(ctx context.Context, u unit.Unit) (map[string]string, error) {
	if e.Marks == nil {
		return nil, nil
	}
	return e.Marks.Marks(ctx, u)
}

// marksOf are the marks an item of w records, from the facts it is planned
// with; a fact with no value a mark can hold records nothing.
func marksOf(w workflow.Workflow, facts workflow.Facts) map[string]string {
	var out map[string]string
	for name, fact := range w.Marks {
		v, ok := mark.Encode(facts[fact])
		if !ok {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[mark.Of(w.Name, name)] = v
	}
	return out
}
