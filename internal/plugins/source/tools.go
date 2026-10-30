package source

import (
	"context"
	"encoding/json"

	"github.com/wloophq/wloop/pkg/plugin/manifest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// Tools are the MCP tools a source plugin's manifest declares, as the MCP
// server calls them; the server words each answer from the layered
// tools.toml.
type Tools struct {
	ID   string
	Kind *Kind
	// Manifest is the plugin's [source] table.
	Manifest *manifest.Source
	// Sessions are the host's sessions; a tool that posts at once runs only
	// for no session or a live one.
	Sessions SessionBooks
}

// Answer is a tool's answer by name, and the fields its wording reads.
type Answer struct {
	Name    string
	Fields  map[string]any
	IsError bool
}

// Call runs tool with args for the MCP session handle, during turn. A tool
// that posts at once is refused before it runs for a session that is not
// live, whose post nothing could record.
func (t Tools) Call(ctx context.Context, tool string, args json.RawMessage, session, turn string) (Answer, error) {
	if session != "" && postsNow(t.Manifest, tool) {
		if err := liveSession(ctx, t.Sessions, session); err != nil {
			return Answer{}, err
		}
	}
	c, err := t.Kind.Client(t.ID)
	if err != nil {
		return Answer{}, err
	}
	r, err := c.CallTool(ctx, src.ToolCall{Tool: tool, Args: args, Session: session, Turn: turn})
	if err != nil {
		return Answer{}, err
	}
	out := Answer{Name: r.Answer, IsError: r.IsError}
	if len(r.Fields) > 0 {
		if err := json.Unmarshal(r.Fields, &out.Fields); err != nil {
			return Answer{}, err
		}
	}
	return out, nil
}
