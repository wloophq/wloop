package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/setup"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/ghcli"
	"github.com/wloophq/wloop/internal/gitx"
	"github.com/wloophq/wloop/internal/termfmt"
)

type mark string

const (
	markOK      mark = "ok     "
	markMissing mark = "missing"
	markInfo    mark = "info   "
)

type check struct {
	mark mark
	name string
	info string
	fix  string
}

// runDoctorCommand runs doctor, or with --load the daemon's meter.
func runDoctorCommand(ctx context.Context, e *Env, args []string) error {
	if slices.ContainsFunc(args, isFlag("load")) {
		return runDoctorLoad(ctx, e, args)
	}
	return runDoctor(ctx, e, args)
}

func isFlag(names ...string) func(string) bool {
	return func(arg string) bool {
		name, _, _ := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		return strings.HasPrefix(arg, "-") && slices.Contains(names, name)
	}
}

// runDoctorLoad prints what the daemon has spent since it started, per scope.
func runDoctorLoad(ctx context.Context, e *Env, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	fs.Bool("load", false, "print the metered cost of every job, route and loop since the daemon started")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	var color termfmt.Mode
	fs.Var(&color, "color", "style the report: auto, always or never")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("doctor --load takes no argument, got %q", fs.Arg(0))
	}
	c, err := e.daemonClient(ctx)
	if err != nil {
		return err
	}
	rep, err := c.Meter(ctx)
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(e.Out)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	if t := e.term(color); t.TTY {
		e.writeLoad(t, rep)
		return nil
	}
	fmt.Fprintf(e.Out, "since %s\n", rep.Since.Local().Format("2006-01-02 15:04"))
	for _, sc := range rep.Scopes {
		fmt.Fprintf(e.Out, "%s: %d runs, %s\n", sc.Name, sc.Runs, statsLine(sc.Stats))
	}
	return nil
}

