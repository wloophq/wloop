package tui

import (
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/api"
)

// stepText is a plan step in the catalog's words the daemon names, filled with its arguments as a person reads
// them; the step's name when the catalog has no words for it.
func (m *Model) stepText(st api.PlanStep) string {
	if st.Says == "" {
		return st.Use
	}
	w := m.w()
	a := map[string]any{}
	for k, v := range st.Args {
		a[k] = v
	}
	if r := st.Args["resolution_says"]; r != "" {
		a["resolution"] = w.T(r, nil)
	}
	a["intent"] = firstOf(st.Args["intent_label"], st.Args["intent"])
	a["task"] = st.Args["task_title"]
	a["verb"] = m.verbLabel(st.Args["verb"])
	prop, value := st.Args["prop"], st.Args["value"]
	if i := slices.IndexFunc(m.viewsCat.Facets, func(f api.FacetSpec) bool { return prop != "" && f.Fact == "task.prop."+prop && f.Provider == "" }); i >= 0 {
		f := m.viewsCat.Facets[i]
		a["prop"] = f.Label
		if l, ok := f.Labels[value]; ok {
			value = l
		}
	}
	a["value"] = value
	return w.T(st.Says, a)
}

// planPosts says whether applying a plan item may post where others see it, as the daemon says of each step.
func planPosts(does []api.PlanStep) bool {
	return slices.ContainsFunc(does, func(st api.PlanStep) bool { return st.Posts })
}

// planReason is why the plan holds it, in the catalog's words: the engine's template filled with its values, a
// wait's facts and a refusal through the catalog; any other reason through the catalog, as it reads when the
// catalog has no words for it.
func (m *Model) planReason(it api.PlanItem) string {
	w := m.w()
	if it.ReasonKey == "" {
		return w.T(it.Reason, nil)
	}
	params := map[string]any{}
	for k, v := range it.ReasonParams {
		params[k] = v
	}
	if len(it.ReasonFacts) > 0 {
		facts := make([]string, len(it.ReasonFacts))
		for i, f := range it.ReasonFacts {
			facts[i] = f.Name + " = " + f.Value
		}
		params["facts"] = strings.Join(facts, ", ")
	}
	if r := it.ReasonParams["refusal"]; r != "" {
		params["refusal"] = w.T(r, nil)
	}
	return w.T(it.ReasonKey, params)
}

func firstOf(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}
