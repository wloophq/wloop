package intent

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/frontmatter"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
)

// manifest is intent.toml as written; a pointer marks a key whose absence matters.
type manifest struct {
	Label         *string           `toml:"label"`
	Summary       *string           `toml:"summary"`
	Key           string            `toml:"key"`
	Verb          string            `toml:"verb"`
	UserFacing    *bool             `toml:"user_facing"`
	Workspace     any               `toml:"workspace"`
	Provider      any               `toml:"provider"`
	Session       *mSession         `toml:"session"`
	Context       *string           `toml:"context"`
	Launch        string            `toml:"launch"`
	Policy        *string           `toml:"policy"`
	Intake        bool              `toml:"intake"`
	StartLine     *string           `toml:"start_line"`
	OwnerOnly     bool              `toml:"owner_only"`
	Answerable    *bool             `toml:"answerable"`
	Board         *string           `toml:"board"`
	DrivesRow     bool              `toml:"drives_row"`
	Reports       *bool             `toml:"reports"`
	Reconcile     string            `toml:"reconcile"`
	Brief         *mBrief           `toml:"brief"`
	Profile       *string           `toml:"profile"`
	Dims          []string          `toml:"dims"`
	Records       *string           `toml:"records"`
	Prepare       string            `toml:"prepare"`
	OnSent        []string          `toml:"on_sent"`
	Guidance      *mGuidance        `toml:"guidance"`
	Headline      *mHeadline        `toml:"headline"`
	Inputs        map[string]mInput `toml:"inputs"`
	AlsoProfiles  []mAlsoProfile    `toml:"also_profiles"`
	Action        *mAction          `toml:"action"`
	Join          *mJoin            `toml:"join"`
	Record        *mRecord          `toml:"record"`
	Resume        *mResume          `toml:"resume"`
	DraftApproval *mDraftApproval   `toml:"draft_approval"`
	Modes         []string          `toml:"modes"`
	Role          string            `toml:"role"`
	Builds        string            `toml:"builds"`
	Prompt        string            `toml:"prompt"`
	Schema        string            `toml:"schema"`
	DraftEffects  []string          `toml:"draft_effects"`
	RoundKind     string            `toml:"round_kind"`
	Cap           string            `toml:"cap"`
	ReasonFact    string            `toml:"reason_fact"`
	RefuseWhen    string            `toml:"refuse_when"`
	Reason        string            `toml:"reason"`
	Skill         bool              `toml:"skill"`
	Help          *mHelp            `toml:"help"`
}

type mHelp struct {
	Target   string   `toml:"target"`
	Summary  string   `toml:"summary"`
	Group    string   `toml:"group"`
	Examples []string `toml:"examples"`
	Docs     string   `toml:"docs"`
}

type mSession struct {
	Strategy      *string `toml:"strategy"`
	Title         *string `toml:"title"`
	CloseWhenDone bool    `toml:"close_when_done"`
}

type mBrief struct {
	Render *bool    `toml:"render"`
	Wait   *bool    `toml:"wait"`
	Need   []string `toml:"need"`
}

type mGuidance struct {
	Label *string `toml:"label"`
	Help  string  `toml:"help"`
	Max   int     `toml:"max"`
}

type mHeadline struct {
	Resumed *string `toml:"resumed"`
	New     []mCase `toml:"new"`
}

type mCase struct {
	When *string `toml:"when"`
	Text *string `toml:"text"`
}

type mInput struct {
	Type       *string  `toml:"type"`
	Flag       string   `toml:"flag"`
	Help       string   `toml:"help"`
	From       *string  `toml:"from"`
	Persist    bool     `toml:"persist"`
	Hidden     bool     `toml:"hidden"`
	ByHandOnly bool     `toml:"by_hand_only"`
	Before     string   `toml:"before"`
	Max        int      `toml:"max"`
	Surfaces   []string `toml:"surfaces"`
}

type mAlsoProfile struct {
	Profile      *string `toml:"profile"`
	When         *string `toml:"when"`
	Round        string  `toml:"round"`
	Define       *string `toml:"define"`
	SourcePrefix *string `toml:"source_prefix"`
}

type mAction struct {
	Group            *string  `toml:"group"`
	Surfaces         []string `toml:"surfaces"`
	Visible          *string  `toml:"visible"`
	Enabled          *string  `toml:"enabled"`
	ReasonOff        *string  `toml:"reason_off"`
	Primary          *string  `toml:"primary"`
	Confirm          *string  `toml:"confirm"`
	Hint             *string  `toml:"hint"`
	NoteWhenQuestion string   `toml:"note_when_question"`
}

