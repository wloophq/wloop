package builtin

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/mark"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
)

// The core rows, as the package declares them, before any Install.
var (
	coreFactRows      = slices.Clone(Facts)
	coreProviderSlots = coreSlots()
	coreBriefSubjects = slices.Clone(BriefSubjects)
	coreDeliveries    = slices.Clone(DraftDeliveries)
	coreTools         = slices.Clone(Tools)
	coreToolAnswers   = slices.Clone(ToolAnswers)
)

// factTypes are the expr types of plugin.toml's fact types.
var factTypes = map[string]expr.Type{
	"bool": expr.Bool, "int": expr.Int, "string": expr.String, "list": expr.List, "duration": expr.Duration, "timestamp": expr.Time,
}

// factSlots are the slots of plugin.toml's fact slots.
var factSlots = map[string]FactSlot{"action": ActionSlot, "turn": TurnSlot, "name": NameSlot}

// Vocabulary is the tables the core rows and some source plugins'
// vocabularies make. Install makes it the tables every reader reads.
type Vocabulary struct {
	facts, actionFacts []Fact
	effects            []Effect
	roles              []Role
	steps              []StepDef
	slots              map[unit.ProviderID]string
	briefSubjects      []string
	workflowSlots      []string
	deliveries         []string
	emojiDeliveries    []string
	pluginDeliveries   map[string]bool
	events             map[string]unit.ProviderID
	answering          map[unit.ProviderID]map[string]map[string]string
	payloads           map[string]unit.ProviderID
	rounds             []unit.ProviderID
	taskKinds          []string
	tools              []Value
	toolAnswers        []ToolAnswer
	toolProviders      map[string]unit.ProviderID
	draftKinds         *draft.Catalog
	languageFacts      map[unit.ProviderID]string
	membersFacts       map[unit.ProviderID]string
	marks              []Mark
	linkSignals        []string
	situations         map[unit.ProviderID][]string
	signalsOf          map[unit.ProviderID][]string
	predicates         map[unit.ProviderID][]manifest.Predicate
}

// DraftKinds are the merged plugins' draft kinds and deliveries; the host
// derives a draft's kind, delivery and payload schema through them.
var DraftKinds = &draft.Catalog{}

// Verdicts are the verdicts the draft kinds of the merged plugins serves takes declare, in their order, once
// each.
func Verdicts(serves func(unit.ProviderID) bool) []string {
	var out []string
	for _, k := range DraftKinds.Kinds() {
		if !serves(unit.ProviderID(k.Provider)) {
			continue
		}
		for _, v := range k.Verdicts {
			if !slices.Contains(out, v) {
				out = append(out, v)
			}
		}
	}
	return out
}

// DraftKinds are the draft kinds and deliveries of v's plugins.
func (v *Vocabulary) DraftKinds() *draft.Catalog { return v.draftKinds }

// draftSpecs are the draft kinds and deliveries src declares, as provider p's.
func draftSpecs(p unit.ProviderID, src *manifest.Source) ([]draft.KindSpec, []draft.DeliverySpec) {
	var ks []draft.KindSpec
	for _, k := range src.DraftKinds {
		spec := draft.KindSpec{Provider: string(p), Name: k.Name, Delivery: cmp.Or(k.Delivery, src.DefaultDelivery), Schema: k.Schema, Preview: k.Preview,
			Basis: k.Basis, TextRequired: k.TextRequired, Editable: slices.Clone(k.Editable), SchemaJSON: slices.Clone(k.SchemaJSON), Verdict: k.Verdict, Verdicts: slices.Clone(k.Verdicts)}
		for _, f := range k.Facts {
			spec.Facts = append(spec.Facts, draft.FactSpec{Name: f.Name, Pointer: f.Pointer})
		}
		ks = append(ks, spec)
	}
	var ds []draft.DeliverySpec
	for _, d := range src.Deliveries {
		ds = append(ds, draft.DeliverySpec{Provider: string(p), Name: d.Name, Posts: d.Posts})
	}
	return ks, ds
}

