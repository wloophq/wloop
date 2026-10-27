package mcp

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// ToolText is a tool's description, its parameters', by parameter path, and
// its answers, by name, as templates over builtin.ToolData and an answer's
// fields: the profile's tools.toml.
type ToolText struct {
	Description string
	Params      map[string]string
	Answers     map[string]string
}

var toolData = builtin.ToolData{Name: brand.Name, Command: brand.Command, PostsPerTurn: messages.PostsPerTurn}

// fields are an answer's fields.
type fields map[string]any

// say is tool's answer name over f, rendered from the profile; with no text
// for it, or one that fails, the answer's name and fields, logged once.
func (s *Server) say(tool, name string, f fields) string {
	var src string
	if s.Texts != nil {
		src = s.Texts()[tool].Answers[name]
	}
	data := toolData.Map()
	maps.Copy(data, f)
	if src != "" {
		var b strings.Builder
		err := builtin.RenderToolText(&b, tool+".answers."+name, src, data)
		if err == nil {
			return b.String()
		}
		s.logOnce("mcp: tools.toml %s.answers.%s: %v", tool, name, err)
	} else {
		s.logOnce("mcp: tools.toml has no %s.answers.%s", tool, name)
	}
	return fmt.Sprintf("%s %v", name, map[string]any(f))
}

// answer is the tool result of tool's answer name, an error when isError.
func (s *Server) answer(tool, name string, f fields, isError bool) map[string]any {
	return text(s.say(tool, name, f), isError)
}

// fixThese is tool's answer to arguments with problems.
func (s *Server) fixThese(tool string, problems []string) map[string]any {
	return s.answer(tool, "fix_these", fields{"Problems": strings.Join(problems, "\n")}, true)
}

// badArguments is tool's answer to arguments that do not decode.
func (s *Server) badArguments(tool string, err error) map[string]any {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	return s.answer(tool, "bad_arguments", fields{"Error": msg}, true)
}

// notLoggedIn is tool's answer while Slack is logged out.
func (s *Server) notLoggedIn(tool string) map[string]any {
	return s.answer(tool, "not_logged_in", nil, true)
}

// failed is tool's answer to a call Slack or the daemon failed.
func (s *Server) failed(tool string, err error) map[string]any {
	return s.answer(tool, "failed", fields{"Error": redactTokens(err.Error())}, true)
}

// describe sets t's description and its parameters' from text; it logs,
// once each, a text that names no parameter or does not render.
func (s *Server) describe(t *tool, text ToolText) {
	t.Description = s.render(t.Name+".description", text.Description)
	left := map[string]bool{}
	for p := range text.Params {
		left[p] = true
	}
	walkParams(t.InputSchema, "", func(path string, sc *schema) {
		if src, ok := text.Params[path]; ok {
			sc.Description = s.render(t.Name+".params."+path, src)
			delete(left, path)
		}
	})
	for _, p := range slices.Sorted(maps.Keys(left)) {
		s.logOnce("mcp: tools.toml %s.params.%s names no parameter", t.Name, p)
	}
}

func (s *Server) render(name, src string) string {
	if src == "" {
		return ""
	}
	var b strings.Builder
	if err := builtin.RenderToolText(&b, name, src, toolData.Map()); err != nil {
		s.logOnce("mcp: tools.toml %s: %v", name, err)
		return ""
	}
	return b.String()
}

// walkParams calls f on every parameter under sc, by its path: a property
// of an object as "<object>.<name>", an array's element as "<array>[]".
func walkParams(sc *schema, path string, f func(path string, sc *schema)) {
	if sc == nil {
		return
	}
	if path != "" {
		f(path, sc)
	}
	for _, k := range slices.Sorted(maps.Keys(sc.Properties)) {
		p := k
		if path != "" {
			p = path + "." + k
		}
		walkParams(sc.Properties[k], p, f)
	}
	if sc.Items != nil {
		walkParams(sc.Items, path+"[]", f)
	}
}

func (s *Server) logOnce(format string, args ...any) {
	if s.Log == nil {
		return
	}
	key := fmt.Sprintf(format, args...)
	s.mu.Lock()
	if s.logged == nil {
		s.logged = map[string]bool{}
	}
	seen := s.logged[key]
	s.logged[key] = true
	s.mu.Unlock()
	if !seen {
		s.Log("%s", key)
	}
}
