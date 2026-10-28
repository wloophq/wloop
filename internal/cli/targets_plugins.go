package cli

import (
	"context"
	"slices"

	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/plugins/bundled"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
)

// pluginTargets are the codecs of every plugin's manifest that names the
// targets it posts to, by provider: the discovered plugin's over the
// bundled one.
func (e *Env) pluginTargets() map[unit.ProviderID]*manifest.Compiled {
	srcs, _ := bundled.Sources()
	for _, p := range e.plugins {
		s, err := p.Manifest.Source()
		if err != nil {
			continue
		}
		srcs = slices.DeleteFunc(srcs, func(b *manifest.Source) bool { return b.Provider == s.Provider })
		srcs = append(srcs, s)
	}
	out := map[unit.ProviderID]*manifest.Compiled{}
	for _, s := range srcs {
		if s.Codec.TargetsRe == "" {
			continue
		}
		if c, err := s.Codec.Compile(); err == nil {
			out[unit.ProviderID(s.Provider)] = c
		}
	}
	return out
}

// pluginTakes says whether a plugin posts drafts to target, such as a
// Slack message ref or permalink.
func (e *Env) pluginTakes(target string) bool {
	for _, c := range e.pluginTargets() {
		if c.Takes(target) {
			return true
		}
	}
	return false
}

// resolvers look up, by provider, an address a plugin posts to: the unit
// the daemon resolves it to.
func (e *Env) resolvers() map[unit.ProviderID]unit.Resolver {
	out := map[unit.ProviderID]unit.Resolver{}
	for p, c := range e.pluginTargets() {
		out[p] = addressResolver{e: e, id: p, codec: c}
	}
	return out
}

type addressResolver struct {
	e     *Env
	id    unit.ProviderID
	codec *manifest.Compiled
}

func (r addressResolver) Resolve(ctx context.Context, address string) (string, bool, error) {
	if !r.codec.Takes(address) {
		return "", false, nil
	}
	u, err := r.e.pluginAddressUnit(ctx, r.id, address)
	return u.Key, err == nil, err
}