// LanguageFacts are, by provider, the fact that tells the language a unit's
// people write in, as a plugin's language_fact names it; a provider absent
// writes its drafts in the user's locale.
var LanguageFacts = map[unit.ProviderID]string{}

// MembersFacts are, by provider, the list fact that holds who can read a unit's conversation, as a plugin's
// members_fact names it; a provider absent never tells.
var MembersFacts = map[unit.ProviderID]string{}

// pluginDeliveries are the deliveries the merged plugins declare, each
// with whether a draft of it reaches people, as a manifest's posts says.
var pluginDeliveries = map[string]bool{}

// DeliveryPosts says whether a draft of delivery d reaches people: a
// delivery a merged plugin declares posts unless no plugin marks it
// posts, and one none declares always does.
func DeliveryPosts(d string) bool {
	posts, declared := pluginDeliveries[d]
	return posts || !declared
}

// ToolProviders are the merged plugins' tools, by name, to the plugin that
// declares each; a core tool is not one of them.
var ToolProviders = map[string]unit.ProviderID{}

// Merge is the vocabulary of the core rows and the [source] tables of
// srcs: their slots, facts, effects, roles, row kinds, deliveries, event
// kinds, payload root, brief subject, rounds and task kinds. A source that
// does not fit the host (a core provider or slot, a name another plugin or
// the core declares, a role naming no host mechanism) is left out, and the
// error names it; the vocabulary holds the others.
func Merge(srcs ...*manifest.Source) (*Vocabulary, error) {
	all := map[unit.ProviderID]*manifest.Source{}
	var errs []error
	for _, s := range srcs {
		p := unit.ProviderID(s.Provider)
		if _, dup := all[p]; dup {
			errs = append(errs, fmt.Errorf("plugin %s: found twice", p))
			continue
		}
		trial := maps.Clone(all)
		trial[p] = s
		if err := fits(p, s, trial); err != nil {
			errs = append(errs, fmt.Errorf("plugin %s: %w", p, err))
			continue
		}
		all = trial
	}
	return build(all), errors.Join(errs...)
}

// installed is the vocabulary Install last installed; nil for the core rows
// alone.
var installed *Vocabulary

// Installed is the vocabulary the tables hold, for a test to restore.
func Installed() *Vocabulary {
	if installed == nil {
		return coreVocabulary()
	}
	return installed
}

var coreVocabulary = sync.OnceValue(func() *Vocabulary { return build(nil) })

// Install makes v the tables. The composition root installs once, before
// the configuration and the profile load and before anything else reads the
// tables; a test that swaps the tables runs alone and restores them.
func Install(v *Vocabulary) {
	installed = v
	Facts, ActionFacts, Effects, Roles, Steps = v.facts, v.actionFacts, v.effects, v.roles, v.steps
	Slots, Providers, BriefSubjects = v.slots, slices.Sorted(maps.Keys(v.slots)), v.briefSubjects
	WorkflowSlots, DraftDeliveries, EventProviders, AnsweringEvents = v.workflowSlots, v.deliveries, v.events, v.answering
	DraftEmojiDeliveries = v.emojiDeliveries
	PayloadProviders, RoundProviders, PluginTaskKinds = v.payloads, v.rounds, v.taskKinds
	Tools, ToolAnswers, ToolProviders, DraftKinds = v.tools, v.toolAnswers, v.toolProviders, v.draftKinds
	LanguageFacts, MembersFacts = v.languageFacts, v.membersFacts
	pluginDeliveries = v.pluginDeliveries
	Marks = v.marks
	LinkSignals = v.linkSignals
	PluginSituations, PluginSignals = v.situations, v.signalsOf
	PluginPredicates = v.predicates
}

