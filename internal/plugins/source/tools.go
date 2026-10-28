package source

import (
	"context"
	"encoding/json"

	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// Tools are the MCP tools a source plugin's manifest declares, as the MCP
// server calls them; the server words each answer from the layered
// tools.toml.
type Tools struct {
	ID   string
	Kind *Kind
}

// Answer is a tool's answer by name, and the fields its wording reads.
type Answer struct {
	Name    string
	Fields  map[string]any
	IsError bool
}

// Call runs tool with args for the MCP session handle, during turn.
func (t Tools) Call(ctx context.Context, tool string, args json.RawMessage, session, turn string) (Answer, error) {
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
