package intent

import (
	"cmp"
	"fmt"
	"slices"
	"sort"
	"strings"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dunit "github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/frontmatter"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
)

// DefaultHeadMoved is resume.head_moved when a manifest leaves it out: the generic sentence of resume.md.tmpl.
const DefaultHeadMoved = "The head moved since the question: re-check what changed before acting on the answer."

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
		Name:           name,
		Label:          s.required("label", m.Label),
		Summary:        s.required("summary", m.Summary),
		Key:            m.Key,
		Verb:           m.Verb,
		UserFacing:     boolOr(m.UserFacing, true),
		Provider:       s.defaults.Provider,
		Context:        str(m.Context, "pr"),
		Launch:         m.Launch,
		Policy:         str(m.Policy, "round"),
		Intake:         m.Intake,
		OwnerOnly:      m.OwnerOnly,
		Answerable:     boolOr(m.Answerable, true),
		Board:          str(m.Board, "none"),
		DrivesRow:      m.DrivesRow,
		Reports:        boolOr(m.Reports, true),
		Reconcile:      m.Reconcile,
		Brief:          dintent.Brief{Render: true, Wait: true},
		Dims:           orEmpty(m.Dims, nil),
		Records:        s.pred("records", m.Records, "true"),
		Prepare:        m.Prepare,
		Effects:        orEmpty(m.OnSent, nil),
		Guidance:       dintent.Guidance{Label: "Guidance"},
		Resume:         dintent.Resume{HeadMoved: DefaultHeadMoved},
		Runners:        s.modes(m.Modes),
		Role:           m.Role,
		Builds:         m.Builds,
		Prompt:         m.Prompt,
		Schema:         m.Schema,
		AllowedEffects: orEmpty(m.DraftEffects, nil),
		RoundKind:      m.RoundKind,
		Cap:            m.Cap,
		ReasonFact:     m.ReasonFact,
		RefuseWhen:     m.RefuseWhen,
		Reason:         m.Reason,
		Skill:          m.Skill,
	}
	roleRound(&d)
	s.provider(&d, m.Provider)
	d.DraftApproval = s.draftApproval(m.DraftApproval, builtin.ApprovalEnv(false, servedSlots(d)...))
	s.workspace(&d, m.Workspace)
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
	for i, a := range m.AlsoProfiles {
		path := fmt.Sprintf("also_profiles[%d]", i)
		ap := dintent.AlsoProfile{
			Profile: s.required(path+".profile", a.Profile),
			Round:   a.Round,
			Define:  s.required(path+".define", a.Define),
		}
		if a.When == nil {
			s.add(path+".when", "is required")
		} else {
			ap.When = s.pred(path+".when", a.When, "")
		}
		ap.SourcePrefix = str(a.SourcePrefix, ap.Profile+" guide: ")
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
		d.Record = dintent.RecordRules{
			States:          r.States,
			Artifacts:       orEmpty(r.Artifacts, nil),
			Outcome:         orEmpty(r.Outcome, nil),
			OutcomeDoc:      r.OutcomeDoc,
			HistoryArtifact: r.HistoryArtifact,
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
		d.Resume.HeadMoved = str(m.Resume.HeadMoved, DefaultHeadMoved)
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
		if c.Text == nil {
			s.add(path+".text", "is required")
		} else {
			cs.Text = s.text(path+".text", c.Text, "")
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

func roleRound(d *dintent.Def) {
	r := builtin.RoleRounds[d.Role]
	d.RoundKind = cmp.Or(d.RoundKind, r.RoundKind)
	if d.RoundKind != "" {
		r.Cap = builtin.CapRounds
	}
	d.Cap = cmp.Or(d.Cap, r.Cap)
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
