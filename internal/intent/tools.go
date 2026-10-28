package intent

import (
	"errors"
	"io"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/internal/profile"
)

// ToolsFile is the profile file that describes the MCP tools.
const ToolsFile = "tools.toml"

// ToolText is an MCP tool's description, its parameters', by parameter
// path, and its answers, by name, as templates over builtin.ToolData and,
// for an answer, the fields of its builtin.ToolAnswers row.
type ToolText struct {
	Description string
	Params      map[string]string
	Answers     map[string]string
}

type mToolText struct {
	Description *string           `toml:"description"`
	Params      map[string]string `toml:"params"`
	Answers     map[string]string `toml:"answers"`
}

// loadTools reads tools.toml from bundled and the other layers, by tool
// name: a higher layer's key replaces a lower one's, and a user's text that
// fails keeps the lower one. A plugin's tools are worded by its layer.
func loadTools(layers []profile.Layer, bundled fs.FS) (map[string]ToolText, Problems) {
	out := map[string]ToolText{}
	if bundled == nil {
		return out, nil
	}
	src, err := fs.ReadFile(bundled, ToolsFile)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, Problems{{File: ToolsFile, Msg: err.Error()}}
	}
	problems := mergeTools(out, ToolsFile, src)
	for _, l := range layers {
		if l.Bundled() {
			continue
		}
		r, err := profile.ReadFile(l, ToolsFile)
		file, src := r.Path, r.Body
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			problems = append(problems, Problem{File: file, Msg: err.Error()})
			continue
		}
		problems = append(problems, mergeTools(out, file, src)...)
	}
	for _, v := range builtin.Tools {
		if _, ok := out[v.Name]; !ok && worded(layers, v.Name) {
			problems = append(problems, Problem{File: ToolsFile, Field: v.Name, Msg: "is missing: every tool has a table"})
		}
	}
	for _, a := range builtin.ToolAnswers {
		if _, ok := out[a.Tool].Answers[a.Name]; !ok && worded(layers, a.Tool) {
			problems = append(problems, Problem{File: ToolsFile, Field: a.Tool + ".answers." + a.Name, Msg: "is missing: every answer of a tool has a text"})
		}
	}
	return out, problems
}

// worded says whether layers must word tool: a core tool, or one of a
// plugin whose layer is among them.
func worded(layers []profile.Layer, tool string) bool {
	p, ok := builtin.ToolProviders[tool]
	return !ok || slices.ContainsFunc(layers, func(l profile.Layer) bool { return l.Name == profile.PluginLayer(string(p)) })
}

func mergeTools(out map[string]ToolText, file string, src []byte) Problems {
	m, problems, _, complete := decode[map[string]mToolText](file, src)
	if !complete {
		return problems
	}
	names := builtin.ValueNames(builtin.Tools)
	s := &sheet{file: file, at: positions(src), problems: problems}
	for _, name := range slices.Sorted(maps.Keys(m)) {
		if !slices.Contains(names, name) {
			s.add(name, "names no tool%s", expr.DidYouMean(name, names))
			continue
		}
		out[name] = s.overTool(name, out[name], m[name])
	}
	s.addGrammar(grammar(builtin.SurfaceTools, src))
	return s.problems
}

func (s *sheet) overTool(name string, t ToolText, m mToolText) ToolText {
	t.Params, t.Answers = maps.Clone(t.Params), maps.Clone(t.Answers)
	if t.Params == nil {
		t.Params = map[string]string{}
	}
	if t.Answers == nil {
		t.Answers = map[string]string{}
	}
	if m.Description != nil {
		path := name + ".description"
		switch v := strings.TrimSpace(*m.Description); {
		case v == "":
			s.add(path, "is required")
		case s.toolText(path, v, nil):
			t.Description = v
		}
	}
	for _, p := range slices.Sorted(maps.Keys(m.Params)) {
		v := strings.TrimSpace(m.Params[p])
		if s.toolText(name+".params."+p, v, nil) {
			t.Params[p] = v
		}
	}
	for _, a := range slices.Sorted(maps.Keys(m.Answers)) {
		path := name + ".answers." + a
		row, ok := builtin.AnswerOf(name, a)
		if !ok {
			var names []string
			for _, r := range builtin.ToolAnswers {
				if r.Tool == name {
					names = append(names, r.Name)
				}
			}
			s.add(path, "names no answer of %s%s", name, expr.DidYouMean(a, names))
			continue
		}
		v := strings.TrimSpace(m.Answers[a])
		if v == "" {
			s.add(path, "may not be empty")
			continue
		}
		if s.toolText(path, v, row.Fields) {
			t.Answers[a] = v
		}
	}
	return t
}

// toolText says whether src renders over builtin.AnswerSamples of fields,
// adding the problem when it does not.
func (s *sheet) toolText(path, src string, fields []builtin.AnswerField) bool {
	for _, data := range builtin.AnswerSamples(fields) {
		if err := builtin.RenderToolText(io.Discard, path, src, data); err != nil {
			_, msg := templateMsg(path, err)
			s.add(path, "%s", msg)
			return false
		}
	}
	return true
}

// BundledTools are the tools' texts of bundled and the bundled plugins'
// layers.
func BundledTools(bundled fs.FS) (map[string]ToolText, Problems) {
	return loadTools(profile.BundledChain(), bundled)
}
