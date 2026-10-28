package catalog

import (
	"fmt"
	"slices"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/keys"
)

// The scopes of an action spec: what the action acts on.
const (
	ScopeUnit  = "unit"
	ScopeItem  = "item"
	ScopeDraft = "draft"
	ScopeRun   = "run"
)

// githubBuiltins are the built-in actions only a pull request offers.
var githubBuiltins = map[string]bool{
	"take_over": true, "track": true, "untrack": true,
	"brief_refresh": true, "intake_without": true, "intake_cancel": true,
}

var draftVerbs = []string{"approve", "dismiss", "edit", "retry"}

// checkScope refuses a spec of an unknown scope, and a draft action with a
// key or without a draft verb: the core entry keys reach drafts.
func checkScope(s api.ActionSpec) error {
	switch s.Scope {
	case "", ScopeUnit, ScopeItem, ScopeRun:
		return nil
	case ScopeDraft:
		if s.Key != "" {
			return fmt.Errorf("%s: a draft action takes no key, has %s", s.ID, s.Key)
		}
		if !slices.Contains(draftVerbs, s.Verb) {
			return fmt.Errorf("%s: a draft action's verb is one of %v, has %q", s.ID, draftVerbs, s.Verb)
		}
		return nil
	}
	return fmt.Errorf("%s: unknown scope %q", s.ID, s.Scope)
}

// CheckPluginKeys refuses the plugin's declared keys that the core or a
// built-in action holds on the binding's surface, or that two of its
// actions share; a binding with no surface binds every surface. The error
// starts with the plugin id.
func CheckPluginKeys(id unit.ProviderID, ui *api.PluginUI) error {
	if ui == nil {
		return nil
	}
	if ui.Key != "" {
		if err := keys.CheckView(ui.Key); err != nil {
			return fmt.Errorf("%s: the key of its views: %w", id, err)
		}
	}
	claimed := map[keys.Surface]map[string]string{}
	toggles := map[string]string{}
	for _, b := range append(gateKeyActions(), builtinActions...) {
		if b.spec.Toggle != "" {
			toggles[b.spec.ID] = b.spec.Toggle
		}
		if b.spec.Key == "" {
			continue
		}
		for _, s := range keys.SurfacesOf(b.spec.Surfaces) {
			if claimed[s] == nil {
				claimed[s] = map[string]string{}
			}
			if _, ok := claimed[s][b.spec.Key]; !ok {
				claimed[s][b.spec.Key] = b.spec.ID
			}
		}
	}
	for _, kb := range ui.Keys {
		surfaces := []keys.Surface{keys.Web, keys.TUI, keys.Menubar}
		if kb.Surface != "" {
			surfaces = []keys.Surface{keys.Surface(kb.Surface)}
		}
		for _, s := range surfaces {
			if !slices.Contains([]keys.Surface{keys.Web, keys.TUI, keys.Menubar}, s) {
				return fmt.Errorf("%s: key %s of %s: unknown surface %s", id, kb.Key, kb.Action, s)
			}
			if err := keys.Check(s, kb.Key, kb.Action, claimed[s], toggles); err != nil {
				return fmt.Errorf("%s: key of %s: %w", id, kb.Action, err)
			}
			k, _ := keys.Canonical(kb.Key)
			if claimed[s] == nil {
				claimed[s] = map[string]string{}
			}
			if _, ok := claimed[s][k]; !ok {
				claimed[s][k] = kb.Action
			}
		}
	}
	return nil
}

// gateKeyActions are the keys builtin.GateKeys gives the row gates'
// intents, as the actions that hold them.
func gateKeyActions() []builtinAction {
	out := make([]builtinAction, len(builtin.GateKeys))
	for i, g := range builtin.GateKeys {
		out[i].spec = api.ActionSpec{ID: fmt.Sprintf("the %s gate's intent that %s", g.Kind, g.Role), Key: g.Key, Surfaces: everywhere()}
	}
	return out
}
