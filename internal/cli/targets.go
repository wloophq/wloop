package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// unitTarget is the unit arg names: a Slack message permalink (its thread), a pull request URL or
// owner/repo#n, or <provider>:<key> of a compiled-in provider.
func (e *Env) unitTarget(ctx context.Context, arg string) (unit.Unit, error) {
	if isSlackPermalink(arg) {
		return e.slackPermalinkUnit(ctx, arg)
	}
	if strings.Contains(arg, "://") {
		ref, err := pr.ParseURL(arg)
		if err != nil {
			return unit.Unit{}, fmt.Errorf("%s is neither a Slack message permalink nor a pull request URL", arg)
		}
		return ref.Unit(), nil
	}
	if p, key, ok := strings.Cut(arg, ":"); ok && key != "" && slices.Contains(e.providerIDs(), unit.ProviderID(p)) {
		return unit.Unit{Provider: unit.ProviderID(p), Key: key}, nil
	}
	if ref, err := pr.ParseKey(arg); err == nil {
		return ref.Unit(), nil
	}
	return unit.Unit{}, fmt.Errorf("%s names no unit: give a Slack message permalink, a pull request URL or owner/repo#n, or <provider>:<key>", arg)
}

// pluginCodecs are the codecs of the compiled-in plugins.
func pluginCodecs() *unit.Registry {
	var codecs []unit.Codec
	for _, p := range plugins {
		codecs = append(codecs, p.Codec)
	}
	return unit.NewRegistry(codecs...)
}

// providerIDs are the providers of the compiled-in plugins.
func (e *Env) providerIDs() []unit.ProviderID {
	var out []unit.ProviderID
	for _, p := range e.sourcePlugins() {
		out = append(out, p.ID)
	}
	return out
}
