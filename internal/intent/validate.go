package intent

import (
	"fmt"
	"maps"
	"regexp"
	"slices"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dunit "github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
)

var (
	nameRe      = regexp.MustCompile(`^[a-z][a-z0-9-]{1,23}$`)
	inputNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	flagRe      = regexp.MustCompile(`^--[a-z][a-z0-9-]*$`)
	titleRe     = regexp.MustCompile(`^[a-z]{1,12}$`)
)

func quoted(vs []string) string { return builtin.OrList(vs) }

// oneOf refuses v at path when it is none of values, in the grammar
// table's words.
func (s *sheet) oneOf(path, v string, values []builtin.Value) {
	if msg, _ := builtin.EnumProblem(v, values); msg != "" {
		s.add(path, "%s", msg)
	}
}

func (s *sheet) eachOneOf(path string, vs []string, values []builtin.Value) {
	for i, v := range vs {
		s.oneOf(fmt.Sprintf("%s[%d]", path, i), v, values)
	}
}

// builtinRef checks a builtin name against its slot and says where a misplaced one belongs.
func (s *sheet) builtinRef(path string, slot builtin.Slot, v string) {
	if slices.Contains(builtin.Names[slot], v) {
		return
	}
	for _, other := range []builtin.Slot{builtin.SlotWorkspace, builtin.SlotBefore} {
		if v != "" && slices.Contains(builtin.Names[other], v) {
			s.add(path, "%s belongs in %s, not in %s", v, other, slot)
			return
		}
	}
	s.oneOf(path, v, builtin.NameValues(slot))
}

// effects checks the effects at path against the names applies allows,
// and that an effect of one provider's is named only by its intents.
func (s *sheet) effects(path string, d dintent.Def, names []string, applies builtin.Applies, allowed []string) {
	for i, e := range names {
		at := fmt.Sprintf("%s[%d]", path, i)
		eff, ok := builtin.EffectOf(e)
		switch {
		case !ok || eff.Applies&applies == 0:
			s.add(at, "%q is not one of %s%s", e, quoted(allowed), expr.DidYouMean(e, allowed))
		case eff.Provider != "" && !onlyServes(d, eff.Provider):
			s.add(at, "%s applies to %s units only; a %s intent cannot name it", e, eff.Provider, d.Provider)
		}
	}
}

// onlyServes says whether every unit d's turns run on is of p.
func onlyServes(d dintent.Def, p dunit.ProviderID) bool {
	return !slices.ContainsFunc(providersOf(d), func(q dunit.ProviderID) bool { return q != p })
}

// validate checks one manifest on its own; cross-manifest rules are in crossCheck.
func (s *sheet) validate(d dintent.Def) {
	switch {
	case d.Verb == "":
	case !slices.Contains(builtin.IntentVerbs, d.Verb):
	case d.Action == nil:
		s.add("verb", "verb needs an [action]")
	}
	if d.Workspace != "" {
		s.builtinRef("workspace", builtin.SlotWorkspace, d.Workspace)
	}
	if d.Session.Title != "" && !titleRe.MatchString(d.Session.Title) {
		s.add("session.title", "%q must be 1 to 12 lowercase letters", d.Session.Title)
	}
	s.validateKeys(d)
	if r, ok := builtin.RoleOf(d.Role); ok && r.Provider != "" && !onlyServes(d, r.Provider) {
		s.add("role", "%s runs on %s units; a %s intent cannot hold it", d.Role, r.Provider, d.Provider)
	}
	if r, ok := builtin.RoleOf(d.Role); ok && r.Workspace != "" && d.Workspace != r.Workspace {
		s.add("workspace", "%s runs in %s workspaces: an intent holding it names workspace = %q", d.Role, r.Workspace, r.Workspace)
	}
	s.effects("effects", d, d.Effects, builtin.AppliesTurn, builtin.TurnEffectNames())
	s.effects("drafts.allowed_effects", d, d.AllowedEffects, builtin.AppliesDraft, builtin.EffectNames())
	if d.Guidance.Max < 0 {
		s.add("guidance.max", "must be 0 (no limit) or more")
	}
	s.validateInputs(d)
	s.validateHeadline(d.Headline)
	s.validateRecord(d)
	for i, a := range d.AlsoProfiles {
		for _, k := range slices.Sorted(maps.Keys(a.Dims)) {
			path := fmt.Sprintf("also_profile[%d].dims.%s", i, k)
			if k == dintent.DimStage {
				s.oneOf(path, a.Dims[k], builtin.ValuesOf(builtin.SurfaceIntent, "also_profile[].dims.stage"))
			}
		}
	}
	if d.Skill && !d.UserFacing {
		s.add("skill", "applies to user-facing intents only")
	}
	if d.Skill && d.Help.Target == "" {
		s.add("help.target", "is required when skill is true: the turn skill says what its target is")
	}
	s.validateAction(d)
}

