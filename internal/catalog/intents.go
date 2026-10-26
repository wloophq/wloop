package catalog

import (
	"bytes"
	"cmp"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// registryActions are the intents' actions as their manifests declare them;
// a user intent that failed to load keeps a disabled action.
type registryActions struct{ l *intentload.Loaded }

// offering are the intents with an action, by the order of their workspace
// in the builtin vocabulary, then by name.
func (r registryActions) offering() []dintent.Def {
	var out []dintent.Def
	for _, d := range r.l.Registry.All() {
		if d.Action != nil {
			out = append(out, d)
		}
	}
	order := builtin.Names[builtin.SlotWorkspace]
	slices.SortStableFunc(out, func(a, b dintent.Def) int {
		return cmp.Or(cmp.Compare(slices.Index(order, a.Workspace), slices.Index(order, b.Workspace)), strings.Compare(string(a.Name), string(b.Name)))
	})
	return out
}

func (r registryActions) broken() []dintent.Name {
	var out []dintent.Name
	for n := range r.l.Broken {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

func manifestPath(d dintent.Def) string {
	if d.Source.Dir == "" || d.Bundled && !slices.Contains(d.Source.Overrides, "intent.toml") {
		return path.Join("intents", string(d.Name), "intent.toml")
	}
	return filepath.Join(d.Source.Dir, "intent.toml")
}

// brokenManifest is the intent.toml of a user intent that failed to load,
// from where its problems are.
func brokenManifest(p intentload.Problems) string {
	if len(p) == 0 {
		return ""
	}
	dir := p[0].File
	if b := filepath.Base(dir); b == "intent.toml" || b == "instructions.md.tmpl" {
		dir = filepath.Dir(dir)
	}
	return filepath.Join(dir, "intent.toml")
}

// FirstProblem is the first line of the problems, sorted.
func FirstProblem(p intentload.Problems) string {
	first, _, _ := strings.Cut(p.Error(), "\n")
	return first
}

func (r registryActions) Specs() []api.ActionSpec {
	var out []api.ActionSpec
	for _, d := range r.offering() {
		a := d.Action
		out = append(out, api.ActionSpec{ID: string(d.Name), Label: d.Label, Group: a.Group, Key: d.Key, Verb: d.Verb, Surfaces: slices.Clone(a.Surfaces),
			Input:  &api.InputSpec{Kind: "text", Label: d.Guidance.Label, Placeholder: d.Guidance.Help, Max: d.Guidance.Max},
			Intake: d.Intake, Source: manifestPath(d), Scope: intentScope(d), Plugin: pluginOf(d)})
	}
	for _, n := range r.broken() {
		out = append(out, api.ActionSpec{ID: string(n), Label: string(n), Group: "round", Surfaces: []string{SurfaceRow, SurfaceDetail, SurfaceTUI, SurfaceCLI, SurfaceExplore},
			Source: brokenManifest(r.l.Broken[n]), Scope: ScopeUnit})
	}
	return out
}

// pluginOf is the plugin whose units d acts on, "" for every provider's.
func pluginOf(d dintent.Def) string {
	if p := d.ProviderOrDefault(); p != dintent.AnyProvider {
		return string(p)
	}
	return ""
}

func (r registryActions) States(f Facts) []api.ActionState {
	var out []api.ActionState
	facts := intentload.ActionFactMap(f.Get)
	provider := cmp.Or(f.Row.Unit, f.Ref.Unit()).Provider
	for _, d := range r.offering() {
		if !d.Serves(provider) {
			continue
		}
		p := r.l.Programs[d.Name]
		if p.Visible != nil && !p.Visible.Eval(f) {
			continue
		}
		id := string(d.Name)
		if p.Enabled != nil && !p.Enabled.Eval(f) {
			out = append(out, api.ActionState{ID: id, Reason: actionText(d.Name, p, "reason_off", facts)})
			continue
		}
		st := api.ActionState{ID: id, Enabled: true, Note: actionText(d.Name, p, "hint", facts), Confirm: actionText(d.Name, p, "confirm", facts)}
		st.Primary = p.Primary != nil && p.Primary.Eval(f)
		recommend(&st, d, f.Recommended)
		if f.Question {
			st.Warning = d.Action.Warning
		}
		out = append(out, st)
	}
	if !f.Open {
		return out
	}
	for _, n := range r.broken() {
		out = append(out, api.ActionState{ID: string(n), Reason: "the profile has errors: " + FirstProblem(r.l.Broken[n])})
	}
	return out
}

// actionText renders an action text; one that fails to render says so, so
// a refusal never reads empty.
func actionText(intent dintent.Name, p intentload.Programs, name string, facts map[string]any) string {
	t := p.ActionTexts[name]
	if t == nil {
		return ""
	}
	var b bytes.Buffer
	if err := t.Execute(&b, facts); err != nil {
		return fmt.Sprintf("the profile has errors: %s: action.%s: %v", intent, name, err)
	}
	return b.String()
}
