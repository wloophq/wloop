package builtin

import (
	"go/parser"
	"go/token"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// providerRowExceptions are the core rows that still key a value by a
// plugin's id, each with why the manifest cannot hold it yet.
var providerRowExceptions = map[string]string{
	"brief.kind": "GitHub's brief taxonomy closes the fact on its units; plugin.toml has no key for a plugin's brief kinds while the host's queue briefs pull requests",
}

// pluginDomainExceptions are the plugin domains builtin still imports, each
// with why and the task that removes the import.
var pluginDomainExceptions = map[string]string{}

// modulePath is the module, whose packages the imports name.
var modulePath = reflect.TypeFor[Fact]().PkgPath()[:strings.Index(reflect.TypeFor[Fact]().PkgPath(), "/internal/")]

// pluginDomains are the packages of a plugin's domain, which builtin never
// imports.
var pluginDomains = []string{"internal/domain/slack", "internal/domain/pr", "internal/domain/github", "internal/domain/inventory",
	"internal/github", "internal/slack", "internal/adapter/", "internal/domain/round"}

func TestBuiltinHasNoProviderRows(t *testing.T) {
	core := Installed()
	Install(build(nil))
	t.Cleanup(func() { Install(core) })

	coreProvider := func(p unit.ProviderID) bool { _, ok := coreSlots()[p]; return ok || p == "" }
	coreSlot := func(s string) bool {
		return s == "" || slices.Contains(coreUnitSlots, s) || slices.Contains(nonUnitSlots, s)
	}
	for _, f := range Facts {
		if !coreProvider(f.Provider) || !coreSlot(f.Slot) {
			t.Errorf("fact %s is of provider %q, slot %q", f.Name, f.Provider, f.Slot)
		}
		for p := range f.Enum {
			if !coreProvider(p) && providerRowExceptions[f.Name] == "" {
				t.Errorf("fact %s closes its values on %s", f.Name, p)
			}
		}
		for _, s := range f.Subjects {
			if !coreSlot(s) {
				t.Errorf("fact %s is read under slot %s", f.Name, s)
			}
		}
	}
	for _, e := range Effects {
		if !coreProvider(e.Provider) {
			t.Errorf("effect %s is of %s", e.Name, e.Provider)
		}
	}
	for _, r := range Roles {
		if !coreProvider(r.Provider) {
			t.Errorf("role %s is of %s", r.Name, r.Provider)
		}
	}
	for _, s := range Steps {
		for _, p := range s.Providers {
			if !coreProvider(p) {
				t.Errorf("step %s acts on %s", s.Name, p)
			}
		}
	}
	if !maps.Equal(Slots, coreSlots()) || len(RoundProviders) > 0 || len(PayloadProviders) > 0 || len(PluginTaskKinds) > 0 {
		t.Errorf("slots %v, rounds %v, payloads %v, task kinds %v", Slots, RoundProviders, PayloadProviders, PluginTaskKinds)
	}
	for k, p := range EventProviders {
		if !coreProvider(p) {
			t.Errorf("event %s is of %s", k, p)
		}
	}
	if !slices.Equal(BriefSubjects, []string{TaskSlot}) || !slices.Equal(DraftDeliveries, []string{"message", "reaction"}) {
		t.Errorf("brief subjects %v, deliveries %v", BriefSubjects, DraftDeliveries)
	}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			for _, d := range pluginDomains {
				if strings.Contains(path, "/"+d) && strings.HasPrefix(path, modulePath) && pluginDomainExceptions[d] == "" {
					t.Errorf("%s imports %s, a plugin's domain", name, path)
				}
			}
		}
	}
}
