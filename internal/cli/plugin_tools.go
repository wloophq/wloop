package cli

import (
	"context"
	"encoding/json"

	"github.com/wloophq/wloop/internal/config"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/internal/transport/mcp"
)

// pluginTools are the MCP tools of every plugin cfg enables and serve runs,
// as their manifests declare them; none outside serve.
func (e *Env) pluginTools(cfg config.Config) []mcp.PluginTool {
	if e.pluginKind == nil {
		return nil
	}
	var out []mcp.PluginTool
	for _, p := range e.plugins {
		s, err := p.Manifest.Source()
		if err != nil || p.Binary == "" || !cfg.PluginEnabled(p.ID()) {
			continue
		}
		tools := psource.Tools{ID: p.ID(), Kind: e.pluginKind}
		for _, t := range s.Tools {
			out = append(out, mcp.PluginTool{Name: t.Name, Input: json.RawMessage(t.Input),
				Call: func(ctx context.Context, args json.RawMessage, session string) (psource.Answer, error) {
					return tools.Call(ctx, t.Name, args, session, "")
				}})
		}
	}
	return out
}
