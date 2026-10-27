package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/setup"
	"github.com/wloophq/wloop/internal/slack/auth"
)

var uninstallUsage = "usage: " + brand.Command + " uninstall [--purge] [--dry-run] [--yes]"

// uninstallInteractive says whether e reads from a terminal; tests replace it.
var uninstallInteractive = func(e *Env) bool {
	f, ok := e.In.(*os.File)
	if e.In == nil {
		f, ok = os.Stdin, true
	}
	return ok && term.IsTerminal(int(f.Fd()))
}

// uninstallStep is one thing uninstall takes out: the lines the plan lists
// and the run that removes it, printing what it did.
type uninstallStep struct {
	plan []string
	run  func(ctx context.Context) error
}

type uninstallPlan struct {
	remove, purge []uninstallStep
	// kept are what uninstall leaves and why.
	kept []string
}

func (p uninstallPlan) empty() bool { return len(p.remove) == 0 && len(p.purge) == 0 }

// runUninstall reverses install and app install; with
// --purge it also deletes the state and config directories. It never
// removes the package itself.
func runUninstall(ctx context.Context, e *Env, args []string) error {
	fset := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fset.SetOutput(e.Err)
	purge := fset.Bool("purge", false, "also delete the state and config directories: the store and its backups, MCP key, dossiers, logs, config and review profiles")
	dryRun := fset.Bool("dry-run", false, "print what would be removed and change nothing")
	yes := fset.Bool("yes", false, "remove without asking")
	if err := fset.Parse(args); err != nil {
		return err
	}
	if fset.NArg() > 0 {
		return usageError{usage: uninstallUsage}
	}
	p := e.uninstallPlan(ctx, *purge)
	switch {
	case p.empty():
		e.printLines("Left:", p.kept)
		fmt.Fprintf(e.Out, "Nothing of %s is left to remove.\n", brand.Name)
	case *dryRun:
		e.printPlan(p)
	default:
		if !*yes {
			if err := e.confirmUninstall(p); err != nil {
				return err
			}
		}
		if err := e.applyUninstall(ctx, p); err != nil {
			e.printBrewHint()
			return err
		}
		e.printLines("Left:", p.kept)
	}
	e.printBrewHint()
	return nil
}

func (e *Env) printPlan(p uninstallPlan) {
	var remove, purge []string
	for _, s := range p.remove {
		remove = append(remove, s.plan...)
	}
	for _, s := range p.purge {
		purge = append(purge, s.plan...)
	}
	e.printLines("Would remove:", remove)
	e.printLines("Would delete (--purge):", purge)
	e.printLines("Would leave:", p.kept)
}

func (e *Env) printLines(heading string, lines []string) {
	if len(lines) == 0 {
		return
	}
	fmt.Fprintln(e.Out, heading)
	for _, l := range lines {
		fmt.Fprintf(e.Out, "  %s\n", l)
	}
}

func (e *Env) printBrewHint() {
	fmt.Fprintf(e.Out, "%s itself stays installed; to remove it: brew uninstall --cask %s\n", brand.Command, brand.Name)
}

func (e *Env) confirmUninstall(p uninstallPlan) error {
	if !uninstallInteractive(e) {
		return errors.New("uninstall asks before removing anything; pass --yes when standard input is not a terminal, or --dry-run to see what it removes")
	}
	e.printPlan(p)
	fmt.Fprint(e.Out, "Proceed? [y/N] ")
	in := e.In
	if in == nil {
		in = os.Stdin
	}
	answer, _ := bufio.NewReader(in).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
		fmt.Fprintln(e.Out, "Nothing removed.")
		return exitCode(1)
	}
	return nil
}

// applyUninstall runs every removal, then the purge unless a daemon still
// answers: its store and socket live in the state directory.
func (e *Env) applyUninstall(ctx context.Context, p uninstallPlan) error {
	var errs []error
	for _, s := range p.remove {
		errs = append(errs, s.run(ctx))
	}
	if len(p.purge) > 0 {
		if _, err := e.daemonClient(ctx); !errors.Is(err, errDaemonDown) {
			errs = append(errs, fmt.Errorf("a daemon still answers on %s, so the data stays; stop the '%s serve' you started by hand and run '%s uninstall --purge' again", e.Socket, brand.Command, brand.Command))
			return errors.Join(errs...)
		}
		for _, s := range p.purge {
			errs = append(errs, s.run(ctx))
		}
	}
	return errors.Join(errs...)
}

