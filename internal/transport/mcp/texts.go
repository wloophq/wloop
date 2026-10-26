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

// ToolText is a tool's description and its parameters', by parameter path,
// as templates over builtin.ToolData: the profile's tools.toml.
type ToolText struct {
	Description string
	Params      map[string]string
}

var toolData = builtin.ToolData{Name: brand.Name, Command: brand.Command, PostsPerTurn: messages.PostsPerTurn}

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
	if err := builtin.RenderToolText(&b, name, src, toolData); err != nil {
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