type mJoin struct {
	DiffersOn []string `toml:"differs_on"`
}

type mRecord struct {
	States          []string   `toml:"states"`
	Artifacts       []string   `toml:"artifacts"`
	Outcome         []string   `toml:"outcome"`
	OutcomeDoc      string     `toml:"outcome_doc"`
	HistoryArtifact string     `toml:"history_artifact"`
	Require         []mRequire `toml:"require"`
}

type mRequire struct {
	State    *string  `toml:"state"`
	Artifact string   `toml:"artifact"`
	Fields   []string `toml:"fields"`
}

type mResume struct {
	HeadMoved *string `toml:"head_moved"`
}

// mDraftApproval is the [draft_approval] table intents and workflows share.
type mDraftApproval struct {
	Mode   *string         `toml:"mode"`
	Window *string         `toml:"window"`
	Rule   []mApprovalRule `toml:"rule"`
}

type mApprovalRule struct {
	When   *string `toml:"when"`
	Mode   *string `toml:"mode"`
	Window *string `toml:"window"`
}

// turnModes are the turn.Mode values a manifest's modes take.
var turnModes = []string{"session", "headless"}

func (s *sheet) modes(m []string) []string {
	if m == nil {
		return nil
	}
	if len(m) == 0 {
		s.add("modes", "lists at least one of %s", quoted(turnModes))
		return nil
	}
	for i, v := range m {
		switch {
		case !slices.Contains(turnModes, v):
			s.oneOf(fmt.Sprintf("modes[%d]", i), v, turnModes)
			return nil
		case slices.Contains(m[:i], v):
			s.add(fmt.Sprintf("modes[%d]", i), "repeats %q", v)
			return nil
		}
	}
	return slices.Clone(m)
}

var draftApprovalModes = []string{string(draft.Wait), string(draft.Delay), string(draft.Send), string(draft.Record)}

// draftApproval is the policy of m, its rules' when compiled over env; nil
// when m is. A rule that releases while reading draft.delivery in a form
// that never names comment delivery loads with a warning.
func (s *sheet) draftApproval(m *mDraftApproval, env expr.Env) *draft.Policy {
	if m == nil {
		return nil
	}
	a, ok := s.approval("draft_approval", m.Mode, m.Window)
	if !ok {
		return nil
	}
	p := &draft.Policy{Default: a}
	for i, r := range m.Rule {
		path := fmt.Sprintf("draft_approval.rule[%d]", i)
		ra, ok := s.approval(path, r.Mode, r.Window)
		if r.When == nil {
			s.add(path+".when", "is required: a predicate over the draft's facts and its unit's")
			ok = false
		}
		if !ok {
			continue
		}
		rule, err := builtin.ApprovalRule(*r.When, ra, env)
		if err != nil {
			s.ruleProblem(path+".when", *r.When, err, env)
			continue
		}
		if builtin.NeverReleasesComment(rule) {
			s.warn(path+".when", "reads draft.delivery but never releases a comment draft; to release one, write %s", builtin.CommentForm)
		}
		p.Rules = append(p.Rules, rule)
	}
	return p
}

// ruleProblem places why a rule's when does not compile; one that reads a
// fact only the loop names where an agent names it says so.
func (s *sheet) ruleProblem(path, when string, err error, env expr.Env) {
	var untrusted *builtin.UntrustedError
	var subjects *builtin.SubjectsError
	if errors.As(err, &untrusted) || errors.As(err, &subjects) {
		s.add(path, "%s", err.Error())
		return
	}
	for _, name := range builtin.AutoOnly {
		if _, ok := env[name]; ok {
			continue
		}
		with := maps.Clone(env)
		with[name] = builtin.ApprovalEnv(true)[name]
		if _, perr := builtin.ApprovalRule(when, draft.Approval{Mode: draft.Wait}, with); perr == nil {
			s.add(path, "reads %s, which an intent's rule may not: its turns' agents name their drafts' purpose and effects", name)
			return
		}
	}
	msg, snippet := exprMsg(err)
	s.add(path, "%s", msg)
	s.problems[len(s.problems)-1].Snippet = snippet
}

