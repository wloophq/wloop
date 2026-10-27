package intent

import (
	"errors"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/internal/profile"
)

// ToolsFile is the profile file that describes the MCP tools.
const ToolsFile = "tools.toml"

// ToolText is an MCP tool's description and its parameters', by parameter
// path, as templates over builtin.ToolData.
type ToolText struct {
	Description string
	Params      map[string]string
}

type mToolText struct {
	Description *string           `toml:"description"`
	Params      map[string]string `toml:"params"`
}

// loadTools reads tools.toml from bundled and the user's layers, by tool
// name: a higher layer's key replaces a lower one's, and a user's text that
// fails keeps the lower one.
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
		if l.Dir == "" {
			continue
		}
		file := filepath.Join(l.Dir, ToolsFile)
		src, err := os.ReadFile(file)
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
		if _, ok := out[v.Name]; !ok {
			problems = append(problems, Problem{File: ToolsFile, Field: v.Name, Msg: "is missing: every tool has a table"})
		}
	}
	return out, problems
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
	t.Params = maps.Clone(t.Params)
	if t.Params == nil {
		t.Params = map[string]string{}
	}
	if m.Description != nil {
		path := name + ".description"
		switch v := strings.TrimSpace(*m.Description); {
		case v == "":
			s.add(path, "is required")
		case s.toolText(path, v):
			t.Description = v
		}
	}
	for _, p := range slices.Sorted(maps.Keys(m.Params)) {
		v := strings.TrimSpace(m.Params[p])
		if s.toolText(name+".params."+p, v) {
			t.Params[p] = v
		}
	}
	return t
}

// toolText says whether src renders over builtin.ToolData, adding the
// problem when it does not.
func (s *sheet) toolText(path, src string) bool {
	err := builtin.RenderToolText(io.Discard, path, src, builtin.ToolData{})
	if err != nil {
		_, msg := templateMsg(path, err)
		s.add(path, "%s", msg)
	}
	return err == nil
}
