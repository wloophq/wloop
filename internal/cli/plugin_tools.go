package cli

import (
	"context"
	"encoding/json"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/config"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/internal/transport/mcp"
)

// pluginTools are the MCP tools of every plugin cfg enables and serve runs,
// as their manifests declare them, for the sessions of db; none outside
// serve.
func (e *Env) pluginTools(db *sqlite.DB) func(cfg config.Config) []mcp.PluginTool {
	return func(cfg config.Config) []mcp.PluginTool { return e.pluginToolsOf(cfg, sessionBooks{sqlite.New(db)}) }
}

func (e *Env) pluginToolsOf(cfg config.Config, sessions psource.SessionBooks) []mcp.PluginTool {
	if e.pluginKind == nil {
		return nil
	}
	var out []mcp.PluginTool
	for _, p := range e.plugins {
		s, err := p.Manifest.Source()
		if err != nil || p.Binary == "" || !cfg.PluginEnabled(p.ID()) {
			continue
		}
		tools := psource.Tools{ID: p.ID(), Kind: e.pluginKind, Manifest: s, Sessions: sessions}
		for _, t := range s.Tools {
			out = append(out, mcp.PluginTool{Name: t.Name, Input: json.RawMessage(t.Input),
				Call: func(ctx context.Context, args json.RawMessage, session string) (psource.Answer, error) {
					return tools.Call(ctx, t.Name, args, session, "")
				}})
		}
	}
	return out
}
