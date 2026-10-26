package intent

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/duration"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/jsonschema"
	"github.com/wloophq/wloop/internal/domain/mark"
	dunit "github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/internal/profile"
)

var (
	workflowNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,39}$`)
	timeVarying    = []string{"pr.silent_days", "entry.waiting_days", "slack.items_unowned_age_days"}
)

// wManifest is workflow.toml as written; a step is a table of its use and its arguments.
type wManifest struct {
	Summary  *string           `toml:"summary"`
	Provider *string           `toml:"provider"`
	Trigger  *wTrigger         `toml:"trigger"`
	When     *string           `toml:"when"`
	Step     []map[string]any  `toml:"step"`
	Limits   *wLimits          `toml:"limits"`
	Observe  bool              `toml:"observe"`
	Order    *int              `toml:"order"`
	Key      []string          `toml:"key"`
	Marks    map[string]string `toml:"marks"`
	Approval *mApproval        `toml:"approval"`
	Drafts   *wDrafts          `toml:"drafts"`
}

// wDrafts is the [drafts] table: the approval of the drafts the
// workflow's steps and turns propose, and when its open ones are withdrawn.
type wDrafts struct {
	Approval *mApproval `toml:"approval"`
	Withdraw *wWithdraw `toml:"withdraw"`
}

type wWithdraw struct {
	When   *string `toml:"when"`
	Reason *string `toml:"reason"`
}

type wTrigger struct {
	On       []string `toml:"on"`
	Schedule *string  `toml:"schedule"`
}

type wLimits struct {
	PerRun        int    `toml:"per_run"`
	PerDay        int    `toml:"per_day"`
	PerUnitPerDay int    `toml:"per_unit_per_day"`
	Cooldown      string `toml:"cooldown"`
}

// wUnit is one workflows/<name>/ directory on its way to a workflow.
type wUnit struct {
	name     string
	dir      string // as shown in problems
	bundled  bool
	files    files
	problems Problems
	warnings Problems
	wf       workflow.Workflow
	// whenAt is where the manifest's when is, for a problem found after it loaded.
	whenAt dintent.Pos
}

// loadWorkflows reads workflows/ of the bundled FS and of each directory layer.
// A user workflow that fails is listed in broken and left out of loaded; a
// bundled one whose override fails is listed in reverted and loads without
// it. intentProblem, when set, is why a turn.start may not name an intent,
// "" when it may.
func loadWorkflows(layers []profile.Layer, bundled fs.FS, intentProblem func(name string) string, extras func(dunit.ProviderID) expr.Env) (loaded []workflow.Workflow, broken, reverted map[string]Problems, problems, warnings Problems) {
	var units []*wUnit
	bundledUnits := map[string]*wUnit{}
	if bundled != nil {
		entries, _ := fs.ReadDir(bundled, "workflows")
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			dir := path.Join("workflows", e.Name())
			u := &wUnit{name: e.Name(), dir: dir, bundled: true, files: files{bundled: bundled, dir: dir}}
			bundledUnits[u.name] = u
			units = append(units, u)
		}
	}
	seen := map[string]string{}
	for _, l := range layers {
		if l.Dir == "" {
			continue
		}
		entries, _ := os.ReadDir(filepath.Join(l.Dir, "workflows"))
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			dir := filepath.Join(l.Dir, "workflows", e.Name())
			if b := bundledUnits[e.Name()]; b != nil {
				if len(overrides([]string{dir})) > 0 {
					b.files.overlay = append(b.files.overlay, dir)
				}
				continue
			}
			if seen[e.Name()] != "" {
				problems = append(problems, Problem{File: dir, Msg: fmt.Sprintf("%s is defined already in %s", e.Name(), seen[e.Name()])})
				continue
			}
			seen[e.Name()] = dir
			units = append(units, &wUnit{name: e.Name(), dir: dir, files: files{dir: dir}})
		}
	}
	broken, reverted = map[string]Problems{}, map[string]Problems{}
	for _, u := range units {
		u.load(intentProblem, extras)
		if len(u.problems) > 0 && len(u.files.overlay) > 0 {
			reverted[u.name] = u.files.own(u.problems, "workflow.toml")
			problems = append(problems, reverted[u.name]...)
			u.files.overlay = nil
			u.load(intentProblem, extras)
		}
		if len(u.problems) > 0 {
			problems = append(problems, u.problems...)
			if !u.bundled {
				broken[u.name] = u.problems
			}
			continue
		}
	}
	readable := markNames(units)
	for _, u := range units {
		if len(u.problems) > 0 {
			continue
		}
		if p := u.unknownMarks(readable); len(p) > 0 {
			problems = append(problems, p...)
			if !u.bundled {
				broken[u.name] = p
			}
			continue
		}
		loaded = append(loaded, u.wf)
		warnings = append(warnings, u.warnings...)
	}
	slices.SortFunc(loaded, func(a, b workflow.Workflow) int {
		if a.Order != b.Order {
			return a.Order - b.Order
		}
		return strings.Compare(a.Name, b.Name)
	})
	return loaded, broken, reverted, problems, warnings
}

// markNames are the marks a workflow may read: those the loaded workflows
// record and the plugins' (builtin.Marks).
func markNames(units []*wUnit) map[string]bool {
	out := map[string]bool{}
	for _, m := range builtin.Marks {
		out[m.Name] = true
	}
	for _, u := range units {
		if len(u.problems) == 0 {
			for name := range u.wf.Marks {
				out[mark.Of(u.wf.Name, name)] = true
			}
		}
	}
	return out
}

func (u *wUnit) unknownMarks(readable map[string]bool) Problems {
	var out Problems
	names := workflow.MarkNames(u.wf.When)
	for _, p := range u.wf.Approval.Predicates() {
		names = append(names, workflow.MarkNames(p)...)
	}
	for _, name := range names {
		if !readable[name] {
			out = append(out, Problem{File: u.wf.Source, Line: u.whenAt.Line, Col: u.whenAt.Col, Field: "when",
				Msg: fmt.Sprintf("handled(%q): no workflow records that mark and no plugin writes one a workflow can read", name)})
		}
	}
	return out
}

func (u *wUnit) load(intentProblem func(string) string, extras func(dunit.ProviderID) expr.Env) {
	file := u.files.path("workflow.toml")
	s := &sheet{file: file, extras: extras}
	defer func() { u.problems, u.warnings = s.problems, s.warnings }()
	if !workflowNameRe.MatchString(u.name) {
		s.problems = append(s.problems, Problem{File: u.dir, Msg: "a workflow name is 2 to 40 lowercase letters, digits and -, starting with a letter"})
	}
	src, err := u.files.read("workflow.toml")
	if err != nil {
		s.problems = append(s.problems, readProblem(file, err, "missing: every workflow directory holds workflow.toml"))
		return
	}
	m, problems, skip, complete := decode[wManifest](file, src)
	if !complete {
		s.problems = append(s.problems, problems...)
		return
	}
	defer func() {
		s.problems = slices.DeleteFunc(s.problems, func(p Problem) bool { return skip != "" && unindexed(p.Field) == unindexed(skip) })
		s.problems = append(s.problems, problems...)
		ps := grammar(builtin.SurfaceWorkflow, src, u.files.user("workflow.toml"))
		for i := range ps {
			if ps[i].Path == "trigger.when" {
				ps[i].Msg = "when is a top-level key: write it before the [trigger] table"
			}
		}
		s.addGrammar(ps)
	}()
	s.at = positions(src)
	u.wf = s.defineWorkflow(u.name, m, intentProblem)
	s.stepFiles(u.wf.Steps, u.files.read)
	u.whenAt = s.pos("when")
	u.wf.Source = file
	u.wf.Bundled = u.bundled
	u.wf.Overrides = overrides(u.files.overlay)
}

func (s *sheet) has(path string) bool {
	_, ok := s.at[path]
	return ok
}

func (s *sheet) predicate(path, src string, env expr.Env) workflow.Predicate {
	p, err := expr.CompileWorkflow(src, env)
	if err != nil {
		msg, snippet := exprMsg(err)
		s.add(path, "%s", msg)
		s.problems[len(s.problems)-1].Snippet = snippet
	}
	return p
}

func (s *sheet) duration(path, src string) time.Duration {
	v, err := duration.Parse(src)
	if err != nil {
		s.add(path, "%s", err.Error())
	}
	return v
}

func providerNames() []string {
	names := make([]string, 0, len(builtin.Slots))
	for p := range builtin.Slots {
		names = append(names, string(p))
	}
	slices.Sort(names)
	return names
}

func (s *sheet) defineWorkflow(name string, m wManifest, intentProblem func(string) string) workflow.Workflow {
	w := workflow.Workflow{Name: name, Observe: m.Observe, Order: 100}
	if m.Summary == nil || strings.TrimSpace(*m.Summary) == "" {
		s.add("summary", "is required: one line saying what the workflow does")
	} else {
		w.Summary = *m.Summary
	}
	slot := ""
	switch m.Provider {
	case nil:
		s.add("provider", "is required: the unit provider the workflow matches, one of %s", quoted(providerNames()))
	default:
		p := dunit.ProviderID(*m.Provider)
		var ok bool
		if slot, ok = builtin.Slots[p]; ok {
			w.Provider, w.Slot = p, slot
		} else {
			s.add("provider", "unknown provider %s; the providers are %s", *m.Provider, strings.Join(providerNames(), ", "))
		}
	}
	s.defineTrigger(&w, m.Trigger)
	if m.When == nil && !s.has("trigger.when") {
		s.add("when", "is required: a predicate over the subject's facts, the trigger's and config.*")
	} else if m.When != nil && slot != "" {
		w.When = s.predicate("when", *m.When, s.withExtras(builtin.WorkflowEnv(slot), w.Provider, slot))
	}
	s.defineSteps(&w, m.Step, intentProblem)
	if l := m.Limits; l != nil {
		for k, v := range map[string]int{"per_run": l.PerRun, "per_day": l.PerDay, "per_unit_per_day": l.PerUnitPerDay} {
			if v < 0 {
				s.add("limits."+k, "must be 0 (no limit) or more")
			}
		}
		w.Limits = workflow.Limits{PerRun: l.PerRun, PerDay: l.PerDay, PerUnitPerDay: l.PerUnitPerDay}
		if l.Cooldown != "" {
			w.Limits.Cooldown = s.duration("limits.cooldown", l.Cooldown)
		}
	}
	if m.Order != nil {
		w.Order = *m.Order
	}
	s.defineKey(&w, m.Key, slot)
	s.defineMarks(&w, m.Marks, slot)
	s.defineApproval(&w, m.Approval, slot)
	if d := m.Drafts; d != nil {
		w.DraftApproval = s.draftApprovalAt("drafts.approval", d.Approval, builtin.ApprovalEnv(true, slot))
		s.defineWithdraw(&w, d.Withdraw, slot)
	}
	s.turnBudget(w, m.Limits)
	return w
}

func (s *sheet) defineWithdraw(w *workflow.Workflow, m *wWithdraw, slot string) {
	if m == nil {
		return
	}
	if m.When == nil {
		s.add("drafts.withdraw.when", "is required: a predicate over the unit's facts and draft.* that withdraws an open draft the workflow proposed")
		return
	}
	if !slices.ContainsFunc(w.Steps, func(st workflow.Step) bool { return st.Use == builtin.StepDraftPropose }) {
		s.add("drafts.withdraw", "withdraws drafts the workflow proposed; it has no %s step", builtin.StepDraftPropose)
	}
	if slot != "" {
		w.Withdraw = s.predicate("drafts.withdraw.when", *m.When, s.withExtras(builtin.WithdrawEnv(slot), w.Provider, slot))
	}
	w.WithdrawReason = draft.WithdrawMoot
	if m.Reason != nil {
		w.WithdrawReason = *m.Reason
	}
}

// turnBudget refuses a workflow that starts turns on units outside
// builtin.RoundProviders without limits.per_day.
func (s *sheet) turnBudget(w workflow.Workflow, l *wLimits) {
	if w.Provider == "" || slices.Contains(builtin.RoundProviders, w.Provider) {
		return
	}
	if !slices.ContainsFunc(w.Steps, func(st workflow.Step) bool { return st.Use == builtin.StepTurnStart }) {
		return
	}
	if l == nil || l.PerDay <= 0 {
		s.add("limits.per_day", "is required: a workflow starting turns on %s units caps them per day", w.Provider)
	}
}

// defineTrigger reads [trigger]; a workflow without one runs on every sweep.
func (s *sheet) defineTrigger(w *workflow.Workflow, t *wTrigger) {
	w.Trigger.Kind = workflow.TriggerSweep
	if t == nil {
		return
	}
	set := 0
	for _, k := range []string{"on", "schedule"} {
		if s.has("trigger." + k) {
			set++
		}
	}
	if set != 1 {
		s.add("trigger", "needs exactly one of on or schedule; it has %d; a workflow without [trigger] runs on every sweep", set)
		return
	}
	switch {
	case s.has("trigger.on"):
		w.Trigger = workflow.Trigger{Kind: workflow.TriggerEvent, On: t.On}
		if len(t.On) == 0 {
			s.add("trigger.on", "must name at least one event kind")
		}
		for i, k := range t.On {
			if slices.Contains(workflow.EngineKinds, k) {
				s.add(fmt.Sprintf("trigger.on[%d]", i), "%s is an engine event; a workflow reacts to source events", k)
			}
		}
	default:
		w.Trigger.Kind = workflow.TriggerSchedule
		c, err := workflow.ParseCron(*t.Schedule)
		if err != nil {
			s.add("trigger.schedule", "%s", strings.TrimPrefix(err.Error(), "schedule: "))
		}
		w.Trigger.Schedule = c
	}
}

func stepDef(use string) (builtin.StepDef, bool) {
	i := slices.IndexFunc(builtin.Steps, func(d builtin.StepDef) bool { return d.Name == use })
	if i < 0 {
		return builtin.StepDef{}, false
	}
	return builtin.Steps[i], true
}

func (s *sheet) defineSteps(w *workflow.Workflow, steps []map[string]any, intentProblem func(string) string) {
	if len(steps) == 0 {
		s.add("step", "is required: at least one [[step]] with use = \"<builtin>\"")
		return
	}
	names := make([]string, len(builtin.Steps))
	for i, d := range builtin.Steps {
		names[i] = d.Name
	}
	for i, raw := range steps {
		p := fmt.Sprintf("step[%d]", i)
		use, ok := raw["use"].(string)
		if !ok {
			s.add(p+".use", "is required: one of %s", quoted(names))
			continue
		}
		def, ok := stepDef(use)
		if !ok {
			s.add(p+".use", "unknown step %q; the steps are %s", use, quoted(names))
			continue
		}
		st := workflow.Step{Use: use, Reach: def.Reach}
		if len(def.Providers) > 0 && w.Provider != "" && !slices.Contains(def.Providers, w.Provider) {
			on := make([]string, len(def.Providers))
			for j, q := range def.Providers {
				on[j] = string(q)
			}
			s.add(p+".use", "%s acts on %s, not on %s", use, strings.Join(on, " or "), w.Provider)
		}
		if len(def.Trigger) > 0 && w.Trigger.Kind != "" && !slices.Contains(def.Trigger, w.Trigger.Kind) {
			on := make([]string, len(def.Trigger))
			for j, k := range def.Trigger {
				on[j] = string(k)
			}
			s.add(p+".use", "%s runs under an %s trigger, not %s", use, strings.Join(on, " or "), w.Trigger.Kind)
		}
		for _, k := range slices.Sorted(maps.Keys(raw)) {
			if k == "use" {
				continue
			}
			ap := p + "." + k
			switch {
			case k == "force":
				s.add(ap, "force is never available to an automatic run")
			case k == "reach":
				s.add(ap, "reach comes from the step's vocabulary; a manifest cannot set it")
			case !slices.Contains(builtin.StepArgs(use), k):
				if args := builtin.StepArgs(use); len(args) == 0 {
					s.add(ap, "%s takes no arguments", use)
				} else {
					s.add(ap, "%s takes %s, not %s", use, quoted(args), k)
				}
			default:
				v, ok := stepArg(use, k, raw[k])
				if !ok {
					s.add(ap, "must be %s", argKind(use, k))
					continue
				}
				if st.Args == nil {
					st.Args = map[string]any{}
				}
				st.Args[k] = v
			}
		}
		if use == builtin.StepTurnStart {
			intent, present := raw["intent"]
			if !present {
				s.add(p+".intent", "is required: the intent the turn runs")
			}
			if name, ok := intent.(string); ok && intentProblem != nil {
				if msg := intentProblem(name); msg != "" {
					s.add(p+".intent", "%s", msg)
				}
			}
		}
		if use == builtin.StepDraftPropose {
			s.draftPropose(p, st.Args)
		}
		args := maps.Clone(raw)
		delete(args, "use")
		ps := builtin.CheckStep(use, args)
		for i := range ps {
			ps[i].Path = p + "." + ps[i].Path
		}
		s.addGrammar(ps)
		w.Steps = append(w.Steps, st)
	}
}

// stepArg is the value v of a step's argument k as the step reads it: a
// list of strings for a list argument, else a string.
func stepArg(use, k string, v any) (any, bool) {
	if argKind(use, k) != "a list of strings" {
		s, ok := v.(string)
		return s, ok
	}
	raw, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, len(raw))
	for i, e := range raw {
		if out[i], ok = e.(string); !ok {
			return nil, false
		}
	}
	return out, true
}

func argKind(use, k string) string {
	if r, ok := builtin.StepArg(use, k); ok && r.Kind == builtin.KindList {
		return "a list of strings"
	}
	return "a string"
}

// stepFiles checks the files a step's arguments name beside the manifest,
// through read: each must exist, and a schema must compile.
func (s *sheet) stepFiles(steps []workflow.Step, read func(string) ([]byte, error)) {
	for i, st := range steps {
		for _, r := range builtin.StepFiles(st.Use) {
			name := r.Default
			if v, ok := st.Args[r.Path].(string); ok {
				name = v
			}
			p := fmt.Sprintf("step[%d].%s", i, r.Path)
			if name != path.Base(name) || strings.HasPrefix(name, ".") {
				s.add(p, "%q is not a file of the workflow's directory", name)
				continue
			}
			b, err := read(name)
			if err != nil {
				s.add(p, "%s: no such file beside the manifest", name)
				continue
			}
			if r.Path == schemaArg {
				if _, err := jsonschema.Compile(b, jsonschema.Options{}); err != nil {
					s.add(p, "%s: %v", name, err)
				}
			}
		}
	}
}

// draftPropose checks draft.propose's arguments.
func (s *sheet) draftPropose(p string, args map[string]any) {
	arg := func(k string) (string, bool) {
		v, ok := args[k].(string)
		return v, ok
	}
	if v, _ := arg("purpose"); strings.TrimSpace(v) == "" {
		s.add(p+".purpose", "is required: what the draft is for, such as nudge or close")
	}
	delivery, _ := arg("delivery")
	if r, _ := arg("reaction"); slices.Contains(builtin.DraftEmojiDeliveries, delivery) && r == "" {
		s.add(p+".reaction", "is required with delivery = \"reaction\": the emoji name, such as white_check_mark")
	}
	if v, ok := arg("repeat_after"); ok {
		s.repeatAfter(p+".repeat_after", v)
	}
	if v, ok := arg("repeat_after_sent"); ok && v != builtin.RepeatForever {
		s.repeatAfter(p+".repeat_after_sent", v)
	}
}

// repeatAfter checks a draft.propose delay: a duration, or a duration of the
// config slot named by its fact.
func (s *sheet) repeatAfter(path, v string) {
	if !strings.HasPrefix(v, "config.") {
		s.duration(path, v)
		return
	}
	if t, ok := builtin.FactType(v); !ok || t != expr.Duration {
		s.add(path, "%q is not a duration of the config slot", v)
	}
}

func (s *sheet) defineKey(w *workflow.Workflow, key []string, slot string) {
	for i, k := range key {
		p := fmt.Sprintf("key[%d]", i)
		t, known := builtin.FactType(k)
		if !known || slot == "" || !strings.HasPrefix(k, slot+".") {
			s.add(p, "%s is not a fact of the %s slot", k, slot)
			continue
		}
		if t == expr.Duration || slices.Contains(timeVarying, k) {
			s.add(p, "%s changes as time passes; a key holds stable facts such as %s.fingerprint", k, slot)
		}
	}
	w.Key = key
}

// markTypes are the fact types a mark holds: a string is a version, a
// time or an int a watermark (mark.Handled).
var markTypes = []expr.Type{expr.String, expr.Time, expr.Int}

func (s *sheet) defineMarks(w *workflow.Workflow, marks map[string]string, slot string) {
	for _, name := range slices.Sorted(maps.Keys(marks)) {
		p, fact := "marks."+name, marks[name]
		if err := mark.ValidName(name); err != nil {
			s.add(p, "%v", err)
			continue
		}
		t, known := builtin.FactType(fact)
		switch {
		case !known || slot == "" || !strings.HasPrefix(fact, slot+"."):
			s.add(p, "%s is not a fact of the %s slot", fact, slot)
		case !slices.Contains(markTypes, t):
			s.add(p, "%s is a %s; a mark holds a string, a time or an int", fact, t)
		}
	}
	w.Marks = marks
}

func (s *sheet) defineApproval(w *workflow.Workflow, a *mApproval, slot string) {
	var env expr.Env
	if slot != "" {
		env = s.withExtras(builtin.WorkflowEnv(slot), w.Provider, slot)
	}
	w.Approval = s.runApproval(a, env)
}

// schemaArg is the step argument holding the schema the step's agent
// answers to, which Go validates the answer against.
const schemaArg = "schema"

// withExtras is env with the brief.extra.* facts of p's brief intent when
// slot's units have a brief.
func (s *sheet) withExtras(env expr.Env, p dunit.ProviderID, slot string) expr.Env {
	if s.extras == nil || !slices.Contains(builtin.BriefSubjects, slot) {
		return env
	}
	maps.Copy(env, s.extras(p))
	return env
}