// fits says why v, provider p's vocabulary among all, cannot merge.
func fits(p unit.ProviderID, src *manifest.Source, all map[unit.ProviderID]*manifest.Source) error {
	v := src.SourceVocabulary
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if !unit.ValidProvider(string(p)) {
		bad("%q is not a provider id", p)
	}
	if _, core := coreProviderSlots[p]; core {
		bad("%s is a core provider", p)
	}
	if slices.Contains(slices.Collect(maps.Values(coreProviderSlots)), v.Slot) || slices.Contains(nonUnitSlots, v.Slot) {
		bad("slot %q is the core's", v.Slot)
	}
	for o, ov := range all {
		if o != p && ov.Slot == v.Slot {
			bad("slot %q is plugin %s's too", v.Slot, o)
		}
		if o != p && v.Payload != "" && ov.Payload == v.Payload {
			bad("payload %q is plugin %s's too", v.Payload, o)
		}
	}
	for _, m := range v.Marks {
		if err := mark.ValidName(m.Name); err != nil {
			bad("mark %q: %v", m.Name, err)
		}
		for o, ov := range all {
			if o != p && slices.ContainsFunc(ov.Marks, func(x manifest.Named) bool { return x.Name == m.Name }) {
				bad("mark %q is plugin %s's too", m.Name, o)
			}
		}
	}
	for _, f := range v.Facts {
		if _, ok := factTypes[f.Type]; !ok {
			bad("fact %s: type %q is not one the host reads", f.Name, f.Type)
		}
		for _, s := range f.Slots {
			if _, ok := factSlots[s]; !ok {
				bad("fact %s: slot %q is not one the host reads", f.Name, s)
			}
		}
		if slices.ContainsFunc(coreFactRows, func(c Fact) bool { return c.Name == f.Name }) {
			bad("fact %s is a core fact", f.Name)
		}
	}
	for _, k := range v.DraftKinds {
		for _, f := range k.Facts {
			if why := payloadFactName(p, k.Name, f.Name); why != "" {
				bad("%s", why)
			}
		}
	}
	for _, e := range v.Effects {
		if slices.ContainsFunc(coreEffects, func(c Effect) bool { return c.Name == e.Name }) {
			bad("effect %s is a core effect", e.Name)
		}
	}
	for _, r := range v.Roles {
		if slices.ContainsFunc(coreRoles, func(c Role) bool { return c.Name == r.Name }) {
			bad("role %s is a core role", r.Name)
		}
		for o, ov := range all {
			if o != p && slices.ContainsFunc(ov.Roles, func(c manifest.Role) bool { return c.Name == r.Name }) {
				bad("role %s is plugin %s's too", r.Name, o)
			}
		}
		for _, m := range []struct {
			key, name string
			host      []string
		}{
			{"round_kind", r.RoundKind, RoundKinds}, {"reconcile", r.Reconcile, Reconciles}, {"launch", r.Launch, Launches},
			{"workspace", r.Workspace, Names[SlotWorkspace]}, {"context", r.Context, Contexts}, {"policy", r.Policy, Policies},
		} {
			if m.name != "" && !slices.Contains(m.host, m.name) {
				bad("role %s: %s %q names no host mechanism, one of %s", r.Name, m.key, m.name, strings.Join(m.host, ", "))
			}
		}
		if action, ok := strings.CutPrefix(r.Prepare, PrepareAction); r.Prepare != "" && (!ok || action == "") {
			bad("role %s: prepare %q names no action of the plugin, %s<action>", r.Name, r.Prepare, PrepareAction)
		}
	}
	for _, k := range v.Events {
		if c, ok := coreEventProviders()[k.Name]; ok {
			bad("event %s is %s's", k.Name, c)
		}
		if strings.HasPrefix(k.Name, task.VerbEventKind("")) {
			bad("event %s is the kind of a task verb's event", k.Name)
		}
		for o, ov := range all {
			if o != p && slices.ContainsFunc(ov.Events, func(c manifest.Event) bool { return c.Name == k.Name }) {
				bad("event %s is plugin %s's too", k.Name, o)
			}
		}
	}
	for _, name := range v.Steps {
		i := slices.IndexFunc(coreSteps, func(s StepDef) bool { return s.Name == name })
		if i < 0 || coreSteps[i].Providers == nil && !slices.Contains(PluginSteps, name) {
			bad("step %s is no core step that names the providers it runs on", name)
		}
	}
	for _, t := range src.Tools {
		if slices.ContainsFunc(coreTools, func(c Value) bool { return c.Name == t.Name }) {
			bad("tool %s is a core tool", t.Name)
		}
		for o, os := range all {
			if o != p && slices.ContainsFunc(os.Tools, func(c manifest.Tool) bool { return c.Name == t.Name }) {
				bad("tool %s is plugin %s's too", t.Name, o)
			}
		}
	}
	if ks, ds := draftSpecs(p, src); len(ks) > 0 {
		if _, err := draft.NewCatalog(ks, ds, nil); err != nil {
			bad("%v", err)
		}
	}
	for _, k := range src.ConfigReads {
		if !slices.ContainsFunc(configGrammar, func(r Key) bool {
			return r.Path == k || strings.HasPrefix(r.Path, k+".") || strings.HasPrefix(r.Path, k+"[]")
		}) {
			bad("config_reads %q is no configuration key", k)
		}
	}
	return errors.Join(errs...)
}