func statsLine(stats []api.MeterStat) string {
	byRes := map[string]api.MeterStat{}
	for _, st := range stats {
		t := byRes[st.Res]
		t.Calls += st.Calls
		t.Spawns += st.Spawns
		t.Points += st.Points
		byRes[st.Res] = t
	}
	var parts []string
	for _, res := range slices.Sorted(maps.Keys(byRes)) {
		t := byRes[res]
		part := fmt.Sprintf("%d %s", t.Calls, res)
		if t.Spawns > 0 {
			part += fmt.Sprintf(" (%d spawns)", t.Spawns)
		}
		if t.Points > 0 {
			part += fmt.Sprintf(" (%d pts)", t.Points)
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return "nothing metered"
	}
	return strings.Join(parts, ", ")
}

// runDoctor inspects every prerequisite and prints the command that fixes
// whatever is missing. It changes nothing.
func runDoctor(ctx context.Context, e *Env, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	var color termfmt.Mode
	fs.Var(&color, "color", "style the report: auto, always or never")
	liveGit := fs.Bool("git", false, "also check git access live with an ls-remote, as the daemon would run it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("doctor takes no argument, got %q; --repo <owner/repo> checks a repository", fs.Arg(0))
	}
	var sections []checkSection
	var checks []check
	add := func(m mark, name, info, fix string) { checks = append(checks, check{m, name, info, fix}) }
	section := func(title string, more ...check) {
		sections = append(sections, checkSection{title, append(checks, more...)})
		checks = nil
	}

	daemon, daemonErr := e.daemonClient(ctx)
	if errors.Is(daemonErr, errDaemonDown) {
		return e.downWithStore(daemonErr)
	}
	if daemonErr == nil {
		if only, ok := e.waitingStore(ctx, daemon); ok {
			return e.reportDoctor(color, only)
		}
	}
	cfg, cfgErr := e.loadConfig()

	// The shell commands stay on one goroutine: Sh and Stdout need not be safe
	// for concurrent use. The git fetch starts before the manager answers and
	// is dropped when it does not.
	authErr := async(func() error { return ghcli.Authenticated(ctx, e.GH) })
	reach := async(func() map[string]error { return e.WS.Unreachable(ctx) })
	sshCtx, cancelSSH := context.WithCancel(ctx)
	defer cancelSSH()
	ssh := async(func() liveGitCheck {
		if !*liveGit {
			return liveGitCheck{}
		}
		return e.sshCheck(sshCtx, cfg)
	})
	shell := async(func() shellChecks {
		var s shellChecks
		s.timers = e.timerChecks(ctx)
		if goos == "darwin" {
			s.bundle = []check{e.bundleCheck(ctx)}
		}
		s.paths = e.pathChecks(ctx, cfg)
		return s
	})
	daemonChecks := async(func() daemonReport { return e.daemonChecks(ctx, cfg, daemon, daemonErr) })

	if err := authErr(); err != nil {
		add(markMissing, "gh authenticated", err.Error(), "gh auth login")
	} else {
		add(markOK, "gh authenticated", "", "")
	}

	display := e.managerDisplay()
	unreachable := reach()
	var reachErr error
	if len(unreachable) == e.WS.Len() {
		reachErr = e.WS.Reachable(ctx)
		cancelSSH()
	}
	for _, m := range e.WS.All() {
		name := e.displayOf([]string{m.Name()})
		reachable := strings.ToLower(name) + " reachable"
		switch err := unreachable[m.Name()]; {
		case err == nil:
			add(markOK, reachable, "", "")
		case reachErr != nil:
			add(markMissing, reachable, err.Error(), "open the "+name+" app")
		default:
			add(markInfo, reachable, err.Error()+"; its worktrees and sessions wait until it is back, and new ones go to the next manager", "open the "+name+" app")
		}
	}
	section("GitHub and " + display)

	switch {
	case errors.As(cfgErr, new(*config.UnknownKeysError)):
		add(markMissing, "config", cfgErr.Error(), "remove or rename those keys in "+e.ConfigPath+"; the configuration reference lists every key")
	case cfgErr != nil:
		add(markMissing, "config", cfgErr.Error(), "fix the JSON in "+e.ConfigPath)
	default:
		where := e.ConfigPath
		if _, err := os.Stat(e.ConfigPath); err != nil {
			where += " (absent, using defaults; '" + brand.Command + " init' writes it)"
		}
		add(markOK, "config", where, "")
	}
	add(markInfo, "effective config", e.effectiveConfig(cfg), "")
	if cfgErr == nil {
		for _, c := range e.managerChecks(ctx, cfg) {
			add(c.mark, c.name, c.info, c.fix)
		}
	}

	switch cfg.ProjectsDir {
	case "":
		add(markInfo, "projects_dir", "not set; 'review' needs it only to clone a repository "+display+" does not know yet", "")
	default:
		if fi, err := os.Stat(cfg.ProjectsDir); err != nil || !fi.IsDir() {
			add(markMissing, "projects_dir", cfg.ProjectsDir+" does not exist", "mkdir -p "+cfg.ProjectsDir)
		} else {
			add(markOK, "projects_dir", cfg.ProjectsDir, "")
		}
	}
	section("Config")
	section("Slack", e.slackChecks(cfg)...)
	section(homesSection, e.homeChecks(ctx, cfg)...)
	section("Profile and intents", slices.Concat(e.agentModelChecks(cfg), e.restrictedChecks(ctx, cfg), e.profileChecks(cfg), e.intentChecks(), e.projectChecks(cfg))...)

	sh := shell()
	dr := daemonChecks()
	checks = slices.Concat(sh.timers, sh.bundle, dr.checks)
	section("Daemon")
	section("Workflows", dr.workflows...)
	checks = sh.paths
	live := ssh()
	if *liveGit && reachErr == nil {
		checks = append(checks, live.check)
		if live.record != nil && daemon != nil {
			_ = daemon.RecordGitAccess(ctx, *live.record)
		}
	}
	if dr.health != nil {
		if reachErr != nil {
			live.record = nil
		}
		checks = append(checks, e.gitAccessChecks(dr.health.GitAccess, live.record)...)
	}
	section("Login shell")
	checks = append(checks, e.storeChecks()...)
	if e.boardSink(cfg) != "" {
		col := func(status string) string { return e.column(cfg, status) }
		cols := uniq(col(config.ColumnWorking), col(config.ColumnNeedsMe), col(config.ColumnReviewed), col(config.ColumnWorkingFallback), col(config.ColumnReady),
			col(config.ColumnInProgress), col(config.ColumnInReview), col(config.ColumnCompleted))
		add(markInfo, "board columns "+strings.Join(cols, ", "), "not verifiable without writing; the daemon's log names a missing column when a turn starts", "")
	}

	section("Store and board")
	section("Disk", e.diskChecks(ctx, cfg)...)

	return e.reportDoctor(color, sections)
}

func (e *Env) reportDoctor(color termfmt.Mode, sections []checkSection) error {
	sections = dedupe(sections)
	t := e.term(color)
	if t.TTY {
		e.writeDoctor(t, sections)
	} else {
		writeDoctorPlain(e.Out, sections)
	}
	if failed := pending(sections); failed > 0 {
		return fmt.Errorf("%d check(s) pending", failed)
	}
	done := "All set."
	if t.TTY {
		done = t.Green(done)
	}
	fmt.Fprintln(e.Out, done+" '"+brand.Command+" status' lists what waits on whom; '"+brand.Command+" open' opens the cockpit.")
	return nil
}

type shellChecks struct{ timers, bundle, paths []check }

// async starts f and returns its result's getter, which waits for it; call
// the getter from one goroutine only.
func async[T any](f func() T) func() T {
	ch := make(chan T, 1)
	go func() { ch <- f() }()
	var (
		v    T
		done bool
	)
	return func() T {
		if !done {
			v, done = <-ch, true
		}
		return v
	}
}

func (e *Env) effectiveConfig(c config.Config) string {
	parts := []string{fmt.Sprintf("agent=%s review_prefix=%q", c.Agent, c.ReviewPrefix)}
	if sink := e.boardSink(c); sink != "" {
		for _, status := range []string{config.ColumnWorking, config.ColumnWorkingFallback, config.ColumnReady} {
			parts = append(parts, fmt.Sprintf("views.%s.columns.%s=%s", sink, status, c.Column(sink, status)))
		}
	}
	parts = append(parts, fmt.Sprintf("rounds.per_pr_per_day=%d automated_reviewers=%v owners=%v brief=%v",
		c.Rounds.PerPRPerDay, c.AutomatedReviewers, c.Owners, c.Brief.Enabled))
	return strings.Join(parts, " ")
}

// homeChecks reports, per agent home, each entry the loop needs and its
// bundled skills, as the home's kind sees them.
func (e *Env) homeChecks(ctx context.Context, cfg config.Config) []check {
	if _, err := e.homeKind(cfg.Agent); err != nil {
		return []check{{markInfo, "agent homes", err.Error(), ""}}
	}
	byKind := e.homesByKind(cfg)
	var out []check
	for _, name := range agentNames(cfg) {
		kind, err := e.homeKind(name)
		if err != nil {
			continue
		}
		out = append(out, e.kindHomeChecks(ctx, cfg, name, kind, byKind[name])...)
	}
	return append(out, e.skillCheck(filepath.Join(e.Home, ".agents"), e.loopCommand()+" install"))
}

func (e *Env) kindHomeChecks(ctx context.Context, cfg config.Config, name string, kind setup.HomeKind, homes []string) []check {
	dirs, absent := existingHomes(homes)
	var out []check
	for _, d := range absent {
		out = append(out, check{markInfo, "agent home " + d, absentHome, ""})
	}
	findings, err := setup.Service{Home: kind.New(e.homeSpecFor(cfg, name))}.Check(ctx, dirs)
	for _, f := range findings {
		name := "agent home " + f.Home + ": " + f.Entry
		switch {
		case f.Problem == "":
			out = append(out, check{markOK, name, "", ""})
		case f.Fix == "":
			out = append(out, check{markInfo, name, f.Problem, ""})
		default:
			out = append(out, check{markMissing, name, f.Problem, f.Fix})
		}
	}
	if err != nil {
		out = append(out, check{markMissing, "agent homes", err.Error(), ""})
	}
	for _, d := range dirs {
		if root := kind.SkillsRoot(d); root != "" {
			out = append(out, e.skillCheck(root, setup.InstallCommand(e.loopCommand(), d)))
		}
	}
	return out
}

// skillCheck covers the bundled skills in one home, which run a turn by hand
// and let a session drive the loop; fix is the command that installs them.
func (e *Env) skillCheck(dir, fix string) check {
	name := "skills in " + dir
	root := skillsRoot(dir)
	var missing, outdated, kept, linked []string
	for _, sk := range bundledSkills() {
		in, err := e.installedSkill(root, sk)
		switch {
		case err != nil:
			missing = append(missing, sk.Name+" ("+err.Error()+")")
		case in.state == skillAbsent:
			missing = append(missing, sk.Name)
		case in.state == skillLinked:
			linked = append(linked, in.path)
		case in.state == skillManaged:
			outdated = append(outdated, sk.Name)
		case in.state == skillEdited || in.state == skillForeign:
			kept = append(kept, sk.Name+" ("+e.skillKept(sk.Name, in)+")")
		}
	}
	var parts []string
	if len(missing) > 0 {
		parts = append(parts, "missing "+strings.Join(missing, ", "))
	}
	if len(outdated) > 0 {
		parts = append(parts, "from another install, which install replaces: "+strings.Join(outdated, ", "))
	}
	if len(kept) > 0 {
		parts = append(parts, "differs from the bundled version, and install leaves it: "+strings.Join(kept, ", "))
	}
	if len(linked) > 0 {
		parts = append(parts, "a symlink "+brand.Name+" does not write through: "+strings.Join(linked, ", "))
		fix = "remove " + strings.Join(linked, ", ") + ", then " + fix
	}
	if len(parts) == 0 {
		return check{markOK, name, "", ""}
	}
	return check{markMissing, name, strings.Join(parts, "; "), fix}
}

var lastExitRe = regexp.MustCompile(`last exit code = (\([^)]*\)|\S+)`)

func (e *Env) timerChecks(ctx context.Context) []check {
	checks := e.daemonAgentChecks(ctx)
	if goos == "darwin" {
		checks = append(checks, e.appAgentChecks(ctx)...)
	}
	return checks
}

// appAgentChecks reports the menu bar's LaunchAgent, installed by
// the app install command.
func (e *Env) appAgentChecks(ctx context.Context) []check {
	const name = "menu bar LaunchAgent"
	a := appAgentOf(bundleExe(e.Home))
	path := e.agentPath(a.label)
	if _, err := os.Stat(path); err != nil {
		return []check{{markInfo, name, "not installed; '" + brand.Command + " app install' adds it", ""}}
	}
	checks := []check{{markOK, name, path, ""}}
	out, err := e.Sh(ctx, "launchctl", "print", fmt.Sprintf("gui/%d/%s", os.Getuid(), a.label))
	if err != nil {
		return append(checks, check{markMissing, name + " loaded", "launchctl does not know it", brand.Command + " app install"})
	}
	return append(checks, check{markOK, name + " loaded", loadedInfo(out), ""})
}

func (e *Env) daemonAgentChecks(ctx context.Context) []check {
	const name = "LaunchAgent"
	raw, err := os.ReadFile(e.plistPath())
	switch {
	case err != nil:
		return []check{{markMissing, name, "not installed; nothing starts the daemon at login", brand.Command + " app install"}}
	}
	if command := agentCommand(raw); command != "serve" {
		return []check{{markMissing, name, fmt.Sprintf("%s runs '%s'; the daemon's agent runs 'serve' and the menu bar has its own", e.plistPath(), command), brand.Command + " app install"}}
	}
	info := e.plistPath()
	if interval, ok := e.installedInterval(); ok {
		info += fmt.Sprintf(", runs serve, sweeps every %s", interval)
	}
	checks := []check{{markOK, name, info, ""}}
	out, err := e.Sh(ctx, "launchctl", "print", fmt.Sprintf("gui/%d/%s", os.Getuid(), launchAgentLabel))
	if err != nil {
		return append(checks, check{markMissing, name + " loaded", "launchctl does not know it", fmt.Sprintf("launchctl kickstart gui/%d/%s, or "+brand.Command+" app install", os.Getuid(), launchAgentLabel)})
	}
	return append(checks, check{markOK, name + " loaded", loadedInfo(out), ""})
}

func loadedInfo(launchctlPrint []byte) string {
	info := "loaded"
	if m := lastExitRe.FindSubmatch(launchctlPrint); m != nil {
		info += ", last exit code " + string(m[1])
	}
	return info
}

type daemonReport struct {
	checks []check
	// workflows are the checks of the workflows the daemon runs.
	workflows []check
	// health is nil when the daemon did not answer.
	health *api.Health
}

// daemonChecks asks the daemon itself: the socket answers or nothing runs,
// whatever files a previous run left behind.
func (e *Env) daemonChecks(ctx context.Context, cfg config.Config, c *client.Client, err error) daemonReport {
	h, out, workflows := e.daemonHealthChecks(ctx, cfg, c, err)
	return daemonReport{out, workflows, h}
}

func (e *Env) daemonHealthChecks(ctx context.Context, cfg config.Config, c *client.Client, err error) (*api.Health, []check, []check) {
	const name = "daemon"
	files := []check{
		e.fileCheck("store", filepath.Join(e.StateDir, storeFile), "created by '"+brand.Command+" serve' on its first run"),
		e.fileCheck("cockpit address", filepath.Join(e.StateDir, httpInfoFile), "written by '"+brand.Command+" serve' when it listens"),
	}
	switch {
	case errors.Is(err, errDaemonDown):
		return nil, append([]check{{markMissing, name, err.Error(), ""}}, files...), nil
	case err != nil:
		return nil, append([]check{{markMissing, name, err.Error(), restartHint()}}, files...), nil
	}
	h, err := c.Health(ctx)
	if err != nil {
		return nil, append([]check{{markMissing, name, err.Error(), restartHint()}}, files...), nil
	}
	info := "running, version " + h.Version
	if h.Viewer != "" {
		info += ", as " + h.Viewer
	}
	out := []check{{markOK, name, info, ""}}
	if h.Version != Version && h.Version != "dev" && Version != "dev" {
		out = append(out, check{markMissing, name + " version", "daemon runs " + h.Version + ", CLI is " + Version, restartHint()})
	}

	def := Defaults().Sweep
	every := map[string]time.Duration{
		"fast":  time.Duration(cmp.Or(cfg.Sweep.Fast, def.Fast)),
		"sweep": time.Duration(cmp.Or(cfg.Sweep.Slow, def.Slow)),
		"slow":  time.Duration(cmp.Or(cfg.Sweep.Slow, def.Slow)),
	}
	now := e.Now()
	for _, j := range h.Jobs {
		switch interval := cmp.Or(j.Every, every[j.Name]); {
		case j.LastErr != "":
			out = append(out, check{markMissing, "job " + j.Name, j.LastErr, "see " + filepath.Join(e.StateDir, "serve.err.log")})
		case j.LastOK.IsZero() || interval == 0:
		case now.Sub(j.LastOK) > 2*interval:
			out = append(out, check{markMissing, "job " + j.Name,
				fmt.Sprintf("last success %s ago, expected every %s", shortAge(now.Sub(j.LastOK), false), roundDuration(interval)),
				"see " + filepath.Join(e.StateDir, "serve.err.log")})
		}
	}
	if a := h.App; a != nil {
		switch {
		case a.Notifications == "denied":
			out = append(out, check{markMissing, "notifications", "off for " + brand.Name, "System Settings > Notifications > " + brand.Name + ": allow notifications"})
		case a.Notifications == "not-determined":
			out = append(out, check{markMissing, "notifications", "not yet allowed for " + bundleID, "allow them when macOS asks, or System Settings > Notifications > " + brand.Name + ": allow notifications"})
		case a.Notifications != "":
			out = append(out, check{markOK, "notifications", a.Notifications, ""})
		}
	}
	if !h.Notifier && goos == "darwin" {
		out = append(out, check{markInfo, "menu bar", "not running, so notifications are off; " + notifierStart() + " starts it, or '" + brand.Command + " app install' adds it", ""})
	}
	out = append(out, e.briefCheck(cfg, h.Brief, h.Briefs))
	state, workflows := e.stateChecks(ctx, c)
	out = append(out, state...)
	for _, w := range h.Warnings {
		if strings.HasPrefix(w, "app: ") || strings.HasPrefix(w, "brief: ") || strings.HasPrefix(w, "menu bar not running") ||
			strings.HasPrefix(w, "agent home ") || strings.HasPrefix(w, "reconcile") {
			continue
		}
		out = append(out, check{markMissing, "daemon warning", w, ""})
	}
	if h.SlowSync {
		info := "GitHub rate limit below sweep.rate_limit_fraction; the fast sweep waits"
		if !h.RateLimit.ResetAt.IsZero() {
			info += " until " + h.RateLimit.ResetAt.Local().Format("15:04")
		}
		out = append(out, check{markInfo, "slow sync", info, ""})
	}
	return &h, append(out, files...), workflows
}

// stateChecks are the checks that read the loop's state, which the daemon
// runs: rounds held today, the pause, the start-up reconcile.
func (e *Env) stateChecks(ctx context.Context, c *client.Client) (state, workflows []check) {
	rep, err := c.Doctor(ctx, api.DoctorQuery{})
	if err != nil {
		return []check{{markMissing, "daemon state checks", err.Error(), restartHint()}}, nil
	}
	return doctorChecks(rep.Checks), doctorChecks(rep.Workflows)
}

func doctorChecks(dcs []api.DoctorCheck) []check {
	marks := map[string]mark{api.DoctorOK: markOK, api.DoctorMissing: markMissing, api.DoctorInfo: markInfo}
	out := make([]check, 0, len(dcs))
	for _, dc := range dcs {
		m, ok := marks[dc.Mark]
		if !ok {
			m = markInfo
		}
		out = append(out, check{m, dc.Name, dc.Info, dc.Fix})
	}
	return out
}

// briefCheck reads the daemon's brief status and breaker; st is nil when
// the daemon does not run briefs, and b says why when it cannot.
func (e *Env) briefCheck(cfg config.Config, st *api.BriefStatus, b *api.BriefsHealth) check {
	const name = "brief"
	switch {
	case !cfg.Brief.Enabled:
		return check{markInfo, name, "off; set brief.enabled to true to preview each pull request with a headless call of the agent (one call per pull request, again when it changes)", ""}
	case st == nil && b != nil && b.Unavailable != "":
		return check{markMissing, name, "on in the config, but the daemon cannot brief: " + b.Unavailable + "; every turn goes without a brief", "fix the agent named by brief.agent or agent, then " + restartHint()}
	case st == nil:
		return check{markMissing, name, "on in the config, but the daemon does not brief", "see " + filepath.Join(e.StateDir, "serve.err.log") + " for why, then " + restartHint()}
	}
	info := fmt.Sprintf("on (%s tier): %d ready, %d pending, %d failed", cfg.Brief.Tier, st.Ready, st.Pending, st.Failed)
	if b != nil && b.Open {
		return check{markMissing, name, fmt.Sprintf("briefs unavailable since %s: %s; first turns start without the brief until a trial at %s succeeds; %s",
			b.Since.Local().Format("15:04"), b.Error, b.Next.Local().Format("15:04"), info), "see " + filepath.Join(e.StateDir, "serve.err.log")}
	}
	if !st.PausedUntil.IsZero() {
		info += "; paused until " + st.PausedUntil.Local().Format("15:04")
	}
	if st.LastError != "" {
		return check{markMissing, name, st.LastError + "; " + info, "see " + filepath.Join(e.StateDir, "serve.err.log")}
	}
	return check{markOK, name, info, ""}
}

// notifierStart is the command that starts the menu bar, "" off macOS.
func notifierStart() string {
	if goos != "darwin" {
		return ""
	}
	return fmt.Sprintf("launchctl kickstart gui/%d/%s", os.Getuid(), appAgentOf("").label)
}

// restartHint restarts the daemon in place, so it runs the installed binary
// and the current config.
func restartHint() string {
	if goos == "darwin" {
		return fmt.Sprintf("launchctl kickstart -k gui/%d/%s", os.Getuid(), launchAgentLabel)
	}
	return "restart " + brand.Command + " serve"
}

func (e *Env) fileCheck(name, path, absent string) check {
	if _, err := os.Stat(path); err != nil {
		return check{markInfo, name, path + " absent; " + absent, ""}
	}
	return check{markOK, name, path, ""}
}

// pathChecks resolves the binaries the way the login shell does, which is
// what the LaunchAgent starts the daemon with.
func (e *Env) pathChecks(ctx context.Context, cfg config.Config) []check {
	bins := []string{"gh"}
	for _, k := range e.enabledEntries(cfg) {
		if k.Binary != "" {
			bins = append(bins, k.Binary)
		}
	}
	bins = uniq(append(append(bins, "git"), e.agentBinaries(cfg)...)...)
	found := e.loginPaths(ctx, bins)
	var checks []check
	for _, b := range bins {
		if p, ok := found[b]; ok {
			checks = append(checks, check{markOK, b + " on the login shell's PATH", p, ""})
		} else {
			checks = append(checks, check{markMissing, b + " on the login shell's PATH", "not found by " + shellPath() + " -lc", "add its directory to PATH in your login profile"})
		}
	}
	return checks
}

// loginPaths maps each of bins the login shell finds to where it is.
func (e *Env) loginPaths(ctx context.Context, bins []string) map[string]string {
	out, _ := e.Sh(ctx, shellPath(), "-lc", "command -v "+strings.Join(bins, " "))
	found := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		for _, b := range bins {
			if line == b || strings.HasSuffix(line, "/"+b) || strings.HasPrefix(line, b+":") {
				found[b] = line
			}
		}
	}
	return found
}

