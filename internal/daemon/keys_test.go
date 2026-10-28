package daemon

import (
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/keys"
)

func TestServedKeysPassTheCoreRule(t *testing.T) {
	t.Parallel()
	check := func(specs []api.ActionSpec, ctx keys.Context) {
		toggles := map[string]string{}
		for _, s := range specs {
			if s.Toggle != "" {
				toggles[s.ID] = s.Toggle
			}
		}
		claimed := map[keys.Surface]map[string]string{}
		for _, s := range specs {
			if s.Key == "" {
				continue
			}
			for _, sf := range keys.SurfacesOf(s.Surfaces) {
				if coreBinding(s.Key, s.Verb, ctx, sf) {
					continue
				}
				if claimed[sf] == nil {
					claimed[sf] = map[string]string{}
				}
				if err := keys.Check(sf, s.Key, s.ID, claimed[sf], toggles); err != nil {
					t.Errorf("%s context %s: %v", s.ID, ctx, err)
				}
				claimed[sf][s.Key] = s.ID
			}
		}
	}
	check(catalogOf(bundledActions()), keys.List)
	check(catalog.PlanSpecs(bundledActions()), keys.Plan)
	for _, s := range catalog.PlanSpecs(bundledActions()) {
		if s.ID == catalog.PlanApply && (s.Key != "Enter" || !coreBinding(s.Key, s.Verb, keys.Plan, keys.TUI)) {
			t.Errorf("plan apply: %q", s.Key)
		}
	}
	for _, s := range append(catalogOf(bundledActions()), catalog.PlanSpecs(bundledActions())...) {
		if len(s.Key) == 1 && s.Key[0] >= 'a' && s.Key[0] <= 'z' {
			t.Errorf("%s has the lowercase key %q", s.ID, s.Key)
		}
	}
}

// coreBinding tells a built-in that carries the core key of its own verb in ctx.
func coreBinding(key, verb string, ctx keys.Context, s keys.Surface) bool {
	return slices.ContainsFunc(keys.Core, func(b keys.Binding) bool {
		return b.Key == key && b.Action == verb && b.Context == ctx && slices.Contains(b.Surfaces, s)
	})
}

func TestKeysEndpoint(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	var got api.KeysResponse
	if code := get(t, s, api.Keys+"?surface=web", &got); code != 200 {
		t.Fatalf("%d", code)
	}
	summary := map[string]string{}
	for _, b := range got.Bindings {
		summary[b.Context+" "+b.Action] = b.Summary
	}
	for _, k := range []string{"list run_now", "list address", "plan plan_apply"} {
		if summary[k] == "" {
			t.Errorf("%s has no summary", k)
		}
	}
	if summary["list next"] == "" || summary["global nav:inbox"] != "" {
		t.Errorf("a verb's key says what the verb does, another core key nothing: %q, %q", summary["list next"], summary["global nav:inbox"])
	}
	has := func(v api.KeyView) bool {
		return slices.ContainsFunc(got.Bindings, func(b api.KeyView) bool {
			b.Summary = ""
			return b == v
		})
	}
	for _, want := range []api.KeyView{
		{Key: "x", Action: "next", Context: "list", Owner: "core", Help: "to Next"},
		{Key: "Cmd+K", Action: "palette", Context: "global", Owner: "core", Help: "every action and its key"},
		{Key: "R", Action: "run_now", Context: "list", Owner: "core", Help: "Run now"},
		{Key: "H", Action: "resume", Context: "list", Owner: "core", Help: "Resume"},
		{Key: "F", Action: "address", Context: "list", Owner: "intent:address", Help: "Address"},
		{Key: "Enter", Action: "plan_apply", Context: "plan", Owner: "core", Help: "Apply"},
	} {
		if !has(want) {
			t.Errorf("missing %+v in %+v", want, got.Bindings)
		}
	}
	for _, b := range got.Bindings {
		if b.Key == "Ctrl+K" || b.Key == "q" {
			t.Errorf("a TUI key on web: %+v", b)
		}
	}
	var tui api.KeysResponse
	get(t, s, api.Keys+"?surface=tui", &tui)
	if !slices.Contains(tui.Bindings, api.KeyView{Key: "q", Action: "quit", Context: "global", Owner: "core", Help: "quit"}) {
		t.Errorf("tui: %+v", tui.Bindings)
	}
	if code := get(t, s, api.Keys+"?surface=watch", nil); code != 400 {
		t.Errorf("an unknown surface: %d", code)
	}
}

func TestAViewsKeyIsTheOneItsCatalogServes(t *testing.T) {
	t.Parallel()
	s := newTasksRig(t, nil).s
	var cat api.ViewsCatalog
	if code := get(t, s, api.ViewsPath, &cat); code != 200 {
		t.Fatalf("%d", code)
	}
	var bound api.KeysResponse
	get(t, s, api.Keys+"?surface=tui", &bound)
	nav := map[string]api.KeyView{}
	for _, b := range bound.Bindings {
		if strings.HasPrefix(b.Action, "nav:") {
			nav[strings.TrimPrefix(b.Action, "nav:")] = b
		}
	}
	keyed := 0
	for _, v := range cat.Views {
		b, ok := nav[v.ID]
		if v.Key == "" {
			if ok {
				t.Errorf("view %s has no key but %s jumps to it", v.ID, b.Key)
			}
			continue
		}
		keyed++
		if b.Key != v.Key || b.Help != v.Label || b.Context != "global" {
			t.Errorf("view %s (%s, %q): bound %+v", v.ID, v.Key, v.Label, b)
		}
	}
	if keyed == 0 {
		t.Fatalf("no served view has a key: %+v", cat.Views)
	}
}
