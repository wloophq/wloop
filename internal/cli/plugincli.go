package cli

import (
	"context"
	"fmt"
	"slices"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/source"
)

// runPluginCLI runs the CLI of plugin id with args; a core command passes it
// the subcommands it does not know.
func runPluginCLI(ctx context.Context, e *Env, id unit.ProviderID, args []string) error {
	cfg, err := e.loadConfig()
	if err != nil {
		return err
	}
	i := slices.IndexFunc(plugins, func(p source.Plugin) bool { return p.ID == id })
	if i < 0 {
		return fmt.Errorf("no %s plugin is compiled in", id)
	}
	if !cfg.PluginEnabled(string(id)) {
		return fmt.Errorf("the %s plugin is disabled", id)
	}
	m, err := plugins[i].Build(cfg, e.cliSourceDeps(cfg))
	if err != nil {
		return err
	}
	if m.CLI == nil || m.CLI.Run == nil {
		return fmt.Errorf("the %s plugin has no command", id)
	}
	return m.CLI.Run(ctx, args, e.Out, e.Err)
}

// pluginCommand is the plugin a command name that is no core command calls:
// an enabled plugin by its CLI's name, a disabled one, which is never built,
// by its id.
func (e *Env) pluginCommand(name string) (unit.ProviderID, bool) {
	cfg, err := e.loadConfig()
	if err != nil {
		return "", false
	}
	for _, p := range plugins {
		if !cfg.PluginEnabled(string(p.ID)) {
			if string(p.ID) == name {
				return p.ID, true
			}
			continue
		}
		m, err := p.Build(cfg, e.cliSourceDeps(cfg))
		if err == nil && m.CLI != nil && m.CLI.Name == name {
			return p.ID, true
		}
	}
	return "", false
}

// cliSourceDeps are the shared handles a command line process has: no
// store or daemon services.
func (e *Env) cliSourceDeps(cfg config.Config) source.Deps {
	return e.withGitHubClient(source.Deps{WS: e.WS, Sink: e.boardSink(cfg), Clock: clockFunc(e.Now), Git: e.Git, Agents: e.Agents,
		Log: func(format string, args ...any) { fmt.Fprintf(e.Err, format+"\n", args...) }})
}
