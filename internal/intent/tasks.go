package intent

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/domain/duration"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/internal/profile"
)

// TasksFile is the profile file that declares the task properties and
// states and words the verbs a task offers.
const TasksFile = "tasks.toml"

// VerbText is a verb's words: Input is the question a verb that asks for
// a value puts first. Offer, when set, is where a task offers the verb.
// From are the states it moves a task from, every state when empty; To the
// state it moves it to, "" for none. By are who may run it, by actor: a
// predicate's source over the task's facts, or "gate"; an actor left out
// may not. Gate is the gate a "gate" opens, Handler what it runs besides
// its move ("" for none), Send how a reply's handler sends. Allow are By's
// predicates compiled, by actor; a "gate" has none.
type VerbText struct {
	Label, Summary, Input string
	Offer                 workflow.Predicate
	From                  []string
	To                    string
	By                    map[string]string
	Allow                 map[string]workflow.Predicate
	Gate, Handler, Send   string
}

// TasksDef is the profile's tasks.toml over every layer. Props and States
// keep their declared order: the bundled entries in the order the bundled
// file declares them, then each user layer's new entries in its own order;
// a layer that sets an entry declared below it changes it in place.
//
// A zero task.Value unsets a property, so an int or a duration property
// holds no 0: 0 means unset, and Parse refuses it.
type TasksDef struct {
	Props  []PropDef
	States []StateDef
	Verbs  map[string]VerbText
	// OwedBy is owed_by.expr as written; Owed is it compiled over OwedByEnv, nil when the profile has none.
	OwedBy string
	Owed   *expr.Program
	// ChatIntent is the intent of the turn a chat message starts on a task with no live session.
	ChatIntent       string
	Areas, Contexts  []string
	StaleWaitingDays int
}

// PropDef is a task property: the fact task.prop.<Name>. Values are an
// enum's, at least one.
type PropDef struct {
	Name    string
	Type    task.PropType
	Values  []string
	Writers []task.Writer
	Doc     string
}

// Untrusted says whether an agent or a plugin may write p, so no approval
// rule that releases reads it.
func (p PropDef) Untrusted() bool {
	return slices.ContainsFunc(p.Writers, func(w task.Writer) bool { return !w.Trusted() })
}

// StateDef is a state a task takes and its category, which is all Go reads
// of it.
type StateDef struct {
	Name     string
	Category task.Category
	Label    string
}

// Prop is the property name declares.
func (d TasksDef) Prop(name string) (PropDef, bool) {
	i := slices.IndexFunc(d.Props, func(p PropDef) bool { return p.Name == name })
	if i < 0 {
		return PropDef{}, false
	}
	return d.Props[i], true
}

// CategoryOf is the category of state; false when the profile declares no
// such state.
func (d TasksDef) CategoryOf(state string) (task.Category, bool) {
	i := slices.IndexFunc(d.States, func(s StateDef) bool { return s.Name == state })
	if i < 0 {
		return "", false
	}
	return d.States[i].Category, true
}

// FirstOf is the first state of category c in the profile's order, which a
// task proposed in c takes; "" when no state is of c.
func (d TasksDef) FirstOf(c task.Category) string {
	for _, s := range d.States {
		if s.Category == c {
			return s.Name
		}
	}
	return ""
}

// Move is the states verb moves a task from (every state when empty) and
// the state it moves it to ("" for none); false when no verb is named so.
func (d TasksDef) Move(verb string) ([]string, string, bool) {
	v, ok := d.Verbs[verb]
	return v.From, v.To, ok
}

// Handler is the handler verb runs besides its move, "" for none.
func (d TasksDef) Handler(verb string) string { return d.Verbs[verb].Handler }

// VerbKinds are the kinds of the events of the profile's verbs, in name
// order.
func (d TasksDef) VerbKinds() []string {
	return task.VerbEventKinds(slices.Sorted(maps.Keys(d.Verbs)))
}

// HeldProblems are the problems of the states stored tasks hold, held, that
// no layer declares: their tasks keep the category they had, and no verb
// moves them.
func (d TasksDef) HeldProblems(held []string) Problems {
	var out Problems
	for _, name := range held {
		if _, ok := d.CategoryOf(name); ok {
			continue
		}
		out = append(out, Problem{File: TasksFile, Field: "states." + name,
			Msg: fmt.Sprintf("stored tasks hold the state %s, which no layer declares%s; declare it again, or move those tasks to another state first",
				name, expr.DidYouMean(name, d.stateNames()))})
	}
	return out
}

