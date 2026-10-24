package cli

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/backup"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/ghcli"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/internal/termfmt"
)

var initUsage = "usage: " + brand.Command + ` init [--projects-dir <dir>] [--agent <name>] [--scope-repo <owner/repo>]... [--scope-team <org/slug>]...
  [--scope-person <login>]... [--profile <dir> | --profile-default] [--slack-client-id <id>] [--skip-slack]
  [--no-install] [--yes] [--color auto|always|never]`

// initDaemonWait bounds how long init waits for the daemon app install started.
var initDaemonWait = 15 * time.Second

// installableAgent is an agent that names itself and its install command.
type installableAgent interface {
	DisplayName() string
	InstallCommand() string
}

// agentInstall is the agent's name for a person and the command that
// installs it, or its registry name and "install <binary>".
func agentInstall(name string, a turns.Agent) (display, fix string) {
	if k, ok := a.(installableAgent); ok {
		return k.DisplayName(), k.InstallCommand()
	}
	return name, "install " + a.Binary()
}

type initFlags struct {
	projectsDir, agent, profile, slackClientID string
	profileDefault, skipSlack, noInstall, yes  bool
	repos, teams, people                       listFlag
	color                                      termfmt.Mode
	set                                        map[string]bool
}

func (f initFlags) scopeSet() bool {
	return f.set["scope-repo"] || f.set["scope-team"] || f.set["scope-person"]
}

// initAnswers are the values the wizard writes into the config.
type initAnswers struct {
	ProjectsDir string
	Agent       string
	// Profile is the personal profile directory, "" for the bundled one alone.
	Profile              string
	Repos, Teams, People []string
	SlackClientID        string
}

// initFacts are what the wizard reads before it asks.
type initFacts struct {
	cur           config.Config
	exists        bool
	agents        []string
	installed     []string
	dirSuggestion string
}

func parseInitFlags(e *Env, args []string) (initFlags, error) {
	fl := initFlags{set: map[string]bool{}}
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	fs.StringVar(&fl.projectsDir, "projects-dir", "", "where review clones a repository the workspace manager does not know")
	fs.StringVar(&fl.agent, "agent", "", "the coding agent that runs the turns")
	fs.Var(&fl.repos, "scope-repo", "a repository the loop follows, owner/repo; repeatable")
	fs.Var(&fl.teams, "scope-team", "a team whose review requests and pull requests the loop follows, org/slug; repeatable")
	fs.Var(&fl.people, "scope-person", "a login whose pull requests the loop follows; repeatable")
	fs.StringVar(&fl.profile, "profile", "", "a personal profile directory, created with example blocks when absent")
	fs.BoolVar(&fl.profileDefault, "profile-default", false, "use the bundled profile alone")
	fs.StringVar(&fl.slackClientID, "slack-client-id", "", "the client id of your Slack app")
	fs.BoolVar(&fl.skipSlack, "skip-slack", false, "leave Slack as it is")
	fs.BoolVar(&fl.noInstall, "no-install", false, "install into neither the agent homes nor the app")
	fs.BoolVar(&fl.yes, "yes", false, "say yes to every confirmation: overwrite the config, install, app install")
	fs.Var(&fl.color, "color", "style the output: auto, always or never")
	if err := fs.Parse(args); err != nil {
		return fl, usageError{usage: initUsage}
	}
	if fs.NArg() > 0 {
		return fl, usageError{usage: initUsage, why: fmt.Sprintf("init takes no argument, got %q", fs.Arg(0))}
	}
	fs.Visit(func(f *flag.Flag) { fl.set[f.Name] = true })
	switch {
	case fl.profile != "" && fl.profileDefault:
		return fl, usageError{usage: initUsage, why: "--profile and --profile-default exclude each other"}
	case fl.skipSlack && fl.slackClientID != "":
		return fl, usageError{usage: initUsage, why: "--skip-slack and --slack-client-id exclude each other"}
	}
	return fl, nil
}