func (e *Env) uninstallPlan(ctx context.Context, purge bool) uninstallPlan {
	var p uninstallPlan
	cfg, err := e.loadConfig()
	if err != nil {
		fmt.Fprintf(e.Err, "warning: %v; uninstall looks for agent homes with the defaults\n", err)
	}
	p.remove = append(p.remove, e.launchAgentSteps(ctx)...)
	p.remove = append(p.remove, e.bundleSteps()...)
	homes, roots, kept := e.homeSteps(ctx, cfg)
	p.remove = append(p.remove, homes...)
	p.kept = append(p.kept, kept...)
	skillSteps, kept := e.skillSteps(roots)
	p.remove = append(p.remove, skillSteps...)
	p.kept = append(p.kept, kept...)
	p.remove = append(p.remove, e.slackSteps(cfg)...)
	if purge {
		p.purge, kept = e.purgeSteps(cfg)
		p.kept = append(p.kept, kept...)
	}
	return p
}

// launchAgentSteps stop the menu bar before the daemon it is a client of.
func (e *Env) launchAgentSteps(ctx context.Context) []uninstallStep {
	var out []uninstallStep
	for _, a := range []launchAgent{appAgentOf(""), daemonAgent("")} {
		label, path := a.label, e.agentPath(a.label)
		_, statErr := os.Stat(path)
		loaded := goos == "darwin" && e.jobLoaded(ctx, label)
		if statErr != nil && !loaded {
			continue
		}
		what := fmt.Sprintf("LaunchAgent %s (%s)", label, path)
		if statErr != nil {
			what = fmt.Sprintf("LaunchAgent %s, loaded without its plist", label)
		}
		out = append(out, uninstallStep{plan: []string{what}, run: func(ctx context.Context) error {
			if goos == "darwin" {
				e.bootOut(ctx, label)
			}
			if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			fmt.Fprintf(e.Out, "stopped and removed %s\n", what)
			return nil
		}})
	}
	return out
}

func (e *Env) jobLoaded(ctx context.Context, label string) bool {
	_, err := e.Sh(ctx, "launchctl", "print", launchDomain()+"/"+label)
	return err == nil
}

func (e *Env) bundleSteps() []uninstallStep {
	app := bundlePath(e.Home)
	if _, err := os.Lstat(app); err != nil {
		return nil
	}
	return []uninstallStep{{plan: []string{"app bundle " + app}, run: func(ctx context.Context) error {
		if goos == "darwin" {
			_, _ = e.Sh(ctx, lsregister, "-u", app)
		}
		if err := os.RemoveAll(app); err != nil {
			return err
		}
		fmt.Fprintf(e.Out, "removed %s\n", app)
		return nil
	}}}
}

// homeSteps take the MCP registration, hooks and permissions out of every
// agent home install writes. roots are the homes' skills roots.
func (e *Env) homeSteps(ctx context.Context, cfg config.Config) (steps []uninstallStep, roots, kept []string) {
	groups, err := e.installGroups(cfg, nil)
	if err != nil {
		return nil, nil, []string{"the agent homes' entries: " + err.Error()}
	}
	for _, g := range groups {
		kind, err := e.homeKind(g.kind)
		if err != nil {
			continue
		}
		dirs, _ := existingHomes(g.dirs)
		for _, d := range dirs {
			if r := kind.SkillsRoot(d); r != "" {
				roots = append(roots, r)
			}
		}
		if len(dirs) == 0 {
			continue
		}
		svc := setup.Service{Home: kind.New(e.homeSpecFor(cfg, g.kind))}
		lines, err := svc.Remove(ctx, dirs, true)
		if err != nil {
			kept = append(kept, fmt.Sprintf("the entries of %s: %v", strings.Join(dirs, ", "), err))
			continue
		}
		if len(lines) == 0 {
			continue
		}
		for i, l := range lines {
			lines[i] = strings.TrimPrefix(l, "would ")
		}
		steps = append(steps, uninstallStep{plan: lines, run: func(ctx context.Context) error {
			done, err := svc.Remove(ctx, dirs, false)
			for _, l := range done {
				fmt.Fprintln(e.Out, l)
			}
			return err
		}})
	}
	return steps, append(roots, filepath.Join(e.Home, ".agents")), kept
}

