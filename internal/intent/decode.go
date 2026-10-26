package intent

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"

	"github.com/wloophq/wloop/internal/frontmatter"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
)

// manifest is intent.toml as written; a pointer marks a key whose absence matters.
type manifest struct {
	Label       *string           `toml:"label"`
	Summary     *string           `toml:"summary"`
	Shortcut    string            `toml:"shortcut"`
	Verb        string            `toml:"verb"`
	UserFacing  *bool             `toml:"user_facing"`
	Workspace   any               `toml:"workspace"`
	Provider    any               `toml:"provider"`
	Session     *mSession         `toml:"session"`
	Intake      bool              `toml:"intake"`
	StartLine   *string           `toml:"start_line"`
	OwnerOnly   bool              `toml:"owner_only"`
	Answerable  *bool             `toml:"answerable"`
	Reports     *bool             `toml:"reports"`
	Brief       *mBrief           `toml:"brief"`
	Profile     *string           `toml:"profile"`
	Dims        []string          `toml:"dims"`
	Effects     []string          `toml:"effects"`
	Why         *string           `toml:"why"`
	Refuse      []mRefuse         `toml:"refuse"`
	Drafts      *mDrafts          `toml:"drafts"`
	Guidance    *mGuidance        `toml:"guidance"`
	Headline    *mHeadline        `toml:"headline"`
	Inputs      map[string]mInput `toml:"inputs"`
	AlsoProfile []mAlsoProfile    `toml:"also_profile"`
	Action      *mAction          `toml:"action"`
	Join        *mJoin            `toml:"join"`
	Record      *mRecord          `toml:"record"`
	Resume      *mResume          `toml:"resume"`
	Runners     []string          `toml:"runners"`
	Role        string            `toml:"role"`
	Builds      string            `toml:"builds"`
	Prompt      string            `toml:"prompt"`
	Schema      string            `toml:"schema"`
	Skill       bool              `toml:"skill"`
	Help        *mHelp            `toml:"help"`
}

type mRefuse struct {
	When *string `toml:"when"`
	Why  *string `toml:"why"`
}

type mDrafts struct {
	AllowedEffects []string   `toml:"allowed_effects"`
	Approval       *mApproval `toml:"approval"`
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
	Line *string `toml:"line"`
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
	Profile *string           `toml:"profile"`
	When    *string           `toml:"when"`
	Dims    map[string]string `toml:"dims"`
	Define  *string           `toml:"define"`
	Prefix  *string           `toml:"prefix"`
}

type mJoin struct {
	DiffersOn []string `toml:"differs_on"`
}

type mRecord struct {
	When            *string    `toml:"when"`
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
	Changed *string `toml:"changed"`
}

// runners are the turn.Runner values a manifest's runners take.
var runners = []string{"session", "headless"}

func (s *sheet) runners(m []string) []string {
	if m == nil {
		return nil
	}
	if len(m) == 0 {
		s.add("runners", "lists at least one of %s", quoted(runners))
		return nil
	}
	for i, v := range m {
		switch {
		case !slices.Contains(runners, v):
			s.oneOf(fmt.Sprintf("runners[%d]", i), v, runners)
			return nil
		case slices.Contains(m[:i], v):
			s.add(fmt.Sprintf("runners[%d]", i), "repeats %q", v)
			return nil
		}
	}
	return slices.Clone(m)
}

// decode reads a manifest; its unknown keys are the grammar's to name.
// After a type mismatch it blanks the
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
	if builtin.IsPredKey(last) {
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