// agentBinaries are the binaries of the top-level agent and of every
// project's; an unknown kind is looked up by its name.
func (e *Env) agentBinaries(cfg config.Config) []string {
	var out []string
	for _, name := range agentNames(cfg) {
		if a, err := e.agent(name); err == nil {
			name = a.Binary()
		}
		out = append(out, name)
	}
	return out
}

type liveGitCheck struct {
	check check
	// record is the outcome for the daemon's git access log, nil when no
	// ls-remote ran or its remote names no host.
	record *api.GitAccessRecord
}

// liveGitOp names doctor --git's ls-remote among the daemon's own git
// operations.
const liveGitOp = "ls-remote (doctor --git)"

// sshCheck runs ls-remote without prompts on one repository with a review
// workspace, the same git access a follow-up needs under launchd.
func (e *Env) sshCheck(ctx context.Context, cfg config.Config) liveGitCheck {
	const name = "git fetch without prompts"
	list := async(func() []wsdomain.Workspace {
		all, _ := e.WS.List(ctx)
		return all
	})
	repos, err := e.WS.Repos(ctx)
	all := list()
	if err != nil {
		return liveGitCheck{check: check{markMissing, name, err.Error(), ""}}
	}
	var repo wsdomain.Repo
	for _, w := range all {
		if strings.HasPrefix(w.DisplayName, cfg.ReviewPrefix) {
			for _, r := range repos {
				if r.ID == w.RepoID {
					repo = r
				}
			}
			if repo.ID != "" {
				break
			}
		}
	}
	if repo.ID == "" {
		for _, r := range repos {
			if _, _, ok := r.OwnerName(); ok {
				repo = r
				break
			}
		}
	}
	if repo.ID == "" {
		return liveGitCheck{check: check{markInfo, name, "no GitHub repository registered in " + e.managerDisplay() + " yet", ""}}
	}
	err = e.Git.LsRemote(ctx, repo.Path)
	var rec *api.GitAccessRecord
	if host, _ := gitx.ParseRemote(gitx.RemoteURL(repo.Path, "origin")); host != "" && ctx.Err() == nil {
		rec = &api.GitAccessRecord{Host: host, GitAccessOutcome: api.GitAccessOutcome{At: e.Now(), Op: liveGitOp}}
		if err != nil {
			rec.Error = err.Error()
		}
	}
	if err != nil {
		return liveGitCheck{check{markMissing, name, err.Error(), sshFix}, rec}
	}
	return liveGitCheck{check{markOK, name, repo.Path, ""}, rec}
}

