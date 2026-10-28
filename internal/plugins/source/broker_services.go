package source

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/wloophq/wloop/internal/config"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

type services struct{ b *Broker }

func (services) Ask(context.Context, src.AskRequest) (src.AskResult, error) {
	return src.AskResult{}, src.ErrUnsupported
}

func (services) Meter(context.Context, src.Sample) error { return src.ErrUnsupported }

// Config is the plugin's [plugins.<id>] table and the host keys its
// manifest's config_reads names, as the configuration in force holds them.
func (s services) Config(context.Context) (src.Config, error) {
	cfg := s.b.Config()
	tree, err := tree(cfg)
	if err != nil {
		return src.Config{}, err
	}
	out := src.Config{Plugin: cfg.PluginTable(s.b.ID)}
	if plugins, ok := tree[config.KeyOf("Plugins")].(map[string]any); ok && out.Plugin == nil {
		if out.Plugin, err = json.Marshal(plugins[s.b.ID]); err != nil {
			return src.Config{}, err
		}
	}
	host := map[string]any{}
	for _, key := range s.b.Manifest.ConfigReads {
		if v, ok := lookup(tree, strings.Split(key, ".")); ok {
			host[key] = v
		}
	}
	if out.Host, err = json.Marshal(host); err != nil {
		return src.Config{}, err
	}
	return out, nil
}

func (services) Profile(context.Context, string) ([]byte, error) { return nil, src.ErrUnsupported }

// tree is cfg as config get reads it: its keys by their names in the file.
func tree(cfg config.Config) (map[string]any, error) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	return out, json.Unmarshal(raw, &out)
}

func lookup(m map[string]any, path []string) (any, bool) {
	v, ok := m[path[0]]
	if !ok || len(path) == 1 {
		return v, ok
	}
	sub, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	return lookup(sub, path[1:])
}