// build is the vocabulary of the core rows and all, in provider order.
func build(all map[unit.ProviderID]*manifest.Source) *Vocabulary {
	ids := slices.Sorted(maps.Keys(all))
	facts := slices.Clone(coreFactRows)
	actionFacts := slices.Clone(coreActionFacts)
	effects := slices.Clone(coreEffects)
	roles := slices.Clone(coreRoles)
	slots := coreSlots()
	briefSubjects := slices.Clone(coreBriefSubjects)
	unitSlots := slices.Clone(coreUnitSlots)
	deliveries := slices.Clone(coreDeliveries)
	pluginDelivs := map[string]bool{}
	var emojis []string
	events := coreEventProviders()
	answering := map[unit.ProviderID]map[string]map[string]string{}
	payloads := map[string]unit.ProviderID{}
	var rounds []unit.ProviderID
	var taskKinds []string
	rowKinds := map[unit.ProviderID][]string{}
	steps := map[string][]unit.ProviderID{}
	tools, answers := slices.Clone(coreTools), slices.Clone(coreToolAnswers)
	toolProviders := map[string]unit.ProviderID{}
	var kindSpecs []draft.KindSpec
	var deliverySpecs []draft.DeliverySpec
	defaultDeliveries := map[string]string{}
	languageFacts, membersFacts := map[unit.ProviderID]string{}, map[unit.ProviderID]string{}
	var marks []Mark
	var signals []string
	situations, signalsOf := map[unit.ProviderID][]string{}, map[unit.ProviderID][]string{}
	predicates := map[unit.ProviderID][]manifest.Predicate{}
	for _, id := range ids {
		src := all[id]
		v := src.SourceVocabulary
		for _, name := range v.Steps {
			steps[name] = append(steps[name], id)
		}
		for _, t := range src.Tools {
			tools = append(tools, Value{Name: t.Name, Doc: t.Doc})
			toolProviders[t.Name] = id
			answers = append(answers, answersOf([]string{t.Name})...)
			for _, a := range t.Answers {
				ta := ToolAnswer{Tool: t.Name, Name: a.Name, Doc: a.Doc}
				for _, f := range a.Fields {
					ta.Fields = append(ta.Fields, AnswerField{Name: f.Name, Number: f.Number})
				}
				answers = append(answers, ta)
			}
		}
		ks, ds := draftSpecs(id, src)
		kindSpecs, deliverySpecs = append(kindSpecs, ks...), append(deliverySpecs, ds...)
		if v.DefaultDelivery != "" {
			defaultDeliveries[string(id)] = v.DefaultDelivery
		}
		facts = append(facts, payloadFacts(id, v.DraftKinds)...)
		if v.LanguageFact != "" {
			languageFacts[id] = v.LanguageFact
		}
		if v.MembersFact != "" {
			membersFacts[id] = v.MembersFact
		}
		for _, m := range v.Marks {
			marks = append(marks, Mark{Name: m.Name, Doc: m.Doc, Provider: string(id)})
		}
		slots[id] = v.Slot
		unitSlots = append(unitSlots, v.Slot)
		if v.BriefSubject {
			briefSubjects = append(briefSubjects, v.Slot)
		}
		if v.Rounds {
			rounds = append(rounds, id)
		}
		if v.Payload != "" {
			payloads[v.Payload] = id
		}
		for _, k := range v.Events {
			events[k.Name] = id
			if k.AnswersAskedTurn {
				if answering[id] == nil {
					answering[id] = map[string]map[string]string{}
				}
				answering[id][k.Name] = maps.Clone(k.Verdicts)
			}
		}
		for _, f := range v.Facts {
			wf, intent, ok := factRows(id, v.Slot, f)
			if ok {
				facts = append(facts, intent)
				if slices.Equal(intent.Slots, []FactSlot{ActionSlot}) {
					actionFacts = append(actionFacts, intent)
				}
			}
			facts = append(facts, wf)
		}
		for _, e := range v.Effects {
			effects = append(effects, effectRow(id, e))
		}
		for _, r := range v.Roles {
			roles = append(roles, roleRow(id, r))
		}
		for _, d := range v.Deliveries {
			if !slices.Contains(deliveries, d.Name) {
				deliveries = append(deliveries, d.Name)
			}
			pluginDelivs[d.Name] = pluginDelivs[d.Name] || d.Posts
			if d.Emoji && !slices.Contains(emojis, d.Name) {
				emojis = append(emojis, d.Name)
			}
		}
		if len(v.RowKinds) > 0 {
			rowKinds[id] = slices.Clone(v.RowKinds)
		}
		for _, k := range v.Kinds {
			if !slices.Contains(taskKinds, k) {
				taskKinds = append(taskKinds, k)
			}
		}
		if len(v.Predicates) > 0 {
			predicates[id] = slices.Clone(v.Predicates)
		}
		for _, sit := range v.Situations {
			situations[id] = append(situations[id], sit.Name)
		}
		for _, g := range v.Signals {
			signalsOf[id] = append(signalsOf[id], g.Name)
			if !slices.Contains(signals, g.Name) {
				signals = append(signals, g.Name)
				facts = append(facts, linkSignalFact(g))
			}
		}
	}
	for _, rows := range [][]Fact{facts, actionFacts} {
		for i, f := range rows {
			switch {
			case f.Slot == BriefSlot:
				rows[i].Subjects = briefSubjects
			case slices.Contains(UnitDraftFacts, f.Name):
				rows[i].Subjects = slices.Clone(unitSlots[len(coreUnitSlots):])
			case f.Name == RowKindFact && len(rowKinds) > 0:
				rows[i].Enum = rowKinds
			case f.Name == DraftDelivery:
				rows[i].Enum = enumOf(deliveries...)
			}
		}
	}
	// fits refused every plugin whose kinds the catalog refuses.
	draftKinds, _ := draft.NewCatalog(kindSpecs, deliverySpecs, defaultDeliveries)
	return &Vocabulary{facts: facts, actionFacts: actionFacts, effects: effects, roles: roles, steps: stepsWith(steps),
		slots: slots, briefSubjects: briefSubjects, workflowSlots: slices.Concat(unitSlots, nonUnitSlots),
		deliveries: deliveries, emojiDeliveries: emojis, pluginDeliveries: pluginDelivs, events: events, answering: answering, payloads: payloads, rounds: rounds, taskKinds: taskKinds,
		tools: tools, toolAnswers: answers, toolProviders: toolProviders, draftKinds: draftKinds, languageFacts: languageFacts, membersFacts: membersFacts, marks: marks, linkSignals: signals,
		situations: situations, signalsOf: signalsOf, predicates: predicates}
}