// initDefaults are the answers the flags, the current config and the
// machine give, and, by key, the flag each value that has none needs.
func initDefaults(f initFacts, fl initFlags, home string) (initAnswers, map[string]string, error) {
	a := initAnswers{
		ProjectsDir:   f.cur.ProjectsDir,
		Profile:       f.cur.Profile,
		Repos:         f.cur.Scope.Repos,
		Teams:         f.cur.Scope.Teams,
		People:        f.cur.Scope.People,
		SlackClientID: f.cur.Slack.ClientID,
	}
	missing := map[string]string{}
	if fl.projectsDir != "" {
		a.ProjectsDir = absPath(fl.projectsDir, home)
	}
	if a.ProjectsDir == "" {
		hint := "--projects-dir <dir>"
		if f.dirSuggestion != "" {
			hint += " (" + f.dirSuggestion + " holds most of the repositories the workspace manager knows)"
		}
		missing["projects_dir"] = hint
	}

	switch {
	case fl.agent != "":
		if !slices.Contains(f.agents, fl.agent) {
			return a, nil, fmt.Errorf("--agent %s is not supported; supported: %s", fl.agent, strings.Join(f.agents, ", "))
		}
		if !slices.Contains(f.installed, fl.agent) {
			return a, nil, fmt.Errorf("--agent %s is not installed; installed: %s", fl.agent, strings.Join(f.installed, ", "))
		}
		a.Agent = fl.agent
	case f.exists && slices.Contains(f.installed, f.cur.Agent):
		a.Agent = f.cur.Agent
	case len(f.installed) == 1:
		a.Agent = f.installed[0]
	default:
		missing["agent"] = "--agent <name> (installed: " + strings.Join(f.installed, ", ") + ")"
	}

	switch {
	case fl.profileDefault:
		a.Profile = ""
	case fl.profile != "":
		a.Profile = absPath(fl.profile, home)
	case !f.exists:
		missing["profile"] = "--profile <dir> or --profile-default"
	}

	if fl.scopeSet() {
		a.Repos, a.Teams, a.People = cleanList(fl.repos), cleanList(fl.teams), cleanList(fl.people)
	}
	if fl.slackClientID != "" {
		a.SlackClientID = strings.TrimSpace(fl.slackClientID)
	}
	return a, missing, nil
}

func cleanList(l []string) []string {
	var out []string
	for _, v := range l {
		for _, x := range splitList(v) {
			if !slices.Contains(out, x) {
				out = append(out, x)
			}
		}
	}
	return out
}

