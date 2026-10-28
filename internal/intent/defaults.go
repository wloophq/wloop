package intent

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/task"
	dunit "github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/frontmatter"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
)

// DefaultChanged is resume.changed when a manifest leaves it out: the generic sentence of resume.md.tmpl.
const DefaultChanged = "The head moved since the question: re-check what changed before acting on the answer."

var (
	defaultInputSurfaces = []string{"cli", "instructions"}
)

// sheet places problems of one intent.toml by key path.
type sheet struct {
	file     string
	at       map[string]frontmatter.Position
	problems Problems
	// warnings are what loads but is deprecated.
	warnings Problems
	// extras are the brief.extra.* facts of a provider's brief intent, which
	// a workflow's predicates over a briefed subject read; nil reads none.
	extras func(dunit.ProviderID) expr.Env
	// defaults are the profile's defaults.toml.
	defaults manifestDefaults
	// taskFacts are the task.prop.* facts of tasks.toml, which a predicate
	// over a task's facts reads.
	taskFacts expr.Env
}

// pos is where path's value starts, else its nearest placed parent, else 1:1.
func (s *sheet) pos(path string) dintent.Pos {
	for p := path; p != ""; p = parent(p) {
		if at, ok := s.at[p]; ok {
			return dintent.Pos{File: s.file, Line: at.Line, Col: at.Col}
		}
	}
	return dintent.Pos{File: s.file, Line: 1, Col: 1}
}

func parent(path string) string {
	i := strings.LastIndexAny(path, ".[")
	if i < 0 {
		return ""
	}
	return path[:i]
}

func (s *sheet) add(path, format string, args ...any) {
	s.problems = append(s.problems, s.problem(path, format, args...))
}

func (s *sheet) warn(path, format string, args ...any) {
	s.warnings = append(s.warnings, s.problem(path, format, args...))
}

func (s *sheet) problem(path, format string, args ...any) Problem {
	at := s.pos(path)
	return Problem{File: s.file, Line: at.Line, Col: at.Col, Field: path, Msg: fmt.Sprintf(format, args...)}
}

func str(v *string, def string) string {
	if v == nil {
		return def
	}
	return *v
}

func (s *sheet) required(path string, v *string) string {
	if v == nil {
		s.add(path, "is required")
		return ""
	}
	return *v
}

func (s *sheet) pred(path string, v *string, def string) dintent.Pred {
	if v == nil {
		return dintent.Pred{Src: def, At: dintent.Pos{File: s.file}}
	}
	return dintent.Pred{Src: *v, At: s.pos(path)}
}

func (s *sheet) text(path string, v *string, def string) dintent.Text {
	if v == nil {
		return dintent.Text{Src: def, At: dintent.Pos{File: s.file}}
	}
	return dintent.Text{Src: *v, At: s.pos(path)}
}

func boolOr(v *bool, def bool) bool {
	if v == nil {
		return def
	}
	return *v
}

func orEmpty(v []string, def []string) []string {
	if v == nil {
		return append([]string(nil), def...)
	}
	return v
}

