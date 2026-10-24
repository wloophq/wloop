package catalog

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func TestCatalogUnchangedButStamps(t *testing.T) {
	specs := Specs(bundled(t))
	stripped := make([]api.ActionSpec, len(specs))
	for i, s := range specs {
		s.Scope, s.Verb, s.Plugin = "", "", ""
		stripped[i] = s
	}
	got, err := json.MarshalIndent(stripped, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "actions.golden.json")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("the served specs differ from the golden beyond scope, verb and plugin:\n%s", got)
	}
}

func specByID(specs []api.ActionSpec, id string) (api.ActionSpec, bool) {
	for _, s := range specs {
		if s.ID == id {
			return s, true
		}
	}
	return api.ActionSpec{}, false
}

func TestCatalogStampsPluginAndVerb(t *testing.T) {
	specs := Specs(bundled(t))
	for _, id := range []string{"review", "address"} {
		s, ok := specByID(specs, id)
		if !ok {
			t.Fatalf("%s is not served", id)
		}
		if s.Plugin != string(unit.GitHub) || s.Scope != ScopeUnit || s.Verb != "delegate" {
			t.Errorf("%s: plugin %q scope %q verb %q, want github unit delegate", id, s.Plugin, s.Scope, s.Verb)
		}
	}
	for _, s := range specs {
		if s.Scope == "" {
			t.Errorf("%s has no scope", s.ID)
		}
	}
	if s, _ := specByID(specs, "pause"); s.Plugin != "" {
		t.Errorf("hold is stamped %q, want core", s.Plugin)
	}
	if s, _ := specByID(specs, "take_over"); s.Plugin != string(unit.GitHub) {
		t.Errorf("take_over is stamped %q, want github", s.Plugin)
	}
}

func TestCatalogOmitsDisabledPlugin(t *testing.T) {
	l := bundled(t)
	off := SpecsFor(l, []unit.ProviderID{unit.GitHub})
	for _, s := range off {
		if s.Plugin == string(unit.GitHub) {
			t.Errorf("%s of the disabled github plugin is served", s.ID)
		}
	}
	if _, ok := specByID(off, "pause"); !ok {
		t.Error("the core action pause is gone with github disabled")
	}
	if VersionFor(l, []unit.ProviderID{unit.GitHub}) == VersionOf(l) {
		t.Error("the version of the disabled set equals the enabled one")
	}
	if VersionFor(l, []unit.ProviderID{unit.GitHub}) != Version(off) {
		t.Error("VersionFor differs from the version of the specs it names")
	}
	n := countBuilds(t)
	again := SpecsFor(l, []unit.ProviderID{"thread", unit.GitHub, "thread"})
	Specs(l)
	same := SpecsFor(l, []unit.ProviderID{unit.GitHub, "thread"})
	if &again[0] != &same[0] || &again[0] != &SpecsFor(l, []unit.ProviderID{unit.GitHub, "thread"})[0] {
		t.Error("SpecsFor of the same disabled set is not the memoized slice")
	}
	if *n != 0 {
		t.Errorf("specs built %d times on a warm Loaded", *n)
	}
	all := SpecsFor(l, nil)
	if &all[0] != &Specs(l)[0] {
		t.Error("SpecsFor with nothing disabled is not Specs")
	}
}

func TestPluginKeyCollisionRefused(t *testing.T) {
	cases := []struct {
		name    string
		binding api.KeyBinding
		want    []string
	}{
		{"core key on web", api.KeyBinding{Key: "x", Action: "pick", Surface: "web"}, []string{"github", "x", "web"}},
		{"chord prefix on tui", api.KeyBinding{Key: "g", Action: "pick", Surface: "tui"}, []string{"github", "g", "tui"}},
		{"built-in action key", api.KeyBinding{Key: "H", Action: "pick", Surface: "menubar"}, []string{"github", "H", "menubar", "pause"}},
		{"unknown surface", api.KeyBinding{Key: "Q", Action: "pick", Surface: "watch"}, []string{"github", "Q", "watch"}},
		{"every surface", api.KeyBinding{Key: "Enter", Action: "pick"}, []string{"github", "Enter", "web"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CheckPluginKeys(unit.GitHub, &api.PluginUI{Keys: []api.KeyBinding{c.binding}})
			if err == nil {
				t.Fatal("the key loads")
			}
			for _, w := range c.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("%q does not name %q", err, w)
				}
			}
			if !strings.HasPrefix(err.Error(), "github: ") {
				t.Errorf("%q does not start with the plugin id", err)
			}
		})
	}
	ok := &api.PluginUI{Keys: []api.KeyBinding{{Key: "Q", Action: "pick", Surface: "web"}, {Key: "Q", Action: "pick", Surface: "tui"}, {Key: "H", Action: "pause", Surface: "tui"}}}
	if err := CheckPluginKeys(unit.GitHub, ok); err != nil {
		t.Errorf("free keys refused: %v", err)
	}
	clash := &api.PluginUI{Keys: []api.KeyBinding{{Key: "Q", Action: "pick", Surface: "web"}, {Key: "Q", Action: "other", Surface: "web"}}}
	if err := CheckPluginKeys(unit.GitHub, clash); err == nil {
		t.Error("two actions of the plugin share Q on web")
	}
	if err := CheckPluginKeys(unit.GitHub, nil); err != nil {
		t.Errorf("no UI refused: %v", err)
	}
}

func TestDraftActionHasNoKey(t *testing.T) {
	saved := builtinActions
	reset := func() { memoMu.Lock(); memo = memoEntry{}; memoMu.Unlock() }
	t.Cleanup(func() { builtinActions = saved; reset() })
	reset()
	draft := api.ActionSpec{ID: "draft.approve", Label: "Approve", Group: "draft", Scope: ScopeDraft, Verb: "approve", Surfaces: []string{SurfaceRow}}
	builtinActions = append(slices.Clone(saved), builtinAction{draft, func(Facts) (api.ActionState, bool) { return absent() }})
	l := bundled(t)
	got, ok := specByID(Specs(l), "draft.approve")
	if !ok || got.Scope != ScopeDraft || got.Key != "" || got.Verb != "approve" {
		t.Fatalf("draft spec served as %+v", got)
	}
	for _, s := range Specs(l) {
		if err := checkScope(s); err != nil {
			t.Errorf("%s: %v", s.ID, err)
		}
	}
	keyed := draft
	keyed.Key = "Q"
	if err := checkScope(keyed); err == nil {
		t.Error("a draft action with a key passes")
	}
	noVerb := draft
	noVerb.Verb = "next"
	if err := checkScope(noVerb); err == nil {
		t.Error("a draft action without a gtd draft verb passes")
	}
	if err := checkScope(api.ActionSpec{ID: "x", Scope: "thread"}); err == nil {
		t.Error("an unknown scope passes")
	}
}