func (d TasksDef) stateNames() []string {
	out := make([]string, len(d.States))
	for i, s := range d.States {
		out[i] = s.Name
	}
	return out
}

// Untrusted says whether no approval rule that releases may read the
// property name: an agent or a plugin writes it, or the profile declares
// no such property.
func (d TasksDef) Untrusted(name string) bool {
	p, ok := d.Prop(name)
	return !ok || p.Untrusted()
}

// Facts are the task.prop.<name> facts of the declared properties, typed
// from their types, an enum's closed to its values and an untrusted one
// marked so, with task.prop_by.<name>, who last wrote each; and
// task.state closed to the declared states.
func (d TasksDef) Facts() expr.Env {
	env := expr.Env{}
	for _, p := range d.Props {
		name := builtin.PropPrefix + p.Name
		env[name] = builtin.PropFactType(p.Type)
		env[builtin.PropByPrefix+p.Name] = expr.String
		env.SetEnum(builtin.PropByPrefix+p.Name, append([]string{""}, writerNames()...))
		if p.Type == task.PropEnum {
			env.SetEnum(name, p.Values)
		}
		if p.Untrusted() {
			env.SetUntrusted(name)
		}
	}
	states := make([]string, len(d.States))
	for i, s := range d.States {
		states[i] = s.Name
	}
	env.SetEnum(taskStateFact, states)
	return env
}

const taskStateFact = "task.state"

// errZeroUnsets refuses a 0 for an int or a duration property.
var errZeroUnsets = errors.New("0 unsets the property: an int or a duration property holds no 0")

// Parse reads text as the value of the property name, by its type: a
// string as is; an enum one of its values; a date YYYY-MM-DD (midnight in
// loc) or RFC 3339; a duration as the profile writes them (90m, 2d); an
// int in decimal; an actor a person's key, provider:id; a list type its
// items comma-separated. Empty text is the zero Value, which unsets the
// property, and so is 0: Parse refuses 0 for an int or a duration.
func (d TasksDef) Parse(name, text string, loc *time.Location) (task.Value, error) {
	p, ok := d.Prop(name)
	if !ok {
		return task.Value{}, fmt.Errorf("%s names no property of %s%s", name, TasksFile, expr.DidYouMean(name, d.propNames()))
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return task.Value{}, nil
	}
	switch p.Type {
	case task.PropEnum:
		if !slices.Contains(p.Values, text) {
			return task.Value{}, fmt.Errorf("%s is %q, not one of %s", name, text, strings.Join(p.Values, ", "))
		}
		return task.Value{Text: text}, nil
	case task.PropInt:
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return task.Value{}, fmt.Errorf("%s is an int, not %q", name, text)
		}
		if n == 0 {
			return task.Value{}, fmt.Errorf("%s: %w", name, errZeroUnsets)
		}
		return task.Value{Int: n}, nil
	case task.PropDate:
		if t, err := time.ParseInLocation(time.DateOnly, text, loc); err == nil {
			return task.Value{Time: t}, nil
		}
		t, err := time.Parse(time.RFC3339, text)
		if err != nil {
			return task.Value{}, fmt.Errorf("%s is a date, YYYY-MM-DD or RFC 3339, not %q", name, text)
		}
		return task.Value{Time: t}, nil
	case task.PropDuration:
		if zeroDuration(text) {
			return task.Value{}, fmt.Errorf("%s: %w", name, errZeroUnsets)
		}
		v, err := duration.Parse(text)
		if err != nil {
			return task.Value{}, fmt.Errorf("%s is a duration such as 90m or 2d, not %q", name, text)
		}
		return task.Value{Dur: v}, nil
	case task.PropActor:
		a, err := actorOf(name, text)
		if err != nil {
			return task.Value{}, err
		}
		return task.Value{Actors: []items.Person{a}}, nil
	case task.PropActors:
		var out []items.Person
		for _, k := range splitList(text) {
			a, err := actorOf(name, k)
			if err != nil {
				return task.Value{}, err
			}
			out = append(out, a)
		}
		return task.Value{Actors: out}, nil
	case task.PropStrings:
		return task.Value{List: splitList(text)}, nil
	}
	return task.Value{Text: text}, nil
}

