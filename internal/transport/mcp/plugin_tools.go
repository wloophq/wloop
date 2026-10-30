package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	psource "github.com/wloophq/wloop/internal/plugins/source"
)

// PluginTool is an MCP tool a plugin's manifest declares, which the plugin
// answers and the layered tools.toml words.
type PluginTool struct {
	Name string
	// Input is the tool's input JSON schema, as the manifest writes it.
	Input json.RawMessage
	// Call runs the tool for the loop's session, "" for none.
	Call func(ctx context.Context, args json.RawMessage, session string) (psource.Answer, error)
}

// pluginTools are the registered tools of s.Plugins; a tool whose input
// does not decode is left out and logged once.
func (s *Server) pluginTools() []registered {
	var out []registered
	for _, p := range s.Plugins {
		in := &schema{}
		if err := json.Unmarshal(p.Input, in); err != nil {
			s.logOnce("mcp: plugin tool %s: its input schema: %v", p.Name, err)
			continue
		}
		out = append(out, registered{tool: tool{Name: p.Name, InputSchema: in}, call: s.pluginCall(p)})
	}
	return out
}

func (s *Server) pluginCall(p PluginTool) func(ctx context.Context, session string, args json.RawMessage) any {
	return func(ctx context.Context, session string, args json.RawMessage) any {
		a, err := p.Call(ctx, args, session)
		if err != nil {
			return s.answer(ctx, p.Name, "failed", fields{"Error": redactSecrets(err.Error())}, true)
		}
		return text(s.pluginSay(ctx, p.Name, a), a.IsError)
	}
}

// problem is one problem of a plugin's fix_these answer: an answer of the
// tool and its fields.
type problem struct {
	Answer string         `json:"answer"`
	Fields map[string]any `json:"fields"`
}

// pluginSay words a plugin's answer; a fix_these answer words each of its
// problems, one per line.
func (s *Server) pluginSay(ctx context.Context, tool string, a psource.Answer) string {
	if a.Name != "fix_these" {
		return s.say(ctx, tool, a.Name, a.Fields)
	}
	raw, err := json.Marshal(a.Fields["Problems"])
	var problems []problem
	if err == nil {
		err = json.Unmarshal(raw, &problems)
	}
	if err != nil {
		return s.say(ctx, tool, "failed", fields{"Error": fmt.Sprintf("the plugin's problems do not decode: %v", err)})
	}
	lines := make([]string, len(problems))
	for i, p := range problems {
		lines[i] = s.say(ctx, tool, p.Answer, p.Fields)
	}
	return s.say(ctx, tool, "fix_these", fields{"Problems": strings.Join(lines, "\n")})
}

// secretToken is a token an error may quote: a Slack user, bot or app token.
var secretToken = regexp.MustCompile(`(?:xox[a-z]\.)?(?:xox[a-z]|xapp)-[A-Za-z0-9-]+`)

func redactSecrets(s string) string { return secretToken.ReplaceAllString(s, "[redacted]") }
