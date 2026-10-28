package intent

import (
	"maps"
	"slices"

	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
)

// TasksFile is the profile file that words the verbs a task offers.
const TasksFile = "tasks.toml"

// VerbText is a verb's words: Input is the question a verb that asks for
// a value puts first. Offer, when set, is where a task offers the verb.
type VerbText struct {
	Label, Summary, Input string
	Offer                 workflow.Predicate
}

type mTasks struct {
	Verbs map[string]mVerbText `toml:"verbs"`
}

type mVerbText struct {
	Label   *string `toml:"label"`
	Summary *string `toml:"summary"`
	Input   *string `toml:"input"`
	Offer   *string `toml:"offer"`
}

// mergeTasks lays the verbs of file over out.
func mergeTasks(out ListTexts, file string, src []byte) Problems {
	m, problems, _, complete := decode[mTasks](file, src)
	if !complete {
		return problems
	}
	s := &sheet{file: file, at: positions(src), problems: problems}
	for _, id := range slices.Sorted(maps.Keys(m.Verbs)) {
		if !slices.Contains(builtin.TaskVerbs, id) {
			s.add("verbs."+id, "names no verb%s", expr.DidYouMean(id, builtin.TaskVerbs))
			continue
		}
		w, t := out.Verbs[id], m.Verbs[id]
		text(s, "verbs."+id+".label", t.Label, &w.Label)
		text(s, "verbs."+id+".summary", t.Summary, &w.Summary)
		text(s, "verbs."+id+".input", t.Input, &w.Input)
		if t.Offer != nil && id == string(task.VerbSeen) {
			s.add("verbs.seen.offer", "seen takes no offer: clients send it when a task opens")
		} else if t.Offer != nil {
			if p := s.predicate("verbs."+id+".offer", *t.Offer, builtin.OfferEnv()); p != nil {
				w.Offer = p
			}
		}
		if w.Label == "" && t.Label == nil {
			s.add("verbs."+id+".label", "is required")
		}
		if w.Summary == "" && t.Summary == nil {
			s.add("verbs."+id+".summary", "is required")
		}
		out.Verbs[id] = w
	}
	s.addGrammar(grammar(builtin.SurfaceTasks, src))
	return s.problems
}

// missingVerbs are the verbs file words none of, or words without the
// question they ask first.
func missingVerbs(out ListTexts) Problems {
	var problems Problems
	for _, v := range builtin.TaskVerbs {
		if _, ok := out.Verbs[v]; !ok {
			problems = append(problems, Problem{File: TasksFile, Field: "verbs." + v, Msg: "is missing: every verb has a table"})
		}
	}
	for _, v := range builtin.TaskVerbInputs {
		if w, ok := out.Verbs[v]; ok && w.Input == "" {
			problems = append(problems, Problem{File: TasksFile, Field: "verbs." + v + ".input", Msg: "is required: the verb asks for a value first"})
		}
	}
	return problems
}
