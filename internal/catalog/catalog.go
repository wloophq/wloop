package catalog

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

type memoEntry struct {
	loaded *intentload.Loaded
	specSet
	// without are the sets of SpecsFor, by their disabled key.
	without map[string]specSet
}

type specSet struct {
	specs   []api.ActionSpec
	version string
}

var (
	memoMu  sync.Mutex
	memo    memoEntry
	onBuild func()
)

func memoized(l *intentload.Loaded) specSet {
	memoMu.Lock()
	defer memoMu.Unlock()
	return built(l).specSet
}

// built is the memo of l; memoMu is held.
func built(l *intentload.Loaded) *memoEntry {
	if memo.loaded != l || l == nil {
		specs := buildSpecs(l)
		if onBuild != nil {
			onBuild()
		}
		memo = memoEntry{loaded: l, specSet: specSet{specs: specs, version: Version(specs)}, without: map[string]specSet{}}
	}
	return &memo
}

func memoizedFor(l *intentload.Loaded, disabled []unit.ProviderID) specSet {
	key := disabledKey(disabled)
	memoMu.Lock()
	defer memoMu.Unlock()
	m := built(l)
	if key == "" {
		return m.specSet
	}
	if set, ok := m.without[key]; ok {
		return set
	}
	off := strings.Split(key, "\x00")
	specs := slices.DeleteFunc(slices.Clone(m.specs), func(s api.ActionSpec) bool { return s.Plugin != "" && slices.Contains(off, s.Plugin) })
	set := m.specSet
	if len(specs) != len(m.specs) {
		set = specSet{specs: specs, version: Version(specs)}
	}
	m.without[key] = set
	return set
}

func disabledKey(disabled []unit.ProviderID) string {
	ids := make([]string, 0, len(disabled))
	for _, p := range disabled {
		ids = append(ids, string(p))
	}
	slices.Sort(ids)
	return strings.Join(slices.Compact(ids), "\x00")
}

// Specs are the specs in render order: the built-in start group, the
// intents of l, then the other built-ins. The slice is shared: callers
// must not change it.
func Specs(l *intentload.Loaded) []api.ActionSpec { return memoized(l).specs }

// VersionOf is Version of the specs of l.
func VersionOf(l *intentload.Loaded) string { return memoized(l).version }

// SpecsFor are Specs of l without the actions of the disabled plugins,
// memoized per l and disabled set. The slice is shared: callers must not
// change it.
func SpecsFor(l *intentload.Loaded, disabled []unit.ProviderID) []api.ActionSpec {
	return memoizedFor(l, disabled).specs
}

// VersionFor is Version of SpecsFor.
func VersionFor(l *intentload.Loaded, disabled []unit.ProviderID) string {
	return memoizedFor(l, disabled).version
}

func buildSpecs(l *intentload.Loaded) []api.ActionSpec {
	var out []api.ActionSpec
	i := 0
	for ; i < len(builtinActions) && builtinActions[i].spec.Group == builtin.GroupStart; i++ {
		out = append(out, withSource(worded(l, builtinActions[i].spec.ID, builtinActions[i].spec)))
	}
	out = append(out, registryActions{l}.Specs()...)
	for ; i < len(builtinActions); i++ {
		out = append(out, withSource(worded(l, builtinActions[i].spec.ID, builtinActions[i].spec)))
	}
	return out
}

func withSource(s api.ActionSpec) api.ActionSpec {
	s.Source = SourceBuiltin
	s.Scope = cmp.Or(s.Scope, ScopeUnit)
	if githubBuiltins[s.ID] {
		s.Plugin = string(pr.Codec{}.Provider())
	}
	return s
}

// Version names the specs, so a client refetches them when it changes.
func Version(specs []api.ActionSpec) string {
	raw, _ := json.Marshal(specs)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:6])
}

// States are every action the row offers now, in the order of the specs.
func States(f Facts) []api.ActionState {
	intents := map[string]api.ActionState{}
	for _, st := range (registryActions{f.Intents}).States(f) {
		intents[st.ID] = st
	}
	out := []api.ActionState{}
	var facts map[string]any
	for _, spec := range Specs(f.Intents) {
		st, ok := intents[spec.ID]
		if i := slices.IndexFunc(builtinActions, func(b builtinAction) bool { return b.spec.ID == spec.ID }); i >= 0 {
			if st, ok = builtinActions[i].offer(f); ok {
				if facts == nil {
					facts = intentload.ActionFactMap(f.Get)
				}
				st = texted(f.Intents, spec.ID, st, facts)
			}
		}
		if !ok {
			continue
		}
		joins := spec.Intake && st.Enabled && !strings.HasPrefix(st.Basis, BasisPending)
		if why := joinRefusal(f, spec.ID); joins && why != "" {
			st = api.ActionState{ID: st.ID, Reason: why}
		}
		out = append(out, st)
	}
	return out
}

// UnitStates are the intents' actions a row of a provider other than GitHub offers now, in the order of
// the specs: the built-in actions act on pull requests or on the loop's rounds, which such a row has none of.
func UnitStates(f Facts) []api.ActionState {
	out := []api.ActionState{}
	if f.Intents == nil {
		return out
	}
	return append(out, registryActions{f.Intents}.States(f)...)
}

// requestIntent is the intent an intake action asks for.
func requestIntent(f Facts, action string) string {
	if action != "run_now" {
		return action
	}
	name, _ := RunNowIntent(f.Intents.Registry, f.Workspace, f.Override, f.Mine)
	return name
}

// joinRefusal is why the open intake of the same intent refuses an intake
// action joining it, "" when it joins or there is none. An action the
// catalog serves carries no inputs.
func joinRefusal(f Facts, action string) string {
	var inputs map[string]any
	if action == f.Asked.Name {
		inputs = f.Asked.Inputs
	}
	intent := requestIntent(f, action)
	def, _ := f.Intents.Registry.Get(dintent.Name(intent))
	for _, i := range f.Intakes {
		if i.Intent != intent {
			continue
		}
		switch {
		case i.FromSession:
			return JoinRefusalText(i.Seq, i.Intent, "from an agent session")
		case i.Action != action:
			return JoinRefusalText(i.Seq, i.Intent, "as a "+i.Action)
		}
		if how := JoinDiffers(def, i.Inputs, inputs); how != "" {
			return JoinRefusalText(i.Seq, i.Intent, how)
		}
	}
	return ""
}

// JoinRefusalText is why open intake seq of intent refuses a request that
// differs from it as how says.
func JoinRefusalText(seq int64, intent, how string) string {
	return fmt.Sprintf("intake %d already asks for this pull request's %s %s; cancel it with "+brand.Command+" intake cancel %d", seq, intent, how, seq)
}

// JoinDiffers is how the open intake's inputs differ from a request's on
// the intent's join.differs_on, from the open intake's view: "with
// --self-review" or "without --self-review"; "" when they agree.
func JoinDiffers(d dintent.Def, open, req map[string]any) string {
	open, _ = d.NormalizeAll(open)
	req, _ = d.NormalizeAll(req)
	for _, name := range d.Join.DiffersOn {
		a, b := open[name], req[name]
		if dintent.Zero(a) && dintent.Zero(b) || reflect.DeepEqual(a, b) {
			continue
		}
		label := "inputs." + name
		if i := slices.IndexFunc(d.Inputs, func(x dintent.Input) bool { return x.Name == name }); i >= 0 {
			label = d.Inputs[i].Label()
		}
		if dintent.Zero(a) {
			return "without " + label
		}
		return "with " + label
	}
	return ""
}