func (s *sheet) validateInputs(d dintent.Def) {
	persisted := map[string]bool{}
	for _, in := range d.Inputs {
		path := "inputs." + in.Name
		if !inputNameRe.MatchString(in.Name) {
			s.add(path, "an input name is lowercase letters, digits and _, starting with a letter")
		}
		if in.Type == "list" && in.From == "user" {
			s.add(path+".type", "a list input comes from prepare, not from the user")
		}
		if in.From == "prepare" {
			if in.Flag != "" {
				s.add(path+".flag", "applies to inputs from the user")
			}
			if in.Help != "" {
				s.add(path+".help", "applies to inputs from the user")
			}
		}
		if in.From == "user" {
			switch {
			case in.Flag == "":
				s.add(path+".flag", "is required for an input from the user")
			case !flagRe.MatchString(in.Flag):
				s.add(path+".flag", "%q must read --name, lowercase letters, digits and -", in.Flag)
			case slices.Contains(builtin.ReservedFlags, in.Flag):
				s.add(path+".flag", "%s belongs to the CLI", in.Flag)
			}
			if in.Help == "" {
				s.add(path+".help", "is required for an input from the user")
			}
		}
		if in.Before != "" {
			s.builtinRef(path+".before", builtin.SlotBefore, in.Before)
		}
		if in.Max < 0 {
			s.add(path+".max", "must be 0 (no limit) or more")
		}
		if in.Max > 0 && in.Type != "text" {
			s.add(path+".max", "applies to text inputs only")
		}
		persisted[in.Name] = in.Persist
	}
	for i, n := range d.Join.DiffersOn {
		if !persisted[n] {
			s.add(fmt.Sprintf("join.differs_on[%d]", i), "%s is not a persisted input", n)
		}
	}
}

func (s *sheet) validateHeadline(h dintent.Headline) {
	for i, c := range h.New {
		path := fmt.Sprintf("headline.new[%d]", i)
		last := i == len(h.New)-1
		switch {
		case last && c.When.At.Line != 0:
			s.add(path+".when", "the last case is the default: leave when out")
		case !last && c.When.At.Line == 0:
			s.add(path+".when", "is required: only the last case may leave it out")
		}
	}
}

func (s *sheet) validateRecord(d dintent.Def) {
	r := d.Record
	if r.States != nil && len(r.States) == 0 {
		s.add("record.states", "must list at least one state")
	}
	if slices.Contains(r.States, "asked") && !d.Answerable {
		s.add("record.states", "asked needs answerable = true")
	}
	if r.HistoryArtifact != "" && !slices.Contains(r.Artifacts, r.HistoryArtifact) {
		s.add("record.history_artifact", "%s is not in record.artifacts", r.HistoryArtifact)
	}
	if r.AttachURL && !slices.Contains(r.Outcome, "url") {
		s.add("record.attach_url", "needs url in record.outcome")
	}
	for i, q := range r.Require {
		path := fmt.Sprintf("record.require[%d]", i)
		if q.State != "" && !slices.Contains(r.States, q.State) {
			s.add(path+".state", "%s is not in record.states", q.State)
		}
		if q.Artifact != "" && !slices.Contains(r.Artifacts, q.Artifact) {
			s.add(path+".artifact", "%s is not in record.artifacts", q.Artifact)
		}
		s.eachOneOf(path+".fields", q.Fields, builtin.ValuesOf(builtin.SurfaceIntent, "record.outcome"))
	}
}

// compile compiles every predicate of d in its slot.
func (s *sheet) compile(d dintent.Def) Programs {
	turn := TurnEnv(d)
	one := func(path string, p dintent.Pred, env expr.Env) *expr.Program {
		prog, err := expr.Compile(p.Src, env)
		if err != nil {
			msg, snippet := exprMsg(err)
			s.add(path, "%s", msg)
			s.problems[len(s.problems)-1].Snippet = snippet
			return prog
		}
		for _, name := range prog.Reads() {
			if fp := builtin.FactProvider(name); fp != "" && fp != d.Provider {
				s.add(path, "%s is a %s fact; a %s intent cannot read it", name, fp, d.Provider)
			}
		}
		return prog
	}
	var ps Programs
	ps.Records = one("record.when", d.Records, turn)
	for i, r := range d.Refuse {
		ps.Refuse = append(ps.Refuse, one(fmt.Sprintf("refuse[%d].when", i), r.When, unitEnv(d, s.taskFacts)))
	}
	for i, c := range d.Headline.New {
		if i == len(d.Headline.New)-1 {
			ps.Headline = append(ps.Headline, nil)
			continue
		}
		ps.Headline = append(ps.Headline, one(fmt.Sprintf("headline.new[%d].when", i), c.When, turn))
	}
	for i, a := range d.AlsoProfiles {
		if a.When.At.Line == 0 {
			ps.Also = append(ps.Also, nil)
			continue
		}
		ps.Also = append(ps.Also, one(fmt.Sprintf("also_profile[%d].when", i), a.When, turn))
	}
	action := builtin.Env(builtin.ActionSlot, nil)
	builtin.Narrow(action, servedProviders(d))
	ps.Action = compileAction(d.Action, func(path string, p dintent.Pred) *expr.Program {
		return one(path, p, action)
	})
	return ps
}

// unitEnv is the env of a predicate over the workflow facts of the units
// d's turns run on: each served slot's, with the brief's, trigger.* and
// config.*, and taskFacts, the profile's task.prop.*, when d serves tasks.
func unitEnv(d dintent.Def, taskFacts expr.Env) expr.Env {
	env := expr.Env{}
	for _, slot := range servedSlots(d) {
		maps.Copy(env, builtin.WorkflowEnv(slot))
	}
	builtin.Narrow(env, servedProviders(d))
	if slices.Contains(servedSlots(d), builtin.TaskSlot) {
		maps.Copy(env, taskFacts)
	}
	return env
}

// servedProviders are the providers d's turns run on, none when it serves
// any.
func servedProviders(d dintent.Def) []dunit.ProviderID {
	ps := providersOf(d)
	if slices.Contains(ps, dintent.AnyProvider) {
		return nil
	}
	return ps
}