// skillSteps remove each bundled skill an install of any version wrote and
// nobody edited since, once per skills directory; any other stays.
func (e *Env) skillSteps(roots []string) (steps []uninstallStep, kept []string) {
	seen := map[string]bool{}
	for _, r := range roots {
		root := skillsRoot(r)
		if seen[root] {
			continue
		}
		seen[root] = true
		for _, sk := range bundledSkills() {
			dir := filepath.Join(root, sk.Name)
			path := filepath.Join(dir, "SKILL.md")
			in, err := e.installedSkill(root, sk)
			switch {
			case err != nil:
				kept = append(kept, fmt.Sprintf("skill %s at %s: %v", sk.Name, path, err))
				continue
			case in.state == skillAbsent:
				continue
			case in.state == skillLinked:
				kept = append(kept, fmt.Sprintf("skill %s at %s: a symlink to content %s does not own", sk.Name, in.path, brand.Name))
				continue
			case in.state == skillEdited || in.state == skillForeign:
				kept = append(kept, fmt.Sprintf("skill %s at %s: %s", sk.Name, path, e.skillKept(sk.Name, in)))
				continue
			}
			name := sk.Name
			steps = append(steps, uninstallStep{plan: []string{fmt.Sprintf("skill %s (%s)", name, dir)}, run: func(context.Context) error {
				if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return err
				}
				if err := os.Remove(dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
					fmt.Fprintf(e.Out, "removed skill %s from %s; kept the directory, which holds other files\n", name, path)
					return nil
				}
				fmt.Fprintf(e.Out, "removed skill %s (%s)\n", name, dir)
				return nil
			}})
		}
	}
	return steps, kept
}

// slackSteps revoke and delete the stored Slack tokens, as slack logout
// does. Off macOS there is no login Keychain to hold them.
func (e *Env) slackSteps(cfg config.Config) []uninstallStep {
	if goos != "darwin" && e.slack.Keychain == nil {
		return nil
	}
	kit := e.slack.kit(cfg.Slack, func(format string, a ...any) { fmt.Fprintf(e.Err, format+"\n", a...) })
	_, userErr := kit.Store.Load()
	_, appErr := kit.Store.AppToken()
	if errors.Is(userErr, auth.ErrNotLoggedIn) && errors.Is(appErr, auth.ErrNotLoggedIn) {
		return nil
	}
	return []uninstallStep{{plan: []string{"the Slack tokens in Keychain service " + kit.Store.Service + ", revoked first"}, run: func(ctx context.Context) error {
		return e.slackLogout(ctx, kit, nil)
	}}}
}

// purgeSteps delete the state and config directories. A review profile
// outside the config directory is the user's and stays.
func (e *Env) purgeSteps(cfg config.Config) (steps []uninstallStep, kept []string) {
	configDir := filepath.Dir(e.ConfigPath)
	for _, d := range []struct{ dir, holds string }{
		{e.StateDir, "the store and its backups, MCP key, dossiers and logs"},
		{configDir, "config.toml and the review profiles in it"},
	} {
		dir := filepath.Clean(d.dir)
		if _, err := os.Lstat(dir); err != nil {
			continue
		}
		if dir == filepath.Clean(e.Home) || dir == filepath.Dir(dir) {
			kept = append(kept, dir+": refusing to delete it")
			continue
		}
		steps = append(steps, uninstallStep{plan: []string{dir + ": " + d.holds}, run: func(context.Context) error {
			if err := os.RemoveAll(dir); err != nil {
				return err
			}
			fmt.Fprintf(e.Out, "deleted %s\n", dir)
			return nil
		}})
	}
	if prof := cfg.Profile; prof != "" && !within(prof, configDir) {
		if _, err := os.Stat(prof); err == nil {
			kept = append(kept, fmt.Sprintf("review profile %s: it lives outside %s", prof, configDir))
		}
	}
	return steps, kept
}