// define applies the manifest defaults and reports missing required keys.
func (s *sheet) define(name dintent.Name, m manifest) dintent.Def {
	d := dintent.Def{
		Name:       name,
		Label:      s.required("label", m.Label),
		Summary:    s.required("summary", m.Summary),
		Key:        m.Shortcut,
		Verb:       m.Verb,
		UserFacing: boolOr(m.UserFacing, true),
		Provider:   s.defaults.Provider,
		Intake:     m.Intake,
		OwnerOnly:  m.OwnerOnly,
		Answerable: boolOr(m.Answerable, true),
		Reports:    boolOr(m.Reports, true),
		Brief:      dintent.Brief{Render: true, Wait: true},
		Dims:       orEmpty(m.Dims, nil),
		Records:    dintent.Pred{Src: "true", At: dintent.Pos{File: s.file}},
		Effects:    orEmpty(m.Effects, nil),
		Guidance:   dintent.Guidance{Label: "Guidance"},
		Resume:     dintent.Resume{Changed: DefaultChanged},
		Runners:    s.runners(m.Runners),
		Role:       m.Role,
		Builds:     m.Builds,
		Prompt:     m.Prompt,
		Schema:     m.Schema,
		Skill:      m.Skill,
	}
	if m.Why != nil {
		d.Why = s.text("why", m.Why, "")
	}
	for i, r := range m.Refuse {
		path := fmt.Sprintf("refuse[%d]", i)
		var rf dintent.Refuse
		if r.When == nil {
			s.add(path+".when", "is required")
		} else {
			rf.When = s.pred(path+".when", r.When, "")
		}
		if r.Why == nil {
			s.add(path+".why", "is required: the text the refusal says")
		} else {
			rf.Why = s.text(path+".why", r.Why, "")
		}
		d.Refuse = append(d.Refuse, rf)
	}
	bindRole(&d)
	s.provider(&d, m.Provider)
	if m.Drafts != nil {
		d.AllowedEffects = orEmpty(m.Drafts.AllowedEffects, nil)
		d.AttachPosts = m.Drafts.AttachPosts
		env := builtin.ApprovalEnv(false, servedSlots(d)...)
		if slices.Contains(servedSlots(d), builtin.TaskSlot) {
			maps.Copy(env, s.taskFacts)
		}
		d.DraftApproval = s.draftApprovalAt("drafts.approval", m.Drafts.Approval, env)
	}
	s.workspace(&d, m.Workspace)
	d.Board = builtin.BoardOf(d.Workspace)
	d.StartLine = str(m.StartLine, "starting the "+strings.ToLower(d.Label))
	if p := str(m.Profile, "none"); p != "none" {
		d.Profile = p
	}
	switch {
	case m.Session == nil && d.Builds == "":
		s.add("session.strategy", "is required")
		s.add("session.title", "is required")
	case m.Session != nil:
		d.Session = dintent.Session{
			Strategy:      s.required("session.strategy", m.Session.Strategy),
			Title:         s.required("session.title", m.Session.Title),
			CloseWhenDone: m.Session.CloseWhenDone,
		}
		if m.Session.Name != nil || s.defaults.SessionName != "" {
			d.Session.Name = s.text("session.name", m.Session.Name, s.defaults.SessionName)
		}
	}
	if t := m.Target; t != nil {
		if t.Missing != "" && t.Missing != dintent.TargetCapture {
			s.add("target.missing", "%q is not %q", t.Missing, dintent.TargetCapture)
		}
		if t.Missing == dintent.TargetCapture && d.Provider != (task.Codec{}).Provider() {
			s.add("target.missing", "capture makes a task the turn's unit, and the intent serves %s units, not %s ones", d.Provider, (task.Codec{}).Provider())
		}
		d.Target = dintent.Target{Missing: t.Missing, Title: s.text("target.title", t.Title, "")}
	}
	if m.Brief != nil {
		d.Brief = dintent.Brief{Render: boolOr(m.Brief.Render, true), Wait: boolOr(m.Brief.Wait, true), Need: m.Brief.Need}
	}
	if h := m.Help; h != nil {
		d.Help = dintent.Help{Target: h.Target, Summary: h.Summary, Group: h.Group, Examples: h.Examples, Docs: h.Docs}
	}
	if g := m.Guidance; g != nil {
		d.Guidance = dintent.Guidance{Label: str(g.Label, "Guidance"), Help: g.Help, Max: g.Max}
	}
	if d.Builds != "" {
		d.Answerable, d.Reports = false, false
	}
	if m.Headline != nil || d.Builds == "" {
		d.Headline = s.headline(m.Headline)
	}
	d.Inputs = s.inputs(m.Inputs)
	for i, a := range m.AlsoProfile {
		path := fmt.Sprintf("also_profile[%d]", i)
		ap := dintent.AlsoProfile{
			Profile: s.required(path+".profile", a.Profile),
			Dims:    a.Dims,
			Define:  s.required(path+".define", a.Define),
		}
		if a.When == nil {
			s.add(path+".when", "is required")
		} else {
			ap.When = s.pred(path+".when", a.When, "")
		}
		ap.Prefix = str(a.Prefix, ap.Profile+" guide: ")
		d.AlsoProfiles = append(d.AlsoProfiles, ap)
	}
	d.Action = s.decodeAction(m.Action)
	if m.Join != nil {
		d.Join.DiffersOn = m.Join.DiffersOn
	}
	switch r := m.Record; {
	case r == nil && d.Builds == "":
		s.add("record.states", "is required")
	case r != nil:
		if r.States == nil {
			s.add("record.states", "is required")
		}
		d.Records = s.pred("record.when", r.When, "true")
		d.Record = dintent.RecordRules{
			States:          r.States,
			Artifacts:       orEmpty(r.Artifacts, nil),
			Outcome:         orEmpty(r.Outcome, nil),
			OutcomeDoc:      r.OutcomeDoc,
			HistoryArtifact: r.HistoryArtifact,
			AttachURL:       r.AttachURL,
		}
		for i, q := range r.Require {
			d.Record.Require = append(d.Record.Require, dintent.Require{
				State:    s.required(fmt.Sprintf("record.require[%d].state", i), q.State),
				Artifact: q.Artifact,
				Fields:   q.Fields,
			})
		}
	}
	if m.Resume != nil {
		d.Resume.Changed = str(m.Resume.Changed, DefaultChanged)
	}
	return d
}

