package cli

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/setup"
)

func (e *Env) homeSpec(cfg config.Config) setup.HomeSpec { return e.homeSpecFor(cfg, cfg.Agent) }

func (e *Env) homeSpecFor(cfg config.Config, kind string) setup.HomeSpec {
	return e.specWith(cfg, kind, e.callerHome(cfg, kind))
}

func (e *Env) specWith(cfg config.Config, kind, callerHome string) setup.HomeSpec {
	var managerHomes, sessionVars []string
	for _, m := range e.enabledEntries(cfg) {
		if m.Homes != nil {
			managerHomes = append(managerHomes, m.Homes(e.Home, kind)...)
		}
		if m.SessionVar != "" {
			sessionVars = append(sessionVars, m.SessionVar)
		}
	}
	return setup.HomeSpec{Command: e.loopCommand(), MCPURL: cfg.MCP.URL(), MCPKey: e.mcpKey(), UserHome: e.Home, CallerHome: callerHome,
		SessionVars: sessionVars, ManagerHomes: uniq(managerHomes...), Now: e.Now}
}

func (e *Env) homeKind(agent string) (setup.HomeKind, error) {
	k, ok := e.Homes[agent]
	if !ok {
		return nil, fmt.Errorf("agent %q has no home installer", agent)
	}
	return k, nil
}

// callerHome is kind's agent home of the session running the loop: the one a
// round by hand brings when it is a home of kind, else kind's from the
// environment.
func (e *Env) callerHome(cfg config.Config, kind string) string {
	home := ""
	if e.HomeOverride != nil {
		if e.overrideKind(cfg) == kind {
			home = *e.HomeOverride
		}
	} else if p, err := e.Harness.For(kind); err == nil {
		home = p.Home(e.Getenv)
	}
	if home == "" {
		return ""
	}
	return config.ExpandHome(home)
}

// overrideKind is the agent whose homes, found without a caller's, hold
// the home a round by hand brings, else cfg's top-level one.
func (e *Env) overrideKind(cfg config.Config) string {
	dir := cleanHome(*e.HomeOverride)
	byKind := e.homesFound(cfg, func(string) string { return "" })
	for _, name := range agentNames(cfg) {
		if slices.Contains(byKind[name], dir) {
			return name
		}
	}
	return cfg.Agent
}

// agentHome is the agent home a round's session runs with, as doctor names it.
func (e *Env) agentHome(cfg config.Config) string {
	def := ""
	if k, err := e.homeKind(cfg.Agent); err == nil {
		def = k.Default(e.Home)
	}
	return cmp.Or(cfg.AgentHome, def)
}

// callerAgentHome is the caller's agent home when it is not the kind's
// default one. A home a round by hand brings comes with no kind, so any
// kind's default counts.
func (e *Env) callerAgentHome(cfg config.Config) string {
	home := e.callerHome(cfg, cfg.Agent)
	if home == "" {
		return ""
	}
	var kinds []setup.HomeKind
	if e.HomeOverride != nil {
		kinds = slices.Collect(maps.Values(e.Homes))
	} else if k, err := e.homeKind(cfg.Agent); err == nil {
		kinds = []setup.HomeKind{k}
	}
	for _, k := range kinds {
		if filepath.Clean(home) == filepath.Clean(k.Default(e.Home)) {
			return ""
		}
	}
	return home
}

// callerKind is the agent of the session running the loop, as its hooks
// detect it. A call the daemon runs for the caller reads no config.
func (e *Env) callerKind() string { return e.Harness.Kind(e.Getenv) }

func agentNames(cfg config.Config) []string {
	names := []string{cfg.Agent}
	for _, p := range cfg.Projects {
		names = append(names, p.Agent)
	}
	return uniq(names...)
}

// homesByKind lists, per agent, the homes the loop installs into: each
// kind's own and the agent homes of the projects that run it.
func (e *Env) homesByKind(cfg config.Config) map[string][]string {
	return e.homesFound(cfg, func(kind string) string { return e.callerHome(cfg, kind) })
}

func (e *Env) homesFound(cfg config.Config, callerHome func(kind string) string) map[string][]string {
	out := map[string][]string{}
	for _, name := range agentNames(cfg) {
		if k, err := e.homeKind(name); err == nil {
			out[name] = k.Find(e.specWith(cfg, name, callerHome(name)))
		}
	}
	out[cfg.Agent] = append(out[cfg.Agent], cfg.AgentHome)
	for _, p := range cfg.Projects {
		kind := cmp.Or(p.Agent, cfg.Agent)
		out[kind] = append(out[kind], p.AgentHome)
	}
	for name, dirs := range out {
		out[name] = cleanHomes(uniq(dirs...))
	}
	return out
}

// kindOfHome is the agent whose homes hold dir, else the top-level one.
func (e *Env) kindOfHome(cfg config.Config, dir string) string {
	dir = cleanHome(dir)
	byKind := e.homesByKind(cfg)
	for _, name := range agentNames(cfg) {
		if slices.Contains(byKind[name], dir) {
			return name
		}
	}
	return cfg.Agent
}