// PluginPredicates are the predicates each merged plugin's profile layer sets in predicates.toml, by
// provider.
var PluginPredicates = map[unit.ProviderID][]manifest.Predicate{}

// PredicateEnv is the env of plugin p's predicate name: the facts its manifest says p hands with the
// question; false when p declares no such predicate.
func PredicateEnv(p unit.ProviderID, name string) (expr.Env, bool) {
	i := slices.IndexFunc(PluginPredicates[p], func(x manifest.Predicate) bool { return x.Name == name })
	if i < 0 {
		return nil, false
	}
	env := expr.Env{}
	for _, f := range Facts {
		if f.Provider == p && slices.Contains(f.Slots, WorkflowSlot) && slices.Contains(PluginPredicates[p][i].Facts, f.Name) {
			put(env, f)
		}
	}
	Narrow(env, []unit.ProviderID{p})
	return env, true
}

// LinkSignals are the link signals the merged plugins declare, each read as
// the Bool fact task.link_signal.<name>.
var LinkSignals []string

// linkSignalFact is the task fact of a declared link signal: on when it is
// on for any of the task's links.
func linkSignalFact(g manifest.Named) Fact {
	f := wf("task", strings.TrimPrefix(LinkSignalPrefix, "task.")+g.Name, expr.Bool, g.Doc+" On when it is on for any unit the task links.")
	f.Untrusted = true
	return f
}