// approval is the mode and window at path.
func (s *sheet) approval(path string, mode, window *string) (draft.Approval, bool) {
	if mode == nil {
		s.add(path+".mode", "is required: one of %s", quoted(draftApprovalModes))
		return draft.Approval{}, false
	}
	a := draft.Approval{Mode: draft.ApprovalMode(*mode)}
	switch {
	case !slices.Contains(draftApprovalModes, *mode):
		s.oneOf(path+".mode", *mode, draftApprovalModes)
		return draft.Approval{}, false
	case a.Mode == draft.Delay && window == nil:
		s.add(path+".window", "is required with mode delay: how long the user has before the draft is sent, such as 10m")
		return draft.Approval{}, false
	case a.Mode != draft.Delay && window != nil:
		s.add(path+".window", "is read only with mode delay, not with %s", a.Mode)
		return draft.Approval{}, false
	case window != nil:
		a.Window = s.duration(path+".window", *window)
		if a.Validate() != nil {
			s.add(path+".window", "must be longer than zero")
			return draft.Approval{}, false
		}
	}
	return a, true
}

// decode reads intent.toml strictly. After a type mismatch it blanks the
// offending key and decodes again, so semantic checks still run; skip is that
// key's path, whose semantic problems would repeat the mismatch. complete is
// false after a syntax error: there is no tree to check.
func decode[T any](file string, src []byte) (m T, problems Problems, skip string, complete bool) {
	err := strictDecode(src, &m)
	var strict *toml.StrictMissingError
	var de *toml.DecodeError
	if !errors.As(err, &strict) && errors.As(err, &de) && len(de.Key()) > 0 {
		row, col := de.Position()
		skip = mismatchedPath(src, strings.Join(de.Key(), "."), row, col)
		problems = Problems{{File: file, Line: row, Col: col, Field: skip, Msg: typeMismatch(de.Error())}}
		blanked, ok := blankKeyValue(src, row, col)
		if !ok {
			return m, problems, skip, false
		}
		var zero T
		m = zero
		err = strictDecode(blanked, &m)
		if !errors.As(err, &strict) && errors.As(err, &de) {
			return m, problems, skip, false
		}
	}
	switch {
	case err == nil:
		return m, problems, skip, true
	case errors.As(err, &strict):
		for i := range strict.Errors {
			de := &strict.Errors[i]
			row, col := de.Position()
			problems = append(problems, Problem{File: file, Line: row, Col: col, Field: strings.Join(de.Key(), "."), Msg: unknownKey(reflect.TypeFor[T](), de.Key())})
		}
		return m, problems, skip, true
	case errors.As(err, &de):
		row, col := de.Position()
		field, msg := syntaxError(src, row, strings.TrimPrefix(de.Error(), "toml: "))
		return m, Problems{{File: file, Line: row, Col: col, Field: field, Msg: msg}}, "", false
	}
	return m, Problems{{File: file, Line: 1, Col: 1, Msg: err.Error()}}, "", false
}

// mismatchedPath narrows go-toml's key, which stops at an inline table or an
// array, to the path whose value starts at row:col.
func mismatchedPath(src []byte, key string, row, col int) string {
	best := key
	for path, at := range frontmatter.Positions(src, 1) {
		if at.Line == row && at.Col == col && strings.HasPrefix(unindexed(path), key) && len(path) > len(best) {
			best = path
		}
	}
	return best
}

func strictDecode[T any](src []byte, m *T) error {
	dec := toml.NewDecoder(bytes.NewReader(src))
	dec.DisallowUnknownFields()
	return dec.Decode(m)
}

// blankKeyValue replaces the innermost key = value holding row:col with spaces,
// keeping newlines so every other position stays put; inside an inline table it
// takes one separating comma along.
func blankKeyValue(src []byte, row, col int) ([]byte, bool) {
	lines := bytes.SplitAfter(src, []byte("\n"))
	if row < 1 || row > len(lines) {
		return nil, false
	}
	offset := col - 1
	for _, l := range lines[:row-1] {
		offset += len(l)
	}
	var p unstable.Parser
	p.Reset(src)
	var best *unstable.Range
	var inner bool
	var visit func(n *unstable.Node, nested bool)
	visit = func(n *unstable.Node, nested bool) {
		if n.Kind == unstable.KeyValue {
			r := n.Raw
			if int(r.Offset) <= offset && offset < int(r.Offset+r.Length) && (best == nil || r.Length < best.Length) {
				best, inner = &r, nested
			}
			n = n.Value()
		}
		if n.Kind == unstable.InlineTable || n.Kind == unstable.Array {
			for it := n.Children(); it.Next(); {
				visit(it.Node(), nested || n.Kind == unstable.InlineTable)
			}
		}
	}
	for p.NextExpression() {
		visit(p.Expression(), false)
	}
	if best == nil {
		return nil, false
	}
	start, end := int(best.Offset), int(best.Offset+best.Length)
	if inner {
		after := end
		for after < len(src) && (src[after] == ' ' || src[after] == '\t') {
			after++
		}
		before := start - 1
		for before >= 0 && (src[before] == ' ' || src[before] == '\t') {
			before--
		}
		switch {
		case after < len(src) && src[after] == ',':
			end = after + 1
		case before >= 0 && src[before] == ',':
			start = before
		}
	}
	out := bytes.Clone(src)
	for i := start; i < end; i++ {
		if out[i] != '\n' {
			out[i] = ' '
		}
	}
	return out, true
}