// otherOwner is the registered kind other than kind that owns dir, when kind
// does not.
func (e *Env) otherOwner(kind, dir string) (string, bool) {
	if o, ok := e.Homes[kind].(setup.HomeOwner); ok && o.Owns(dir) {
		return "", false
	}
	for _, name := range slices.Sorted(maps.Keys(e.Homes)) {
		if o, ok := e.Homes[name].(setup.HomeOwner); ok && name != kind && o.Owns(dir) {
			return name, true
		}
	}
	return "", false
}

func (e *Env) installHomes(cfg config.Config) []string {
	byKind := e.homesByKind(cfg)
	var all []string
	for _, name := range agentNames(cfg) {
		all = append(all, byKind[name]...)
	}
	return cleanHomes(all)
}

func cleanHomes(dirs []string) []string {
	var out []string
	for _, d := range dirs {
		if d = cleanHome(d); !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	return out
}

func cleanHome(d string) string {
	d = filepath.Clean(config.ExpandHome(d))
	if abs, err := filepath.Abs(d); err == nil {
		return abs
	}
	return d
}

var absentHome = "does not exist; " + brand.Name + " installs only into existing homes"

// existingHomes splits dirs into the directories that exist and those that
// do not.
func existingHomes(dirs []string) (present, absent []string) {
	for _, d := range dirs {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			present = append(present, d)
		} else {
			absent = append(absent, d)
		}
	}
	return present, absent
}

type homesFlag []string

func (h *homesFlag) String() string     { return fmt.Sprint(*h) }
func (h *homesFlag) Set(v string) error { *h = append(*h, v); return nil }

// runInstall writes the loop's entries into agent homes. It runs in this
// process: agent homes are not the loop's store, and a
// rollback must work with the daemon down.
func runInstall(ctx context.Context, e *Env, args []string) error {
	fset := flag.NewFlagSet("install", flag.ContinueOnError)
	fset.SetOutput(e.Err)
	var homes homesFlag
	fset.Var(&homes, "home", "an agent home to install into, instead of every home "+brand.Name+" finds; repeatable")
	dryRun := fset.Bool("dry-run", false, "print what would change and change nothing")
	printSkill := fset.String("print-skill", "", "print the bundled skill with this name and exit")
	if err := fset.Parse(args); err != nil {
		return err
	}
	if fset.NArg() > 0 {
		return errors.New("usage: " + brand.Command + " install [--home <dir>]... [--dry-run] [--print-skill <name>]")
	}
	if *printSkill != "" {
		return e.printSkill(*printSkill)
	}
	cfg, err := e.loadConfig()
	if err != nil {
		return err
	}
	if _, err := e.homeKind(cfg.Agent); err != nil {
		return err
	}
	groups, err := e.installGroups(cfg, cleanHomes(homes))
	if err != nil {
		return err
	}
	for i, g := range groups {
		var absent []string
		groups[i].dirs, absent = existingHomes(g.dirs)
		for _, d := range absent {
			fmt.Fprintf(e.Out, "skipped %s: %s\n", d, absentHome)
		}
	}
	key, err := e.ensureMCPKey(*dryRun)
	if err != nil {
		return err
	}
	errs := []error{e.placeBundledPlugins(*dryRun)}
	var roots []string
	for _, g := range groups {
		kind, err := e.homeKind(g.kind)
		if err != nil {
			for _, d := range g.dirs {
				fmt.Fprintf(e.Out, "skipped %s: %v\n", d, err)
			}
			continue
		}
		spec := e.homeSpecFor(cfg, g.kind)
		if key != "" {
			spec.MCPKey = key
		}
		svc := setup.Service{Home: kind.New(spec)}
		lines, err := svc.Install(ctx, g.dirs, *dryRun)
		for _, l := range lines {
			fmt.Fprintln(e.Out, l)
		}
		errs = append(errs, err)
		for _, d := range g.dirs {
			if root := kind.SkillsRoot(d); root != "" {
				roots = append(roots, root)
			}
		}
	}
	errs = append(errs, e.installSkills(append(roots, filepath.Join(e.Home, ".agents")), *dryRun))
	return errors.Join(errs...)
}

type homeGroup struct {
	kind string
	dirs []string
}

// installGroups is the homes install writes, by kind: dirs by the kind
// whose homes hold each, else every home the loop finds. It refuses a dir
// another kind owns.
func (e *Env) installGroups(cfg config.Config, dirs []string) ([]homeGroup, error) {
	if len(dirs) == 0 {
		byKind := e.homesByKind(cfg)
		var out []homeGroup
		for _, name := range agentNames(cfg) {
			if len(byKind[name]) > 0 {
				out = append(out, homeGroup{name, byKind[name]})
			}
		}
		return out, nil
	}
	var out []homeGroup
	for _, d := range dirs {
		kind := e.kindOfHome(cfg, d)
		if other, ok := e.otherOwner(kind, d); ok {
			return nil, fmt.Errorf("refusing %s: it is a home of %s, not of %s", d, other, kind)
		}
		i := slices.IndexFunc(out, func(g homeGroup) bool { return g.kind == kind })
		if i < 0 {
			out = append(out, homeGroup{kind: kind})
			i = len(out) - 1
		}
		out[i].dirs = append(out[i].dirs, d)
	}
	return out, nil
}
