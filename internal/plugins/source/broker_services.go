package source

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/domain/unit"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

type services struct{ b *Broker }

// Ask asks the host's agent for the plugin.
func (s services) Ask(ctx context.Context, req src.AskRequest) (src.AskResult, error) {
	if s.b.Hosted == nil || s.b.Hosted.Ask == nil {
		return src.AskResult{}, src.ErrUnsupported
	}
	return s.b.Hosted.Ask(ctx, req)
}

// Meter records a sample the plugin metered under the host's meter scope
// its Call named; a resource the plugin does not cross is refused.
func (s services) Meter(_ context.Context, x src.Sample) error {
	if s.b.Meters == nil {
		return src.ErrUnsupported
	}
	if !knownResource(x.Resource) {
		return fmt.Errorf("%w: plugin %s meters no %q", src.ErrUnfit, s.b.ID, x.Resource)
	}
	s.b.Meters.add(x)
	return nil
}

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
	if s.b.BriefIntent != nil {
		out.BriefIntent = briefIntentOf(s.b.BriefIntent(unit.ProviderID(s.b.ID)))
	}
	return out, nil
}

// briefIntentOf is in as a plugin reads it.
func briefIntentOf(in brief.Intent) *src.BriefIntent {
	out := &src.BriefIntent{FieldVersions: in.Schema.FieldVersions(), Prompt: in.Prompt, FieldsPrompt: in.FieldsPrompt}
	if x := in.Schema.Extra; x != nil {
		out.ExtraSchema = json.RawMessage(x.Source())
	}
	return out
}

// Profile reads path in the plugin's profile layer.
func (s services) Profile(ctx context.Context, path string) ([]byte, error) {
	if s.b.Hosted == nil || s.b.Hosted.Profile == nil {
		return nil, src.ErrUnsupported
	}
	return s.b.Hosted.Profile(ctx, path)
}

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