const (
	sshFix = "make the SSH key usable without a prompt (ssh-add --apple-use-keychain)"
	// gitAccessFresh is how long a git operation of the daemon stands as
	// proof of its access.
	gitAccessFresh = 24 * time.Hour
)

// gitAccessChecks judge, per host, the daemon's own latest git network
// operation, with live, doctor --git's outcome, as the latest when set.
func (e *Env) gitAccessChecks(hosts []api.GitAccess, live *api.GitAccessRecord) []check {
	hosts = slices.Clone(hosts)
	if live != nil {
		i := slices.IndexFunc(hosts, func(g api.GitAccess) bool { return g.Host == live.Host })
		if i < 0 {
			hosts, i = append(hosts, api.GitAccess{Host: live.Host}), len(hosts)
		}
		hosts[i].Last = live.GitAccessOutcome
		if live.Error == "" {
			ok := live.GitAccessOutcome
			hosts[i].LastOK = &ok
		}
	}
	now := func() string { return "run '" + e.command() + " doctor --git' to check now" }
	if len(hosts) == 0 {
		return []check{{markInfo, "git without prompts", "no git fetch of the daemon seen yet; " + now(), ""}}
	}
	out := make([]check, 0, len(hosts))
	for _, g := range hosts {
		name := "git without prompts (" + g.Host + ")"
		age := e.Now().Sub(g.Last.At)
		ago := shortAge(age, false) + " ago"
		if age < time.Minute {
			ago = "just now"
		}
		switch {
		case g.Last.Error != "" && gitx.NeedsPrompt(g.Last.Error):
			out = append(out, check{markMissing, name, oneLine(g.Last.Error) + " (" + g.Last.Op + ", " + ago + ")", sshFix})
		case age > gitAccessFresh:
			out = append(out, check{markInfo, name, "not seen in " + shortAge(age, false) + "; " + now(), ""})
		case g.Last.Error == "":
			out = append(out, check{markOK, name, ago + " · " + g.Last.Op, ""})
		default:
			info := g.Last.Op + " failed " + ago + ": " + oneLine(g.Last.Error)
			if g.LastOK != nil {
				info += "; last success " + shortAge(e.Now().Sub(g.LastOK.At), false) + " ago"
			}
			out = append(out, check{markInfo, name, info, ""})
		}
	}
	return out
}