// absPath is p with a leading ~ read as home, made absolute.
func absPath(p, home string) string {
	p = strings.TrimSpace(p)
	if p == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		p = filepath.Join(home, rest)
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// tilde writes p under home with a leading ~, for a person to read.
func tilde(p, home string) string {
	if home != "" && (p == home || strings.HasPrefix(p, home+string(filepath.Separator))) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

func missingError(missing map[string]string) error {
	var lines []string
	for _, k := range slices.Sorted(maps.Keys(missing)) {
		lines = append(lines, "  "+k+": "+missing[k])
	}
	return fmt.Errorf("init asks for these at a terminal, and standard input is not one; pass them as flags:\n%s", strings.Join(lines, "\n"))
}

type initWizard struct {
	e           *Env
	t           termfmt.Term
	p           *initPrompt
	interactive bool
	fl          initFlags
	// slackLogin is set when the user chose to log in to Slack after the write.
	slackLogin bool
}

func runInit(ctx context.Context, e *Env, args []string) error {
	fl, err := parseInitFlags(e, args)
	if err != nil {
		return err
	}
	t := e.term(fl.color)
	w := &initWizard{e: e, t: t, p: newInitPrompt(e, t), interactive: e.slackInteractive(), fl: fl}

	installed, err := w.prerequisites(ctx)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(e.ConfigPath)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	cur, err := config.LoadFile(e.ConfigPath)
	if err != nil {
		return fmt.Errorf("%w; fix it, then run '%s init' again", err, brand.Command)
	}
	facts := initFacts{cur: cur, exists: exists, agents: slices.Sorted(maps.Keys(e.Agents)), installed: installed,
		dirSuggestion: e.suggestProjectsDir(ctx)}
	a, missing, err := initDefaults(facts, fl, e.Home)
	if err != nil {
		return err
	}
	if w.interactive {
		if a, err = w.ask(ctx, facts, a); err != nil {
			return err
		}
	} else if len(missing) > 0 {
		return missingError(missing)
	}

	if err := w.write(raw, exists, a); err != nil {
		return err
	}
	cfg, err := e.readConfig()
	if err != nil {
		return err
	}
	w.slack(ctx, cfg)
	ranApp := w.install(ctx, cfg)
	w.doctor(ctx, ranApp)
	return nil
}

// prerequisites checks what doctor checks first and returns the agents
// installed; anything missing stops init with the command that fixes it.
func (w *initWizard) prerequisites(ctx context.Context) ([]string, error) {
	e := w.e
	var checks []check
	if err := ghcli.Authenticated(ctx, e.GH); err != nil {
		fix := "gh auth login"
		if errors.Is(err, exec.ErrNotFound) {
			fix = "brew install gh && gh auth login"
		}
		checks = append(checks, check{markMissing, "gh authenticated", err.Error(), fix})
	} else {
		checks = append(checks, check{markOK, "gh authenticated", "", ""})
	}

	agents := slices.Sorted(maps.Keys(e.Agents))
	binaries := map[string]string{}
	bins := []string{"git"}
	for _, name := range agents {
		binaries[name] = e.Agents[name].Binary()
		bins = append(bins, binaries[name])
	}
	found := e.loginPaths(ctx, uniq(bins...))
	if p, ok := found["git"]; ok {
		checks = append(checks, check{markOK, "git", p, ""})
	} else {
		fix := "install git"
		if goos == "darwin" {
			fix = "xcode-select --install"
		}
		checks = append(checks, check{markMissing, "git", "not found on the login shell's PATH", fix})
	}

	for _, m := range e.WS.All() {
		display := e.displayOf([]string{m.Name()})
		if err := m.Reachable(ctx); err != nil {
			checks = append(checks, check{markMissing, strings.ToLower(display) + " running", err.Error(), "open the " + display + " app"})
		} else {
			checks = append(checks, check{markOK, strings.ToLower(display) + " running", "", ""})
		}
	}

	var installed, fixes []string
	for _, name := range agents {
		if _, ok := found[binaries[name]]; ok {
			installed = append(installed, name)
		}
	}
	for _, name := range agents {
		display, fix := agentInstall(name, e.Agents[name])
		label := display + " installed"
		switch {
		case slices.Contains(installed, name):
			checks = append(checks, check{markOK, label, found[binaries[name]], ""})
		case len(installed) > 0:
			checks = append(checks, check{markInfo, label, "not found; " + brand.Name + " runs on the agents installed", ""})
		default:
			fixes = append(fixes, fix)
		}
	}
	if len(installed) == 0 {
		checks = append(checks, check{markMissing, "coding agent installed", "none of " + strings.Join(agents, ", ") + " is on the login shell's PATH", strings.Join(fixes, ", or ")})
	}

	sections := []checkSection{{"Prerequisites", checks}}
	if w.t.TTY {
		e.writeDoctor(w.t, sections)
		fmt.Fprintln(e.Out)
	} else {
		writeDoctorPlain(e.Out, sections)
	}
	if n := pending(sections); n > 0 {
		return installed, fmt.Errorf("%d prerequisite(s) missing; fix them, then run '%s init' again", n, brand.Command)
	}
	return installed, nil
}

// ask asks each question the flags leave open, each with its default.
func (w *initWizard) ask(ctx context.Context, f initFacts, a initAnswers) (initAnswers, error) {
	e, fl := w.e, w.fl
	out := e.Out
	if !fl.set["projects-dir"] {
		def := tilde(cmp.Or(a.ProjectsDir, f.dirSuggestion), e.Home)
		for a.ProjectsDir = ""; a.ProjectsDir == ""; {
			if v := w.p.line("Where should review clone a repository "+e.managerDisplay()+" does not know (projects_dir)?", def); v != "" {
				a.ProjectsDir = absPath(v, e.Home)
			} else if w.p.eof {
				return a, errors.New("standard input ended before projects_dir had a value")
			}
		}
	}

	if !fl.set["agent"] {
		switch {
		case len(f.installed) == 1:
			a.Agent = f.installed[0]
			fmt.Fprintf(out, "Agent: %s, the only one installed.\n", a.Agent)
		default:
			a.Agent = w.p.choose("Which agent runs the turns?", f.installed, cmp.Or(a.Agent, f.installed[0]))
		}
	}

	if !fl.scopeSet() {
		a = w.askScope(ctx, f, a)
	}

	if !fl.set["profile"] && !fl.set["profile-default"] {
		def := "default"
		if a.Profile != "" {
			def = "personal"
		}
		fmt.Fprintln(out, "Review profile: 'default' is the bundled judgment alone; 'personal' also reads a directory of your own rules,")
		fmt.Fprintln(out, "created with an example block per intent.")
		if w.p.choose("Profile", []string{"default", "personal"}, def) == "personal" {
			dir := cmp.Or(a.Profile, filepath.Join(filepath.Dir(e.ConfigPath), "profiles", "personal"))
			a.Profile = absPath(w.p.line("Profile directory", tilde(dir, e.Home)), e.Home)
		} else {
			a.Profile = ""
		}
	}

	switch {
	case fl.skipSlack:
	case fl.slackClientID != "":
		w.slackLogin = true
	default:
		a.SlackClientID = w.askSlack(f.cur.Slack)
		w.slackLogin = a.SlackClientID != "" && (a.SlackClientID != f.cur.Slack.ClientID || !w.slackDone(f.cur.Slack))
	}
	return a, nil
}

func (w *initWizard) slackDone(s config.Slack) bool {
	st := w.e.slackState(s)
	return st.appToken && st.login != ""
}

func (w *initWizard) askScope(ctx context.Context, f initFacts, a initAnswers) initAnswers {
	out := w.e.Out
	fmt.Fprintln(out, "Scope: besides your own pull requests and your review requests, "+brand.Name+" follows the pull")
	fmt.Fprintln(out, "requests of these repositories, of these teams' members and review requests, and of these people.")
	repos, rerr := w.e.reviewedRepos(ctx)
	teams, terr := w.e.userTeams(ctx)
	if len(repos) > 0 {
		fmt.Fprintf(out, "  %s %s\n", w.t.Dim("you reviewed in the last 30 days:"), strings.Join(repos, ", "))
	}
	if len(teams) > 0 {
		fmt.Fprintf(out, "  %s %s\n", w.t.Dim("your teams:"), strings.Join(teams, ", "))
	}
	for _, err := range []error{rerr, terr} {
		if err != nil {
			fmt.Fprintf(out, "  %s\n", w.t.Dim("no suggestion from gh: "+oneLine(err.Error())))
		}
	}
	defRepos := a.Repos
	if !f.exists && len(defRepos) == 0 {
		defRepos = repos
	}
	fmt.Fprintln(out, "Lists are comma separated; '-' is none.")
	a.Repos = w.p.list("Repositories (scope.repos, owner/repo)", defRepos)
	a.Teams = w.p.list("Teams (scope.teams, org/slug)", a.Teams)
	a.People = w.p.list("People (scope.people, logins)", a.People)
	return a
}

// confirm asks at a terminal, defaulting to yes; off one, --yes answers.
func (w *initWizard) confirm(question string) bool {
	if w.interactive {
		return w.p.yes(question, true)
	}
	return w.fl.yes
}

// write creates the directories the answers name and writes the config
// when it differs, after showing the diff.
func (w *initWizard) write(raw []byte, exists bool, a initAnswers) error {
	e := w.e
	next, err := mergeInitConfig(raw, a)
	if err != nil {
		return fmt.Errorf("%s: %w", e.ConfigPath, err)
	}
	changed := !exists || !sameJSON(raw, next)
	if changed {
		var before []byte
		if exists {
			before = indentJSON(raw)
		}
		w.printDiff(profile.Diff(profile.Change{Rel: filepath.Base(e.ConfigPath), Before: before, After: next}))
		switch {
		case w.interactive && !w.p.yes("Write "+tilde(e.ConfigPath, e.Home)+"?", true):
			return errors.New("left the config as it was")
		case !w.interactive && exists && !w.fl.yes:
			return fmt.Errorf("%s exists and differs; pass --yes to overwrite it", e.ConfigPath)
		}
	}
	if err := w.ensureDirs(a); err != nil {
		return err
	}
	if !changed {
		fmt.Fprintf(e.Out, "%s is up to date.\n", tilde(e.ConfigPath, e.Home))
		return nil
	}
	if exists {
		name, err := backup.File(e.ConfigPath, raw, e.Now().Format("20060102-150405"))
		if err != nil {
			return err
		}
		fmt.Fprintf(e.Out, "Kept the previous config in %s.\n", tilde(name, e.Home))
	}
	if err := writeAtomic(e.ConfigPath, next); err != nil {
		return err
	}
	fmt.Fprintf(e.Out, "Wrote %s.\n", tilde(e.ConfigPath, e.Home))
	return nil
}

func (w *initWizard) printDiff(d string) {
	for _, l := range strings.Split(strings.TrimSuffix(d, "\n"), "\n") {
		switch {
		case strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"):
			l = w.t.Bold(l)
		case strings.HasPrefix(l, "+"):
			l = w.t.Green(l)
		case strings.HasPrefix(l, "-"):
			l = w.t.Red(l)
		case strings.HasPrefix(l, "@@"):
			l = w.t.Cyan(l)
		}
		fmt.Fprintln(w.e.Out, l)
	}
}

func (w *initWizard) ensureDirs(a initAnswers) error {
	e := w.e
	if fi, err := os.Stat(a.ProjectsDir); err != nil || !fi.IsDir() {
		if err := os.MkdirAll(a.ProjectsDir, 0o755); err != nil {
			return err
		}
		fmt.Fprintf(e.Out, "Created %s.\n", tilde(a.ProjectsDir, e.Home))
	}
	if a.Profile == "" {
		return nil
	}
	if entries, err := os.ReadDir(a.Profile); err != nil || len(entries) == 0 {
		wrote, err := writeProfileExamples(a.Profile)
		if err != nil {
			return err
		}
		for _, f := range wrote {
			fmt.Fprintf(e.Out, "Wrote the example block %s.\n", tilde(f, e.Home))
		}
	}
	if err := e.checkProfile(a.Profile); err != nil {
		return fmt.Errorf("the profile %s does not load: %w", a.Profile, err)
	}
	return nil
}

func writeAtomic(file string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	tmp := file + ".init-tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

// slack saves the Slack tokens at a terminal, or prints the commands that do.
func (w *initWizard) slack(ctx context.Context, cfg config.Config) {
	e := w.e
	if !cfg.Slack.Configured() || w.fl.skipSlack {
		return
	}
	if w.interactive {
		if !w.slackLogin {
			return
		}
		if err := w.finishSlack(ctx, cfg); err != nil {
			fmt.Fprintf(e.Out, "%s %v; '%s slack login' tries again.\n", w.t.Red("Slack:"), err, brand.Command)
		}
		return
	}
	if w.slackDone(cfg.Slack) {
		return
	}
	fmt.Fprintln(e.Out, "To finish Slack, run at a terminal:")
	for _, c := range slackNextSteps() {
		fmt.Fprintln(e.Out, "  "+c)
	}
}

// install offers install and, on macOS, app install when doctor's checks of
// them have something pending; it reports whether app install ran.
func (w *initWizard) install(ctx context.Context, cfg config.Config) bool {
	e := w.e
	steps := []initStep{{pending(checkSectionOf(e.homeChecks(ctx, cfg))) > 0, "install",
		"Install into your agent homes: the MCP registration, the hooks, the turn_end permission and the skills, backing up each file first?",
		func() error { return runInstall(ctx, e, nil) }}}
	if goos == "darwin" {
		bundle := e.bundleCheck(ctx)
		steps = append(steps, initStep{pending(checkSectionOf(e.timerChecks(ctx))) > 0 || bundle.mark != markOK, "app install",
			"Install " + brand.App + ".app, the menu bar, and the LaunchAgents that run the daemon and the menu bar at login?",
			func() error { return runAppInstall(ctx, e, nil) }})
	}
	ranApp := false
	for _, s := range steps {
		cmd := brand.Command + " " + s.command
		switch {
		case !s.pending:
			fmt.Fprintf(e.Out, "'%s': nothing to do.\n", cmd)
		case w.fl.noInstall || !w.confirm(s.question):
			fmt.Fprintf(e.Out, "Skipped; run '%s' when you want it.\n", cmd)
		default:
			if err := s.run(); err != nil {
				fmt.Fprintf(e.Out, "%s %v\n", w.t.Red("'"+cmd+"' failed:"), err)
				continue
			}
			ranApp = ranApp || s.command == "app install"
		}
	}
	return ranApp
}

type initStep struct {
	pending  bool
	command  string
	question string
	run      func() error
}

func checkSectionOf(checks []check) []checkSection { return []checkSection{{"", checks}} }

// doctor runs doctor and names the next step.
func (w *initWizard) doctor(ctx context.Context, waitDaemon bool) {
	e := w.e
	if waitDaemon {
		for deadline := time.Now().Add(initDaemonWait); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
			if _, err := e.daemonClient(ctx); !errors.Is(err, errDaemonDown) {
				break
			}
		}
	}
	fmt.Fprintln(e.Out)
	err := runDoctor(ctx, e, []string{"--color", w.fl.color.String()})
	switch {
	case errors.Is(err, errDaemonDown):
		fmt.Fprintf(e.Out, "doctor asks the daemon, which is not running: %v\n", err)
	case err != nil:
		fmt.Fprintf(e.Out, "doctor: %v; fix what it lists, then run '%s doctor' again.\n", err, brand.Command)
	}
	fmt.Fprintf(e.Out, "Next: '%s review <pull request url>' starts your first review.\n", brand.Command)
}
