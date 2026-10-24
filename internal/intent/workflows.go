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
	"strconv"
	"strings"
	"time"

	dunit "github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/internal/profile"
)

var (
	workflowNameRe   = regexp.MustCompile(`^[a-z][a-z0-9-]{1,39}$`)
	approvalOutcomes = []string{string(workflow.OutcomeRun), string(workflow.OutcomeWait)}
	workflowModes    = []string{string(workflow.ModeObserve), string(workflow.ModeApply)}
	timeVarying      = []string{"pr.silent_days"}
)

// wManifest is workflow.toml as written; a step is a table of its use and its arguments.
type wManifest struct {
	Description   string           `toml:"description"`
	Subject       *string          `toml:"subject"`
	Trigger       *wTrigger        `toml:"trigger"`
	When          *string          `toml:"when"`
	Step          []map[string]any `toml:"step"`
	Limits        *wLimits         `toml:"limits"`
	Mode          *string          `toml:"mode"`
	AllowVisible  bool             `toml:"allow_visible"`
	Order         *int             `toml:"order"`
	Key           []string         `toml:"key"`
	Approval      *wApproval       `toml:"approval"`
	DraftApproval *mDraftApproval  `toml:"draft_approval"`
}

type wTrigger struct {
	Every    *string  `toml:"every"`
	On       []string `toml:"on"`
	Schedule *string  `toml:"schedule"`
}

type wLimits struct {
	PerRun        int    `toml:"per_run"`
	PerDay        int    `toml:"per_day"`
	PerUnitPerDay int    `toml:"per_unit_per_day"`
	Cooldown      string `toml:"cooldown"`
}

type wApproval struct {
	Default  *string `toml:"default"`
	Window   *string `toml:"window"`
	WaitWhen *string `toml:"wait_when"`
}

// wUnit is one workflows/<name>/ directory on its way to a workflow.
type wUnit struct {
	name     string
	dir      string // as shown in problems
	bundled  bool
	files    files
	problems Problems
	wf       workflow.Workflow
}

// loadWorkflows reads workflows/ of the bundled FS and of each directory layer.
// A user workflow that fails is listed in broken and left out of loaded; the
// bundled layer never has problems. intentProblem, when set, is why a
// turn.start may not name an intent, "" when it may.
func loadWorkflows(layers []profile.Layer, bundled fs.FS, intentProblem func(name string) string) (loaded []workflow.Workflow, broken map[string]Problems, problems Problems) {
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
	broken = map[string]Problems{}
	for _, u := range units {
		u.load(intentProblem)
		if len(u.problems) > 0 && len(u.files.overlay) > 0 {
			problems = append(problems, u.problems...)
			u.files.overlay = nil
			u.load(intentProblem)
		}
		if len(u.problems) > 0 {
			problems = append(problems, u.problems...)
			if !u.bundled {
				broken[u.name] = u.problems
			}
			continue
		}
		loaded = append(loaded, u.wf)
	}
	slices.SortFunc(loaded, func(a, b workflow.Workflow) int {
		if a.Order != b.Order {
			return a.Order - b.Order
		}
		return strings.Compare(a.Name, b.Name)
	})
	return loaded, broken, problems
}