func (s *sheet) headline(h *mHeadline) dintent.Headline {
	if h == nil {
		h = &mHeadline{}
	}
	var out dintent.Headline
	if h.Resumed == nil {
		s.add("headline.resumed", "is required")
	} else {
		out.Resumed = s.text("headline.resumed", h.Resumed, "")
	}
	if len(h.New) == 0 {
		s.add("headline.new", "is required: at least one case, the last without when")
	}
	for i, c := range h.New {
		path := fmt.Sprintf("headline.new[%d]", i)
		var cs dintent.Case
		if c.When != nil {
			cs.When = s.pred(path+".when", c.When, "")
		}
		if c.Line == nil {
			s.add(path+".line", "is required")
		} else {
			cs.Text = s.text(path+".line", c.Line, "")
		}
		out.New = append(out.New, cs)
	}
	return out
}

func (s *sheet) inputs(in map[string]mInput) []dintent.Input {
	names := make([]string, 0, len(in))
	for n := range in {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []dintent.Input
	for _, n := range names {
		m := in[n]
		path := "inputs." + n
		out = append(out, dintent.Input{
			Name:       n,
			Type:       s.required(path+".type", m.Type),
			Flag:       m.Flag,
			Help:       m.Help,
			From:       str(m.From, "user"),
			Persist:    m.Persist,
			Hidden:     m.Hidden,
			ByHandOnly: m.ByHandOnly,
			Before:     m.Before,
			Max:        m.Max,
			Surfaces:   orEmpty(m.Surfaces, defaultInputSurfaces),
			At:         s.pos(path),
		})
	}
	return out
}

// bindRole sets what d's role implies: its round, the builtins its turns
// run and whether they drive their workspace's row.
func bindRole(d *dintent.Def) {
	r, _ := builtin.RoleOf(d.Role)
	d.RoundKind, d.Reconcile, d.Prepare, d.Launch = r.RoundKind, r.Reconcile, r.Prepare, r.Launch
	d.Context, d.Policy, d.DrivesRow = r.Context, r.Policy, r.DrivesRow
}

// servedSlots are the workflow slots of the units d's turns run on.
func servedSlots(d dintent.Def) []string {
	if d.Serves(dintent.AnyProvider) {
		return builtin.UnitSlots()
	}
	var out []string
	for _, p := range append([]dunit.ProviderID{d.ProviderOrDefault()}, d.Providers...) {
		if slot, ok := builtin.Slots[p]; ok && !slices.Contains(out, slot) {
			out = append(out, slot)
		}
	}
	return out
}