// zeroDuration says whether text is a duration of 0, which duration.Parse
// refuses for not being positive.
func zeroDuration(text string) bool {
	if n, ok := strings.CutSuffix(text, "d"); ok {
		v, err := strconv.ParseFloat(n, 64)
		return err == nil && v == 0
	}
	if v, err := strconv.ParseFloat(text, 64); err == nil {
		return v == 0
	}
	v, err := time.ParseDuration(text)
	return err == nil && v == 0
}

func (d TasksDef) propNames() []string {
	out := make([]string, len(d.Props))
	for i, p := range d.Props {
		out[i] = p.Name
	}
	return out
}

func actorOf(name, key string) (items.Person, error) {
	provider, id, ok := strings.Cut(key, ":")
	if !ok || provider == "" || id == "" || strings.ContainsAny(key, " \t") {
		return items.Person{}, fmt.Errorf("%s is a person's key, provider:id such as slack:U0123, not %q", name, key)
	}
	return items.Person{Key: key}, nil
}

func splitList(text string) []string {
	var out []string
	for _, s := range strings.Split(text, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

type mTasks struct {
	Verbs            map[string]mVerbText `toml:"verbs"`
	Props            map[string]mProp     `toml:"props"`
	States           map[string]mState    `toml:"states"`
	OwedBy           *mOwedBy             `toml:"owed_by"`
	Chat             *mChat               `toml:"chat"`
	Areas            []string             `toml:"areas"`
	Contexts         []string             `toml:"contexts"`
	StaleWaitingDays *int                 `toml:"stale_waiting_days"`
}

type mVerbText struct {
	Label   *string           `toml:"label"`
	Summary *string           `toml:"summary"`
	Input   *string           `toml:"input"`
	Offer   *string           `toml:"offer"`
	From    []string          `toml:"from"`
	To      *string           `toml:"to"`
	By      map[string]string `toml:"by"`
	Gate    *string           `toml:"gate"`
	Handler *string           `toml:"handler"`
	Send    *string           `toml:"send"`
}

type mProp struct {
	Type    *string  `toml:"type"`
	Values  []string `toml:"values"`
	Writers []string `toml:"writers"`
	Doc     *string  `toml:"doc"`
}

type mState struct {
	Category *string `toml:"category"`
	Label    *string `toml:"label"`
}

type mOwedBy struct {
	Expr *string `toml:"expr"`
}

type mChat struct {
	Intent *string `toml:"intent"`
}

// ref is a name a file's key holds, checked once every layer is read.
type ref struct {
	s          *sheet
	path, name string
}

// tasksLoad is tasks.toml on its way to a TasksDef over the layers. The
// checks that need every layer's properties, states or gates wait in
// states, gates and preds, by path.
type tasksLoad struct {
	def    TasksDef
	states map[string]ref
	gates  map[string]ref
	preds  map[string]ref
	owed   *ref
}

// BundledTasks is the bundled profile's tasks.toml alone.
func BundledTasks(bundled fs.FS) (TasksDef, Problems) {
	return loadTasks(nil, bundled, nil)
}

// loadTasks reads tasks.toml from bundled and the user's layers: a higher
// layer's key replaces a lower one's. A verb's to and from name declared
// states, its by keys actors, its gate one gates declares (nil gates checks
// none), and its by predicates compile over the task's facts.
func loadTasks(layers []profile.Layer, bundled fs.FS, gates *GatesDef) (TasksDef, Problems) {
	l := &tasksLoad{def: TasksDef{Verbs: map[string]VerbText{}}, states: map[string]ref{}, gates: map[string]ref{}, preds: map[string]ref{}}
	problems, read := readLayered(layers, bundled, TasksFile, l.merge)
	if !read {
		return l.def, problems
	}
	problems = append(problems, missingVerbs(l.def.Verbs)...)
	l.def.withPluginKinds()
	return l.def, append(problems, l.check(gates)...)
}

// withPluginKinds adds to the kind property, when an enum, the task kinds
// the plugins' manifests give their units' tasks.
func (d *TasksDef) withPluginKinds() {
	i := slices.IndexFunc(d.Props, func(p PropDef) bool { return p.Name == task.PropKind && p.Type == task.PropEnum })
	if i < 0 {
		return
	}
	for _, k := range builtin.PluginTaskKinds {
		if !slices.Contains(d.Props[i].Values, k) {
			d.Props[i].Values = append(slices.Clip(d.Props[i].Values), k)
		}
	}
}

// merge lays file over the definition so far.
func (l *tasksLoad) merge(file string, src []byte) Problems {
	m, problems, _, complete := decode[mTasks](file, src)
	if !complete {
		return problems
	}
	s := &sheet{file: file, at: positions(src), problems: problems}
	l.mergeProps(s, m.Props)
	l.mergeStates(s, m.States)
	l.mergeVerbs(s, m.Verbs)
	if m.OwedBy != nil {
		text(s, "owed_by.expr", m.OwedBy.Expr, &l.def.OwedBy)
		l.owed = &ref{s, "owed_by.expr", l.def.OwedBy}
	}
	if m.Chat != nil {
		text(s, "chat.intent", m.Chat.Intent, &l.def.ChatIntent)
	}
	if m.Areas != nil {
		l.def.Areas = slices.Clone(m.Areas)
	}
	if m.Contexts != nil {
		l.def.Contexts = slices.Clone(m.Contexts)
	}
	if n := m.StaleWaitingDays; n != nil {
		if *n < 0 {
			s.add("stale_waiting_days", "must be 0 or more")
		} else {
			l.def.StaleWaitingDays = *n
		}
	}
	s.addGrammar(grammar(builtin.SurfaceTasks, src))
	return s.problems
}

func (l *tasksLoad) mergeProps(s *sheet, props map[string]mProp) {
	for _, name := range declared(s, "props", props) {
		path, m := "props."+name, props[name]
		i := slices.IndexFunc(l.def.Props, func(p PropDef) bool { return p.Name == name })
		p := PropDef{Name: name}
		if i >= 0 {
			p = l.def.Props[i]
		}
		n := len(s.problems)
		switch {
		case m.Type != nil:
			s.oneOf(path+".type", *m.Type, builtin.ValuesOf(builtin.SurfaceTasks, "props.*.type"))
			p.Type = task.PropType(*m.Type)
		case i < 0:
			s.add(path+".type", "is required: one of %s", joinTypes())
		}
		if m.Values != nil {
			p.Values = slices.Clone(m.Values)
		}
		switch {
		case len(p.Values) > 0 && p.Type != task.PropEnum:
			s.add(path+".values", "is read only with type enum, not %s", p.Type)
		case len(p.Values) == 0 && p.Type == task.PropEnum:
			s.add(path+".values", "is required with type enum: the values it takes; a property that takes any text is a string")
		}
		if m.Writers != nil {
			s.eachOneOf(path+".writers", m.Writers, builtin.ValuesOf(builtin.SurfaceTasks, "props.*.writers"))
			p.Writers = make([]task.Writer, len(m.Writers))
			for j, w := range m.Writers {
				p.Writers[j] = task.Writer(w)
			}
		}
		text(s, path+".doc", m.Doc, &p.Doc)
		switch {
		case len(s.problems) > n:
		case i < 0:
			l.def.Props = append(l.def.Props, p)
		default:
			l.def.Props[i] = p
		}
	}
}

func joinTypes() string {
	out := make([]string, len(task.PropTypes))
	for i, t := range task.PropTypes {
		out[i] = string(t)
	}
	return strings.Join(out, ", ")
}

func (l *tasksLoad) mergeStates(s *sheet, states map[string]mState) {
	for _, name := range declared(s, "states", states) {
		path, m := "states."+name, states[name]
		i := slices.IndexFunc(l.def.States, func(st StateDef) bool { return st.Name == name })
		st := StateDef{Name: name}
		if i >= 0 {
			st = l.def.States[i]
		}
		n := len(s.problems)
		switch {
		case m.Category != nil:
			s.oneOf(path+".category", *m.Category, builtin.ValuesOf(builtin.SurfaceTasks, "states.*.category"))
			st.Category = task.Category(*m.Category)
		case i < 0:
			s.add(path+".category", "is required: the category Go reads of the state")
		}
		text(s, path+".label", m.Label, &st.Label)
		switch {
		case len(s.problems) > n:
		case i < 0:
			l.def.States = append(l.def.States, st)
		default:
			l.def.States[i] = st
		}
	}
}

func (l *tasksLoad) mergeVerbs(s *sheet, verbs map[string]mVerbText) {
	for _, id := range slices.Sorted(maps.Keys(verbs)) {
		if !slices.Contains(builtin.TaskVerbs, id) {
			s.add("verbs."+id, "names no verb%s", expr.DidYouMean(id, builtin.TaskVerbs))
			continue
		}
		path := "verbs." + id
		w, t := l.def.Verbs[id], verbs[id]
		text(s, path+".label", t.Label, &w.Label)
		text(s, path+".summary", t.Summary, &w.Summary)
		text(s, path+".input", t.Input, &w.Input)
		if t.Offer != nil && id == string(task.VerbSeen) {
			s.add("verbs.seen.offer", "seen takes no offer: clients send it when a task opens")
		} else if t.Offer != nil {
			if p := s.predicate(path+".offer", *t.Offer, builtin.OfferEnv()); p != nil {
				w.Offer = p
			}
		}
		l.verbStates(s, path, t)
		if t.From != nil {
			w.From = slices.Clone(t.From)
		}
		if t.To != nil {
			w.To = *t.To
		}
		l.verbActors(s, path, t.By)
		if t.By != nil {
			w.By = maps.Clone(w.By)
			if w.By == nil {
				w.By = map[string]string{}
			}
			maps.Copy(w.By, t.By)
		}
		if t.Gate != nil {
			l.gates[path] = ref{s, path + ".gate", *t.Gate}
			w.Gate = *t.Gate
		}
		text(s, path+".handler", t.Handler, &w.Handler)
		text(s, path+".send", t.Send, &w.Send)
		if w.Gate == "" && slices.Contains(slices.Collect(maps.Values(w.By)), "gate") {
			s.add(path+".gate", "is required: a by.<actor> of \"gate\" opens the gate it names")
		}
		if w.Label == "" && t.Label == nil {
			s.add(path+".label", "is required")
		}
		if w.Summary == "" && t.Summary == nil {
			s.add(path+".summary", "is required")
		}
		l.def.Verbs[id] = w
	}
}

// verbStates waits to check the states the verb at path moves from and to.
func (l *tasksLoad) verbStates(s *sheet, path string, t mVerbText) {
	if t.To != nil {
		l.states[path+".to"] = ref{s, path + ".to", *t.To}
	}
	if t.From == nil {
		return
	}
	maps.DeleteFunc(l.states, func(k string, _ ref) bool { return strings.HasPrefix(k, path+".from[") })
	for i, f := range t.From {
		at := fmt.Sprintf("%s.from[%d]", path, i)
		l.states[at] = ref{s, at, f}
	}
}

// verbActors checks the actors of the verb at path's by, and waits to
// compile their predicates.
func (l *tasksLoad) verbActors(s *sheet, path string, by map[string]string) {
	names := make([]string, len(task.Actors))
	for i, a := range task.Actors {
		names[i] = string(a)
	}
	for _, actor := range slices.Sorted(maps.Keys(by)) {
		at := path + ".by." + actor
		switch {
		case !slices.Contains(names, actor):
			s.add(at, "names no actor%s; the actors are %s", expr.DidYouMean(actor, names), strings.Join(names, ", "))
		case by[actor] == "gate":
			delete(l.preds, at)
		default:
			l.preds[at] = ref{s, at, by[actor]}
		}
	}
}

// check is what needs every layer read: the states a verb names, the gate
// it opens and its by predicates, over the task's facts.
func (l *tasksLoad) check(gates *GatesDef) Problems {
	var out Problems
	states := make([]string, len(l.def.States))
	for i, s := range l.def.States {
		states[i] = s.Name
	}
	for _, at := range slices.Sorted(maps.Keys(l.states)) {
		r := l.states[at]
		if !slices.Contains(states, r.name) {
			out = append(out, r.s.problem(r.path, "names no state %s%s; the states are %s", r.name, expr.DidYouMean(r.name, states), strings.Join(states, ", ")))
		}
	}
	if gates != nil {
		names := gates.names()
		for _, at := range slices.Sorted(maps.Keys(l.gates)) {
			r := l.gates[at]
			if !slices.Contains(names, r.name) {
				out = append(out, r.s.problem(r.path, "names no gate of %s%s", GatesFile, expr.DidYouMean(r.name, names)))
			}
		}
	}
	if r := l.owed; r != nil && strings.TrimSpace(r.name) != "" {
		prog, err := expr.CompileString(r.name, OwedByEnv(l.def))
		if err != nil {
			msg, snippet := exprMsg(err)
			p := r.s.problem(r.path, "%s", msg)
			p.Snippet = snippet
			out = append(out, p)
		}
		l.def.Owed = prog
	}
	env := ByEnv(l.def)
	for _, at := range slices.Sorted(maps.Keys(l.preds)) {
		r := l.preds[at]
		pred, err := expr.CompileWorkflow(r.name, env)
		if err != nil {
			msg, snippet := exprMsg(err)
			p := r.s.problem(r.path, "%s", msg)
			p.Snippet = snippet
			out = append(out, p)
			continue
		}
		verb, actor, _ := strings.Cut(strings.TrimPrefix(at, "verbs."), ".by.")
		v := l.def.Verbs[verb]
		v.Allow = maps.Clone(v.Allow)
		if v.Allow == nil {
			v.Allow = map[string]workflow.Predicate{}
		}
		v.Allow[actor] = pred
		l.def.Verbs[verb] = v
	}
	return out
}

// ByFacts are the facts a verb's by reads besides the task.prop.* of
// tasks.toml: the task's own, which the authority reads off the task with
// no facts provider at hand.
var ByFacts = []string{"task.id", "task.state", "task.category", "task.unit", "task.unit_provider"}

// OwedFacts are the task facts owed_by.expr may not read: those its value
// makes.
var OwedFacts = []string{"task.owed_by", "task.owed_since", "task.check_on"}

// OwedByEnv is the env owed_by.expr compiles over: the task's workflow
// facts but OwedFacts, and d's properties.
func OwedByEnv(d TasksDef) expr.Env {
	env := builtin.WorkflowEnv(builtin.TaskSlot)
	for _, name := range OwedFacts {
		delete(env, name)
	}
	maps.Copy(env, d.Facts())
	return env
}

// ByEnv is the env a verb's by compiles over: ByFacts and d's properties.
func ByEnv(d TasksDef) expr.Env {
	all := builtin.WorkflowEnv(builtin.TaskSlot)
	env := expr.Env{}
	for _, name := range ByFacts {
		env[name] = all[name]
	}
	maps.Copy(env, d.Facts())
	return env
}

// declared is the keys of the map table section of s's file in their
// source order; a key the file does not place comes last, by name.
func declared[V any](s *sheet, section string, m map[string]V) []string {
	keys := slices.Sorted(maps.Keys(m))
	slices.SortStableFunc(keys, func(a, b string) int {
		pa, oka := s.at[section+"."+a]
		pb, okb := s.at[section+"."+b]
		switch {
		case oka && okb && pa.Line != pb.Line:
			return pa.Line - pb.Line
		case oka && okb:
			return pa.Col - pb.Col
		case oka:
			return -1
		case okb:
			return 1
		}
		return 0
	})
	return keys
}

// missingVerbs are the verbs no layer words, or words without the question
// they ask first.
func missingVerbs(verbs map[string]VerbText) Problems {
	var problems Problems
	for _, v := range builtin.TaskVerbs {
		if _, ok := verbs[v]; !ok {
			problems = append(problems, Problem{File: TasksFile, Field: "verbs." + v, Msg: "is missing: every verb has a table"})
		}
	}
	for _, v := range builtin.TaskVerbInputs {
		if w, ok := verbs[v]; ok && w.Input == "" {
			problems = append(problems, Problem{File: TasksFile, Field: "verbs." + v + ".input", Msg: "is required: the verb asks for a value first"})
		}
	}
	return problems
}

func writerNames() []string {
	out := make([]string, len(task.Writers))
	for i, w := range task.Writers {
		out[i] = string(w)
	}
	return out
}