func uniq(vals ...string) []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range vals {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// projectChecks covers the clone directories the project settings name.
func (e *Env) projectChecks(cfg config.Config) []check {
	var out []check
	for _, p := range cfg.Projects {
		name := "project " + p.Match
		if p.Agent != "" {
			if _, err := e.agent(p.Agent); err != nil {
				out = append(out, check{markMissing, name + " agent", err.Error(), "remove agent from the project or use one of: " + strings.Join(slices.Sorted(maps.Keys(e.Agents)), ", ")})
			}
		}
		kind := cmp.Or(p.Agent, cfg.Agent)
		if other, ok := e.otherOwner(kind, p.AgentHome); p.AgentHome != "" && ok {
			out = append(out, check{markMissing, name + " agent_home", p.AgentHome + " is a " + other + " home, and the project runs " + kind, "set agent_home to a " + kind + " home"})
		}
		if p.ProjectsDir != "" {
			if fi, err := os.Stat(p.ProjectsDir); err != nil || !fi.IsDir() {
				out = append(out, check{markMissing, name + " projects_dir", p.ProjectsDir + " does not exist", "mkdir -p " + p.ProjectsDir})
			}
		}
	}
	return out
}

func roundDuration(d time.Duration) string {
	switch {
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	}
	return d.String()
}

// intentChecks load the profile's intents: a line per user intent that
// does not load and per override read as bundled, with all its problems,
// else the intents the loop runs.
func (e *Env) intentChecks() []check {
	l := e.loadedIntents()
	var out []check
	fix := "fix the manifest; '" + e.command() + " profile check' lists every problem"
	for _, name := range slices.Sorted(maps.Keys(l.Broken)) {
		problems := strings.ReplaceAll(l.Broken[name].Error(), "\n", "\n            ")
		out = append(out, check{markMissing, "intent " + string(name), "does not load:\n            " + problems, fix})
	}
	for _, name := range slices.Sorted(maps.Keys(l.Reverted)) {
		problems := strings.ReplaceAll(l.Reverted[name].Error(), "\n", "\n            ")
		out = append(out, check{markMissing, "intent " + string(name), "runs as bundled, the override does not load:\n            " + problems, fix})
	}
	if len(out) > 0 {
		return out
	}
	var names []string
	for _, d := range l.Registry.UserFacing() {
		names = append(names, string(d.Name))
	}
	return []check{{markOK, "intents", strings.Join(names, " "), ""}}
}

// managerChecks name each workspace_managers entry no release supports, a
// manager not available, and no manager available at all; and, per launch
// intent, whether a manager can launch its sessions.
func (e *Env) managerChecks(ctx context.Context, cfg config.Config) []check {
	all := e.managers()
	var out []check
	available := 0
	for _, n := range cfg.WorkspaceManagers {
		k, ok := all[n]
		if !ok {
			out = append(out, check{markMissing, "workspace_managers " + n, unknownManager(n, all).Error(), "list only: " + strings.Join(managerNames(all), ", ")})
			continue
		}
		if k.Available != nil {
			if err := k.Available(ctx); err != nil {
				out = append(out, check{markInfo, "workspace manager " + n, "not available: " + err.Error(), ""})
				continue
			}
		}
		available++
		out = append(out, check{markOK, "workspace manager " + n, "available", ""})
		for _, adv := range k.Advice {
			out = append(out, check{markInfo, adv.Name, adv.Info, adv.Fix})
		}
	}
	if available == 0 {
		out = append(out, check{markMissing, "workspace managers", "none of " + strings.Join(cfg.WorkspaceManagers, ", ") + " is available", "install one, or list one that is installed in workspace_managers"})
	}
	return append(out, e.launchChecks(ctx, cfg)...)
}

// launchChecks say, for each intent with a session, which manager launches
// its sessions under the configuration, and fail when none can.
func (e *Env) launchChecks(ctx context.Context, cfg config.Config) []check {
	l := e.loadedIntents()
	if l == nil || l.Registry == nil {
		return nil
	}
	var out []check
	for _, d := range l.Registry.All() {
		if d.Session.Title == "" {
			continue
		}
		opts := intentOptions(d, roundOptions{})
		m, err := e.chooseManager(ctx, cfg, opts)
		if err != nil {
			out = append(out, check{markMissing, "launch " + string(d.Name), err.Error(), "list a manager that can launch it in workspace_managers"})
			continue
		}
		out = append(out, check{markOK, "launch " + string(d.Name), "in " + e.displayOf([]string{m.Name()}), ""})
	}
	return out
}