// PluginSituations are the situations each merged plugin derives, by provider, in its manifest's order.
var PluginSituations = map[unit.ProviderID][]string{}

// PluginSignals are the link signals each merged plugin reports, by provider.
var PluginSignals = map[unit.ProviderID][]string{}

// PluginTaskKinds are the values of the task's kind property the merged
// plugins' units give their tasks, which tasks.toml's kind takes besides
// its own.
var PluginTaskKinds []string

// factRows are the rows of plugin p's fact f: its workflow fact under slot,
// and, when f is read in other slots, its intent fact.
func factRows(p unit.ProviderID, slot string, f manifest.Fact) (wf, intent Fact, ok bool) {
	wf = Fact{Name: f.Name, Type: factTypes[f.Type], Slots: []FactSlot{WorkflowSlot}, Slot: slot, Doc: f.Doc, Provider: p,
		Untrusted: f.Untrusted, TimeVarying: f.TimeVarying}
	if len(f.Enum) > 0 {
		wf.Enum = map[unit.ProviderID][]string{p: slices.Clone(f.Enum)}
	}
	if len(f.Slots) == 0 {
		return wf, Fact{}, false
	}
	intent = Fact{Name: f.Name, Type: wf.Type, Doc: f.Doc, Provider: p, Dim: f.Dim, Enum: wf.Enum, Untrusted: f.Untrusted,
		TimeVarying: f.TimeVarying}
	for _, s := range f.Slots {
		intent.Slots = append(intent.Slots, factSlots[s])
	}
	return wf, intent, true
}

func effectRow(p unit.ProviderID, e manifest.Effect) Effect {
	var a Applies
	for _, on := range e.Applies {
		switch on {
		case "turn":
			a |= AppliesTurn
		case "draft":
			a |= AppliesDraft
		}
	}
	return Effect{Name: e.Name, Provider: p, Applies: a, Doc: e.Doc, Plugin: true, Silent: e.Silent}
}

func roleRow(p unit.ProviderID, r manifest.Role) Role {
	out := Role{Name: r.Name, Doc: r.Doc, Provider: p, RoundKind: r.RoundKind, Reconcile: r.Reconcile, Prepare: r.Prepare,
		Launch: r.Launch, Workspace: r.Workspace, Context: r.Context, Policy: r.Policy, DrivesRow: r.DrivesRow}
	if out.Context == "" {
		out.Context = NoRole.Context
	}
	if out.Policy == "" {
		out.Policy = NoRole.Policy
	}
	return out
}

// stepsWith are the core steps, each also on the units of the plugins
// that list it; a step of PluginSteps no plugin lists is left out.
func stepsWith(listed map[string][]unit.ProviderID) []StepDef {
	var out []StepDef
	for _, s := range coreSteps {
		if s.Providers != nil || slices.Contains(PluginSteps, s.Name) {
			s.Providers = slices.Concat(s.Providers, listed[s.Name])
		}
		if len(s.Providers) == 0 && slices.Contains(PluginSteps, s.Name) {
			continue
		}
		out = append(out, s)
	}
	return out
}
