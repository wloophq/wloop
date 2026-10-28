package catalog

import (
	"context"
	"fmt"
	"maps"

	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
)

// Authority is the profile's word on who runs the task verbs, the core's
// tasks.Authority: actor a may run verb on t where tasks.toml's
// verbs.<verb>.by.<a> holds over t's own facts, and running it opens the
// verb's gate where that is "gate". An actor the verb's by leaves out may
// not run it. loaded is the profile as it loads now.
func Authority(loaded func() *intentload.Loaded) func(ctx context.Context, verb string, a task.Actor, t task.Task) (allow, gate bool, err error) {
	return func(_ context.Context, verb string, a task.Actor, t task.Task) (bool, bool, error) {
		l := loaded()
		v, ok := l.Tasks.Verbs[verb]
		if !ok {
			return false, false, nil
		}
		src, ok := v.By[string(a)]
		switch {
		case !ok:
			return false, false, nil
		case src == "gate":
			return false, true, nil
		}
		pred, ok := v.Allow[string(a)]
		if !ok {
			return false, false, fmt.Errorf("verbs.%s.by.%s did not compile", verb, a)
		}
		allow, err := pred.Match(workflow.Vars{Facts: TaskFacts(t, l.Tasks)})
		return allow, false, err
	}
}

// TaskFacts are the facts of t its own fields hold, its properties typed
// as d declares them; the facts only the task facts provider reads are
// absent, and read as their zero.
func TaskFacts(t task.Task, d intentload.TasksDef) workflow.Facts {
	f := OfferFacts(t)
	f["task.category"] = string(t.Category)
	maps.Copy(f, PropFacts(t, d))
	return f
}

// PropFacts are the task.prop.* facts of t's properties, typed as d
// declares them; a property d does not declare is left out.
func PropFacts(t task.Task, d intentload.TasksDef) workflow.Facts {
	f := workflow.Facts{}
	for _, p := range d.Props {
		v, ok := t.Props[p.Name]
		if !ok {
			continue
		}
		f["task.prop."+p.Name] = propFact(p.Type, v)
	}
	return f
}

func propFact(typ task.PropType, v task.Value) any {
	switch typ {
	case task.PropInt:
		return int(v.Int)
	case task.PropDate:
		return v.Time
	case task.PropDuration:
		return v.Dur
	case task.PropStrings:
		return v.List
	case task.PropActor:
		if len(v.Actors) > 0 {
			return v.Actors[0].Key
		}
		return ""
	case task.PropActors:
		out := make([]string, len(v.Actors))
		for i, p := range v.Actors {
			out[i] = p.Key
		}
		return out
	}
	return v.Text
}
