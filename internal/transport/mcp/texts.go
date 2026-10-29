package mcp

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

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

type fields map[string]any

type textsKey struct{}

// withTexts is ctx carrying the tools' texts for one call, read once.
func (s *Server) withTexts(ctx context.Context) context.Context {
	if s.Texts == nil {
		return ctx
	}
	return context.WithValue(ctx, textsKey{}, s.Texts())
}

func (s *Server) texts(ctx context.Context) map[string]ToolText {
	if t, ok := ctx.Value(textsKey{}).(map[string]ToolText); ok {
		return t
	}
	if s.Texts != nil {
		return s.Texts()
	}
	return nil
}

// say is tool's answer name over f, rendered from the call's texts; a text
// that is missing or fails falls back to the bundled one, and the answer's
// name and fields stand in only when that fails too.
func (s *Server) say(ctx context.Context, tool, name string, f fields) string {
	s.checkFields(tool, name, f)
	data := toolData.Map()
	maps.Copy(data, f)
	path := tool + ".answers." + name
	src := s.texts(ctx)[tool].Answers[name]
	if src == "" {
		s.logOnce("mcp: tools.toml has no %s", path)
	}
	if out, ok := s.render(path, src, data); ok {
		return out
	}
	if s.Bundled != nil {
		if out, ok := s.render(path, s.Bundled()[tool].Answers[name], data); ok {
			return out
		}
	}
	return fmt.Sprintf("%s %v", name, map[string]any(f))
}

// checkFields notes fields that are not the ones builtin.ToolAnswers lists
// for the answer, which its templates are checked over; a test fails on it.
func (s *Server) checkFields(tool, name string, f fields) {
	row, _ := builtin.AnswerOf(tool, name)
	want := make([]string, len(row.Fields))
	for i, x := range row.Fields {
		want[i] = x.Name
	}
	if got := slices.Sorted(maps.Keys(f)); !slices.Equal(got, slices.Sorted(slices.Values(want))) {
		msg := fmt.Sprintf("mcp: %s.answers.%s gets fields %v, builtin.ToolAnswers lists %v", tool, name, got, want)
		if testing.Testing() {
			panic(msg)
		}
		s.logOnce("%s", msg)
	}
}

// answer is the tool result of tool's answer name, an error when isError.
func (s *Server) answer(ctx context.Context, tool, name string, f fields, isError bool) map[string]any {
	return text(s.say(ctx, tool, name, f), isError)
}

// fixThese is tool's answer to arguments with problems.
func (s *Server) fixThese(ctx context.Context, tool string, problems []string) map[string]any {
	return s.answer(ctx, tool, "fix_these", fields{"Problems": strings.Join(problems, "\n")}, true)
}

// badArguments is tool's answer to arguments that do not decode.
func (s *Server) badArguments(ctx context.Context, tool string, err error) map[string]any {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	return s.answer(ctx, tool, "bad_arguments", fields{"Error": msg}, true)
}

// describe sets t's description and its parameters' from text; it logs,
// once each, a text that names no parameter or does not render.
func (s *Server) describe(t *tool, text ToolText) {
	t.Description, _ = s.render(t.Name+".description", text.Description, toolData.Map())
	left := map[string]bool{}
	for p := range text.Params {
		left[p] = true
	}
	walkParams(t.InputSchema, "", func(path string, sc *schema) {
		if src, ok := text.Params[path]; ok {
			sc.Description, _ = s.render(t.Name+".params."+path, src, toolData.Map())
			delete(left, path)
		}
	})
	for _, p := range slices.Sorted(maps.Keys(left)) {
		s.logOnce("mcp: tools.toml %s.params.%s names no parameter", t.Name, p)
	}
}

// render is the tools.toml text src, named name, over data; false when src
// is empty or fails, which is logged once.
func (s *Server) render(name, src string, data map[string]any) (string, bool) {
	if src == "" {
		return "", false
	}
	var b strings.Builder
	if err := builtin.RenderToolText(&b, name, src, data); err != nil {
		s.logOnce("mcp: tools.toml %s: %v", name, err)
		return "", false
	}
	return strings.Trim(b.String(), "\n"), true
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