func (u *wUnit) load(intentProblem func(string) string) {
	file := u.files.path("workflow.toml")
	s := &sheet{file: file}
	defer func() { u.problems = s.problems }()
	if !workflowNameRe.MatchString(u.name) {
		s.problems = append(s.problems, Problem{File: u.dir, Msg: "a workflow name is 2 to 40 lowercase letters, digits and -, starting with a letter"})
	}
	src, err := u.files.read("workflow.toml")
	if err != nil {
		s.problems = append(s.problems, Problem{File: file, Msg: "missing: every workflow directory holds workflow.toml"})
		return
	}
	m, problems, skip, complete := decode[wManifest](file, src)
	if !complete {
		s.problems = append(s.problems, problems...)
		return
	}
	for i := range problems {
		if problems[i].Field == "trigger.when" {
			problems[i].Msg = "when is a top-level key: write it before the [trigger] table"
		}
	}
	defer func() {
		s.problems = slices.DeleteFunc(s.problems, func(p Problem) bool { return skip != "" && unindexed(p.Field) == unindexed(skip) })
		s.problems = append(s.problems, problems...)
	}()
	s.at = positions(src)
	u.wf = s.defineWorkflow(u.name, m, intentProblem)
	u.wf.Source = file
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

func parseWorkflowDuration(src string) (time.Duration, error) {
	v, err := time.ParseDuration(src)
	if n, ok := strings.CutSuffix(src, "d"); ok {
		days, derr := strconv.Atoi(n)
		if derr != nil {
			return 0, derr
		}
		v, err = time.Duration(days)*24*time.Hour, nil
	}
	if err == nil && v < 0 {
		err = fmt.Errorf("negative")
	}
	return v, err
}

func (s *sheet) duration(path, src string) time.Duration {
	v, err := parseWorkflowDuration(src)
	if err != nil {
		s.add(path, "%q is not a duration such as 30s, 5m, 2h or 1d", src)
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
	w := workflow.Workflow{Name: name, Description: m.Description, AllowVisible: m.AllowVisible, Order: 100, Mode: workflow.ModeApply}
	slot := ""
	switch m.Subject {
	case nil:
		s.add("subject", "is required: the unit provider the workflow matches, one of %s", quoted(providerNames()))
	default:
		p := dunit.ProviderID(*m.Subject)
		var ok bool
		if slot, ok = builtin.Slots[p]; ok {
			w.Provider, w.Slot = p, slot
		} else {
			s.add("subject", "unknown provider %s; the providers are %s", *m.Subject, strings.Join(providerNames(), ", "))
		}
	}
	s.defineTrigger(&w, m.Trigger)
	if m.When == nil && !s.has("trigger.when") {
		s.add("when", "is required: a predicate over the subject's facts, the trigger's and config.*")
	} else if m.When != nil && slot != "" {
		w.When = s.predicate("when", *m.When, builtin.WorkflowEnv(slot))
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
	if m.Mode != nil {
		s.oneOf("mode", *m.Mode, workflowModes)
		w.Mode = workflow.Mode(*m.Mode)
	}
	if m.Order != nil {
		w.Order = *m.Order
	}
	s.defineKey(&w, m.Key, slot)
	s.defineApproval(&w, m.Approval, slot)
	w.DraftApproval = s.draftApproval(m.DraftApproval)
	return w
}

func (s *sheet) defineTrigger(w *workflow.Workflow, t *wTrigger) {
	if t == nil {
		s.add("trigger", "is required: one of every = \"sweep\", on = [...] or schedule = \"...\"")
		return
	}
	set := 0
	for _, k := range []string{"every", "on", "schedule"} {
		if s.has("trigger." + k) {
			set++
		}
	}
	if set != 1 {
		s.add("trigger", "needs exactly one of every, on or schedule; it has %d", set)
		return
	}
	switch {
	case t.Every != nil:
		w.Trigger.Kind = workflow.TriggerSweep
		if *t.Every != "sweep" {
			s.add("trigger.every", "%q is not \"sweep\"; use on or schedule for other triggers", *t.Every)
		}
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
			case !slices.Contains(def.Args, k):
				if len(def.Args) == 0 {
					s.add(ap, "%s takes no arguments", use)
				} else {
					s.add(ap, "%s takes %s, not %s", use, quoted(def.Args), k)
				}
			default:
				v, isString := raw[k].(string)
				if !isString {
					s.add(ap, "must be a string")
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
			if r, ok := raw["round"].(string); ok {
				s.oneOf(p+".round", r, rounds[1:])
			}
		}
		w.Steps = append(w.Steps, st)
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

func (s *sheet) defineApproval(w *workflow.Workflow, a *wApproval, slot string) {
	w.Approval.Default = workflow.OutcomeRun
	if a != nil {
		if a.Default != nil {
			s.oneOf("approval.default", *a.Default, approvalOutcomes)
			w.Approval.Default = workflow.Outcome(*a.Default)
		}
		if a.Window != nil {
			w.Approval.Window = s.duration("approval.window", *a.Window)
		}
		if a.WaitWhen != nil && slot != "" {
			w.Approval.WaitWhen = s.predicate("approval.wait_when", *a.WaitWhen, builtin.WorkflowEnv(slot))
		}
	}
}
