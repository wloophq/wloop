package intent

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/record"
	dunit "github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
)

var (
	nameRe      = regexp.MustCompile(`^[a-z][a-z0-9-]{1,23}$`)
	inputNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	flagRe      = regexp.MustCompile(`^--[a-z][a-z0-9-]*$`)
	titleRe     = regexp.MustCompile(`^[a-z]{1,12}$`)

	strategies    = []string{"reuse_live", "new"}
	boards        = []string{"review", "author", "none"}
	inputTypes    = []string{"flag", "text", "list"}
	inputFroms    = []string{"user", "prepare"}
	inputSurfaces = []string{"cli", "instructions", "try"}
	rounds        = []string{"", "first", "follow-up"}
)

func quoted(vs []string) string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = fmt.Sprintf("%q", v)
	}
	return strings.Join(out, ", ")
}

func (s *sheet) oneOf(path, v string, allowed []string) {
	if !slices.Contains(allowed, v) {
		s.add(path, "%q is not one of %s", v, quoted(allowed))
	}
}

func (s *sheet) eachOneOf(path string, vs, allowed []string) {
	for i, v := range vs {
		s.oneOf(fmt.Sprintf("%s[%d]", path, i), v, allowed)
	}
}

// builtinRef checks a builtin name against its slot and says where a misplaced one belongs.
func (s *sheet) builtinRef(path string, slot builtin.Slot, v string) {
	if slices.Contains(builtin.Names[slot], v) {
		return
	}
	for _, other := range []builtin.Slot{builtin.SlotWorkspace, builtin.SlotLaunch, builtin.SlotPrepare, builtin.SlotBefore, builtin.SlotOnSent, builtin.SlotPolicy, builtin.SlotContext, builtin.SlotReconcile} {
		if v != "" && slices.Contains(builtin.Names[other], v) {
			s.add(path, "%s belongs in %s, not in %s", v, other, slot)
			return
		}
	}
	s.add(path, "unknown %s builtin %q; the %s builtins are %s", slot, v, slot, quoted(builtin.Names[slot]))
}

// bundledOnlyKeys are the manifest keys only a bundled intent sets: the
// probe's synthetic pull request, launch and policy, and the review's
// reconcile, which an intent taking a bundled intent's role also sets
// (crossCheck). The public schema leaves them out.
var bundledOnlyKeys = []string{"context", "launch", "policy", "reconcile"}

func (s *sheet) refuseBundledOnly(m manifest) {
	set := map[string]bool{"context": m.Context != nil, "launch": m.Launch != "", "policy": m.Policy != nil, "reconcile": m.Reconcile != "" && m.Role == ""}
	for _, k := range bundledOnlyKeys {
		if set[k] {
			s.add(k, "is reserved for the bundled intents")
		}
	}
}

// validate checks one manifest on its own; cross-manifest rules are in crossCheck.
func (s *sheet) validate(d dintent.Def) {
	switch {
	case d.Verb == "":
	case !slices.Contains(builtin.IntentVerbs, d.Verb):
		s.oneOf("verb", d.Verb, builtin.IntentVerbs)
	case d.Action == nil:
		s.add("verb", "verb needs an [action]")
	}
	if d.Workspace != "" {
		s.builtinRef("workspace", builtin.SlotWorkspace, d.Workspace)
	}
	if d.Session.Strategy != "" {
		s.oneOf("session.strategy", d.Session.Strategy, strategies)
	}
	if d.Session.Title != "" && !titleRe.MatchString(d.Session.Title) {
		s.add("session.title", "%q must be 1 to 12 lowercase letters", d.Session.Title)
	}
	s.validateKeys(d)
	if d.Provider != "" && !dunit.ValidProvider(string(d.Provider)) {
		s.add("provider", "%q is not a provider id: lowercase letters, digits, _ and -, starting with a letter", d.Provider)
	}
	if p, ok := builtin.ContextProviders[d.Context]; ok && d.Provider != p {
		s.add("context", "%s is a %s unit; a %s intent cannot use it", d.Context, p, d.Provider)
	}
	if d.DrivesRow && !slices.Contains(builtin.RoundProviders, d.Provider) {
		s.add("drives_row", "a held round drives a %s row; a %s intent cannot drive one", strings.Join(idsOf(builtin.RoundProviders), " or "), d.Provider)
	}
	if d.Bundled {
		s.builtinRef("context", builtin.SlotContext, d.Context)
		s.builtinRef("launch", builtin.SlotLaunch, d.Launch)
		s.builtinRef("policy", builtin.SlotPolicy, d.Policy)
		s.builtinRef("reconcile", builtin.SlotReconcile, d.Reconcile)
	} else if d.Reconcile != "" {
		s.builtinRef("reconcile", builtin.SlotReconcile, d.Reconcile)
	}
	s.builtinRef("prepare", builtin.SlotPrepare, d.Prepare)
	for i, e := range d.OnSent {
		s.builtinRef(fmt.Sprintf("on_sent[%d]", i), builtin.SlotOnSent, e)
	}
	s.oneOf("board", d.Board, boards)
	s.eachOneOf("dims", d.Dims, builtin.IntentDims)
	if d.Guidance.Max < 0 {
		s.add("guidance.max", "must be 0 (no limit) or more")
	}
	s.validateInputs(d)
	s.validateHeadline(d.Headline)
	s.validateRecord(d)
	for i, a := range d.AlsoProfiles {
		s.oneOf(fmt.Sprintf("also_profiles[%d].round", i), a.Round, rounds)
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
		if in.Type != "" {
			s.oneOf(path+".type", in.Type, inputTypes)
		}
		s.oneOf(path+".from", in.From, inputFroms)
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
		s.eachOneOf(path+".surfaces", in.Surfaces, inputSurfaces)
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
	s.eachOneOf("record.states", r.States, builtin.RecordStates)
	if slices.Contains(r.States, "asked") && !d.Answerable {
		s.add("record.states", "asked needs answerable = true")
	}
	s.eachOneOf("record.artifacts", r.Artifacts, record.ArtifactKinds)
	s.eachOneOf("record.outcome", r.Outcome, record.OutcomeFields)
	if r.HistoryArtifact != "" && !slices.Contains(r.Artifacts, r.HistoryArtifact) {
		s.add("record.history_artifact", "%s is not in record.artifacts", r.HistoryArtifact)
	}
	for i, q := range r.Require {
		path := fmt.Sprintf("record.require[%d]", i)
		if q.State != "" && !slices.Contains(r.States, q.State) {
			s.add(path+".state", "%s is not in record.states", q.State)
		}
		if q.Artifact != "" && !slices.Contains(r.Artifacts, q.Artifact) {
			s.add(path+".artifact", "%s is not in record.artifacts", q.Artifact)
		}
		s.eachOneOf(path+".fields", q.Fields, record.OutcomeFields)
	}
}

// compile compiles every predicate of d in its slot.
func (s *sheet) compile(d dintent.Def) Programs {
	turn := builtin.Env(builtin.TurnSlot, d.Inputs)
	if !slices.Contains(d.Dims, dintent.DimRound) {
		delete(turn, "turn.stage")
	}
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
	ps.Records = one("records", d.Records, turn)
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
		ps.Also = append(ps.Also, one(fmt.Sprintf("also_profiles[%d].when", i), a.When, turn))
	}
	ps.ActionPrograms = compileAction(d.Action, func(path string, p dintent.Pred) *expr.Program {
		return one(path, p, builtin.Env(builtin.ActionSlot, nil))
	})
	return ps
}

func idsOf(ps []dunit.ProviderID) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = string(p)
	}
	return out
}
