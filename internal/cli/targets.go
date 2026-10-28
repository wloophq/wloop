package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// unitTarget is the unit arg names: <provider>:<key> of a compiled-in
// provider, an address a provider looks up (a Slack message ref or permalink, to
// its thread), a web address a codec reads offline (a pull request URL), or
// owner/repo#n.
func (e *Env) unitTarget(ctx context.Context, arg string) (unit.Unit, error) {
	return resolveTarget(ctx, arg, e.providerIDs(), e.resolvers(), pluginCodecs())
}

// resolveTarget is unitTarget over the providers, their resolvers and
// their codecs.
func resolveTarget(ctx context.Context, arg string, providers []unit.ProviderID, resolvers map[unit.ProviderID]unit.Resolver, codecs *unit.Registry) (unit.Unit, error) {
	if u, ok, err := explicitTask(arg); err != nil || ok {
		return u, err
	}
	if p, key, ok := strings.Cut(arg, ":"); ok && key != "" && !strings.HasPrefix(key, "//") && slices.Contains(providers, unit.ProviderID(p)) {
		return unit.Unit{Provider: unit.ProviderID(p), Key: key}, nil
	}
	for _, p := range providers {
		r, ok := resolvers[p]
		if !ok {
			continue
		}
		key, ok, err := r.Resolve(ctx, arg)
		switch {
		case err != nil:
			return unit.Unit{}, err
		case ok:
			return unit.Unit{Provider: p, Key: key}, nil
		}
	}
	if u, ok := codecs.ParseURL(arg); ok {
		return u, nil
	}
	if ref, err := pr.ParseKey(arg); err == nil {
		return ref.Unit(), nil
	}
	return unit.Unit{}, fmt.Errorf("%s names no unit: give a Slack message ref or permalink, a pull request URL or owner/repo#n, or <provider>:<key>", arg)
}

// explicitTask is the task arg names as task:<id>, the only way an argument names one. An argument in
// the entry:<id> form tasks had before is refused with the form it has now.
func explicitTask(arg string) (unit.Unit, bool, error) {
	if key, ok := strings.CutPrefix(arg, "entry:"); ok && (task.Codec{}).Valid(key) == nil {
		if rest, ok := strings.CutPrefix(key, "e-"); ok {
			key = "t-" + rest
		}
		now := task.UnitOf(task.ID(key))
		return unit.Unit{}, false, fmt.Errorf("%s names an entry, and entries are tasks now: give %s", arg, now)
	}
	key, ok := strings.CutPrefix(arg, string(task.Codec{}.Provider())+":")
	if !ok || (task.Codec{}).Valid(key) != nil {
		return unit.Unit{}, false, nil
	}
	return task.UnitOf(task.ID(key)), true, nil
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