func unknownKey(t reflect.Type, key []string) string {
	known := keysAt(t, key[:len(key)-1])
	if s := closest(key[len(key)-1], known); s != "" {
		return "unknown key; did you mean " + s + "?"
	}
	return "unknown key"
}

// keysAt lists the toml keys of the table at path, following map values and slice elements.
func keysAt(t reflect.Type, path []string) []string {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	if t.Kind() == reflect.Map {
		if len(path) == 0 {
			return nil
		}
		return keysAt(t.Elem(), path[1:])
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	if len(path) == 0 {
		var keys []string
		for i := range t.NumField() {
			keys = append(keys, t.Field(i).Tag.Get("toml"))
		}
		return keys
	}
	for i := range t.NumField() {
		if f := t.Field(i); f.Tag.Get("toml") == path[0] {
			return keysAt(f.Type, path[1:])
		}
	}
	return nil
}

func closest(s string, options []string) string {
	best, bestD := "", len(s)/2+2
	for _, o := range options {
		if d := distance(s, o); d < bestD {
			best, bestD = o, d
		}
	}
	return best
}

func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

var mismatch = regexp.MustCompile(`cannot decode TOML (\S+) into .* of type (\S+)$`)

func typeMismatch(msg string) string {
	m := mismatch.FindStringSubmatch(msg)
	if m == nil {
		return strings.TrimPrefix(msg, "toml: ")
	}
	want := m[2]
	switch {
	case strings.HasPrefix(want, "[]"):
		want = "an array"
	case strings.Contains(want, "intent.") || strings.HasPrefix(want, "map["):
		want = "a table"
	case want == "int":
		want = "an integer"
	case want == "bool":
		want = "true or false"
	case want == "string":
		want = "a string"
	}
	return fmt.Sprintf("must be %s, not %s", want, m[1])
}

var (
	assignment = regexp.MustCompile(`^\s*([A-Za-z0-9_.-]+)\s*=`)
	header     = regexp.MustCompile(`^\s*\[\[?\s*([A-Za-z0-9_.-]+)\s*\]\]?`)
)

var predicateKeys = map[string]bool{"records": true, "when": true, "visible": true, "enabled": true, "primary": true, "wait_when": true}

// syntaxError names the key on the error's line and, for a predicate written
// without quotes, says to quote it.
func syntaxError(src []byte, row int, msg string) (string, string) {
	lines := strings.Split(string(src), "\n")
	if row < 1 || row > len(lines) {
		return "", msg
	}
	m := assignment.FindStringSubmatch(lines[row-1])
	if m == nil {
		return "", msg
	}
	key := m[1]
	field := key
	for i := row - 2; i >= 0; i-- {
		h := header.FindStringSubmatch(lines[i])
		if h == nil {
			continue
		}
		field = h[1]
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "[[") {
			n := 0
			for _, l := range lines[:i+1] {
				if g := header.FindStringSubmatch(l); g != nil && g[1] == h[1] && strings.HasPrefix(strings.TrimSpace(l), "[[") {
					n++
				}
			}
			field = fmt.Sprintf("%s[%d]", h[1], n-1)
		}
		field += "." + key
		break
	}
	last := key[strings.LastIndex(key, ".")+1:]
	if predicateKeys[last] {
		msg += "; a predicate is a string: quote it, as in " + last + " = '!pr.mine'"
	}
	return field, msg
}

// positions maps key paths of intent.toml to where their values start.
func positions(src []byte) map[string]frontmatter.Position {
	return frontmatter.Positions(src, 1)
}

func exprMsg(err error) (msg, snippet string) {
	var e *expr.Error
	if errors.As(err, &e) {
		return fmt.Sprintf("%d: %s", e.Col, e.Msg), e.Snippet
	}
	return err.Error(), ""
}
