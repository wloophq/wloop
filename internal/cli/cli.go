// Package cli implements the subcommands.
package cli

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/client/harness"
	"github.com/wloophq/wloop/internal/client/menubar"
	"github.com/wloophq/wloop/internal/client/menubar/macos"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/dossiers"
	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/core/intake"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/setup"
	coresweep "github.com/wloophq/wloop/internal/core/sweep"
	"github.com/wloophq/wloop/internal/core/turns"
	coreusage "github.com/wloophq/wloop/internal/core/usage"
	"github.com/wloophq/wloop/internal/core/views"
	"github.com/wloophq/wloop/internal/core/workspaces"
	"github.com/wloophq/wloop/internal/daemon"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/round"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/ghcli"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/gitx"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/turn"
	"github.com/wloophq/wloop/internal/workspace"
)

var Version = "dev"

// Recorder receives the loop's own events; nil records nothing.
type Recorder interface {
	Record(ctx context.Context, ev dinv.Event) error
	// RecordUnit is Record for an event on any unit.
	RecordUnit(ctx context.Context, ev port.Event) error
	Overrides(ctx context.Context) (map[github.Ref]dinv.Override, error)
	// FirstRoundBlocks maps each unit whose round is blocked to its
	// round_blocked event.
	FirstRoundBlocks(ctx context.Context) (map[unit.Unit]port.Event, error)
	KeepNextGuidance(ctx context.Context, ref github.Ref, guidance string) error
	TakeNextGuidance(ctx context.Context, ref github.Ref) (string, error)
	// OpenQuestions maps each pull request to the intents whose newest turn
	// is asked.
	OpenQuestions(books map[unit.Unit]dturn.Book) map[github.Ref]map[string]bool
}

// Rounds is the round state's repository and the pause, in the daemon's
// store.
type Rounds interface {
	coresweep.Rounds
	Save(ctx context.Context, st *round.Store) error
	AgentHome(ctx context.Context, ref pr.Ref) (string, error)
	SetPause(ctx context.Context, p round.Pause) error
}

// Briefing tells the sweep whether a round's brief is ready and which
// intakes are open.
type Briefing interface {
	BriefReady(ctx context.Context, u unit.Unit) (intake.Readiness, error)
	OpenIntakes(ctx context.Context) ([]intake.Intake, error)
}

// Env groups dependencies so commands can be tested without touching the real
// environment.
type Env struct {
	Out io.Writer
	Err io.Writer
	// In is the standard input profile add reads its text from.
	In io.Reader

	GH     ghcli.Runner
	GitHub *github.Client
	Git    gitx.Git
	// Files is what removals read of the file system; nil reads it directly.
	Files workspaces.Files
	// WS is every enabled workspace manager, in the configured order.
	WS *workspace.Set
	// Sh runs the auxiliary commands of doctor, app install and clone.
	Sh func(ctx context.Context, name string, args ...string) ([]byte, error)
	// Stdout runs a command for its standard output alone.
	Stdout func(ctx context.Context, name string, args ...string) ([]byte, error)

	LoadConfig func() (config.Config, error)
	// IntentRecheck is how long the intent registry trusts the profile's stamp; zero rechecks on every read.
	IntentRecheck time.Duration
	// ViewDebounce is how long serve's view sinks gather wakes before they flush; zero is views.FlushDebounce.
	ViewDebounce time.Duration
	ConfigPath   string
	StateDir     string
	Home         string
	// HomeOverride is the session home a round by hand brings; nil reads
	// the caller's from Getenv.
	HomeOverride *string
	Now          func() time.Time
	Rec          Recorder
	// Rounds is the daemon's; a command line process has none.
	Rounds Rounds
	// Store, Turns and Views are the daemon's; a command line process has
	// none, and its rounds keep no turn.
	Store *inventory.Store
	// planReads are the pull requests a sweep plans from; nil reads Store.
	planReads coresweep.Inputs
	Turns     *turns.Service
	Views     *views.Service
	// Briefing is the daemon's; nil sends every automatic round without
	// asking for its brief.
	Briefing Briefing
	// facts are the stored facts a command line process read from the
	// daemon, for the turns it renders; nil reads none.
	facts turn.FactsReader
	// intents is the registry cache and the viewer's login, created on first use.
	intents *envIntents
	// cfg is the configuration a serve holds; nil outside serve.
	cfg *config.Holder
	// Socket is the daemon's; empty means no daemon is reachable.
	Socket string
	// Getenv reads the environment of the agent session that runs hook, turn
	// and instructions.
	Getenv func(string) string
	// Getwd is the directory instructions runs in; nil reads the process's.
	Getwd func() (string, error)
	// Harness reads the hook payloads and the session identity of that session.
	Harness harness.Set
	// Executable is the binary the LaunchAgent runs.
	Executable string

	// The native menu bar and the process exit; tests replace them.
	NativeAvailable func() bool
	RunNative       func(setup func(menubar.Tray, menubar.Notifier))
	QuitNative      func()
	Exit            func(code int)
	// Reexec replaces this process with Executable, same arguments.
	Reexec func() error
	// Agents are the supported agents by name.
	Agents map[string]turns.Agent
	// Usage reads the tokens of each agent's turns by name; a kind
	// without one has no usage.
	Usage map[string]coreusage.Reader
	// Homes are the configuration homes of each agent by name.
	Homes map[string]setup.HomeKind
	// Managers are the supported workspace managers by name; nil keeps WS
	// whatever the config selects.
	Managers map[string]ManagerEntry

	// noJobs is serve --no-jobs: the daemon plans no round and writes no
	// Orca card; it still writes unit cards and derived items to its store.
	noJobs bool
	// changes is serve's one change log; nil outside serve.
	changes *change.Log
	// storeStatus is the store as serve opened it, for the health.
	storeStatus *api.StoreStatus
	// meter is the registry serve attaches to its context.
	meter *meter.Registry
	// gitAccess keeps how serve's git network operations went per host.
	gitAccess *daemon.GitAccessLog
	// gitFlush waits for the records handed to the recorder so far.
	gitFlush func()
	// ports puts the workspace manager behind the turns' sessions and the
	// views' sinks; nil is the manager entry's.
	ports func(store *inventory.Store, cfg config.Config) (turns.Sessions, []views.Sink)
	// served is the daemon's turns and views, built once per daemon.
	served *servedTurns
	// items is the daemon's one items ledger; every writer of items shares it.
	items *coreitems.Ledger
	// gtd and messages are the daemon's entries and Inbox messages, built once per daemon; gtdMe is the
	// user's identity the entries reconcile with.
	gtd   *coregtd.Service
	gtdMe *identity
	// pluginsOff holds the plugins the daemon serves disabled, by config or build; empty until serveGTD.
	pluginsOff *atomic.Pointer[[]unit.ProviderID]
	messages   *messages.Service
	// outbox is the daemon's drafts, built once per daemon by serveOutbox.
	outbox *outbox.Service
	// headlessSwept is set once the headless turns a stop cut short were ended, so a later serve of
	// this process never ends a run it started.
	headlessSwept bool
	// dossierTurns and dossierCalls are the daemon's dossier turn checks and calls; nil without a store.
	dossierTurns *dossiers.Turns
	dossierCalls *dossiers.Calls
	// dossierGrants reads, and recordGrant writes, the sessions launched with their unit's dossier.
	dossierGrants func(ctx context.Context, u unit.Unit) (map[string]time.Time, error)
	recordGrant   func(ctx context.Context, u unit.Unit, handle, path string) error
	// dossierMu serializes the dossiers' git work across serve passes.
	dossierMu *sync.Mutex
	// started is the daemon's start-up reconcile, run once per daemon.
	started *startedOnce
	// interrupted is set once the brief runs a stopped daemon left running
	// are marked interrupted.
	interrupted *atomic.Bool
	// engine runs the workflows; nil until newEngine builds it.
	engine *automation.Engine
	// authorFacts are the views' facts provider; nil until a serve sets it.
	authorFacts *lateFacts
	// placementSources are the plugins' placement hints and defaults; nil outside serve.
	placementSources *placementSources
	// slack is how the Slack commands and the daemon reach the Keychain, Slack and the browser.
	slack slackEnv
	// payloads are the enabled plugins' turn payload readers; nil outside serve.
	payloads map[unit.ProviderID]turn.PayloadReader
}

func NewEnv() *Env {
	gh := ghcli.Metered(ghcli.New())
	exe, err := os.Executable()
	if err != nil {
		exe = brand.StateDir()
	}
	stateDir := config.StateDir()
	managers := defaultManagers()
	agents, homes, protocols := agentRegistries(defaultAgents())
	home := config.Home()
	e := &Env{
		Out:           os.Stdout,
		Err:           os.Stderr,
		In:            os.Stdin,
		GH:            gh,
		GitHub:        github.NewClient(github.Options{}),
		Git:           gitx.New(),
		WS:            newSet(managers, config.Defaults().WorkspaceManagers),
		Sh:            execSh,
		Stdout:        execStdout,
		LoadConfig:    config.Load,
		IntentRecheck: intentRecheck,
		ConfigPath:    config.Path(),
		StateDir:      stateDir,
		Home:          home,
		Now:           time.Now,
		Socket:        config.DaemonSocket(),
		Getenv:        os.Getenv,
		Executable:    exe,
		Harness:       harnessOf(protocols, managers),

		NativeAvailable: macos.Available,
		RunNative: func(setup func(menubar.Tray, menubar.Notifier)) {
			macos.Run(filepath.Join(stateDir, "notify"), setup)
		},
		QuitNative: macos.Quit,
		Exit:       os.Exit,
		Reexec:     func() error { return syscall.Exec(exe, os.Args, os.Environ()) }, //nolint:gosec // G702: re-executes this binary with its own arguments
		Agents:     agents,
		Homes:      homes,
		Usage:      defaultUsage(home),
		Managers:   managers,
	}
	e.intents = newEnvIntents(e)
	return e
}

// agentEntry is what the loop runs on one coding agent.
type agentEntry struct {
	Kind     turns.Agent
	Home     setup.HomeKind
	Protocol harness.Protocol
}

func agentRegistries(entries map[string]agentEntry) (map[string]turns.Agent, map[string]setup.HomeKind, map[string]harness.Protocol) {
	kinds, homes, protocols := map[string]turns.Agent{}, map[string]setup.HomeKind{}, map[string]harness.Protocol{}
	for name, a := range entries {
		kinds[name], homes[name], protocols[name] = a.Kind, a.Home, a.Protocol
	}
	return kinds, homes, protocols
}

// harnessOf falls back to the default config's agent and workspace manager
// when a session's environment names neither.
func harnessOf(agents map[string]harness.Protocol, managers map[string]ManagerEntry) harness.Set {
	sessions := map[string]harness.SessionEnv{}
	for name, m := range managers {
		if m.Session != nil {
			sessions[name] = m.Session
		}
	}
	return harness.Set{SessionEnvs: sessions, Agents: agents, DefaultAgent: config.Defaults().Agent, Cwd: func() string {
		wd, _ := os.Getwd()
		return wd
	}}
}

// managerSessions is a manager's processes with the manager's name, as the
// turns' router takes them.
type managerSessions interface {
	turns.ManagerSessions
	Name() string
}

// ManagerEntry is what the loop runs on one workspace manager.
type ManagerEntry struct {
	// Display names the manager in messages, such as "Orca".
	Display string
	// Binary is the manager's executable, as doctor looks it up on PATH; "" for none.
	Binary string
	// Control are the command prefixes and ControlPaths the paths that drive
	// the manager: no session the loop starts may run or read them.
	Control      [][]string
	ControlPaths []string
	// SessionVar is the variable the manager sets, in its sessions' agent
	// process, to the handle of the process.
	SessionVar string
	// Session reads the identity the manager gives a session from its environment.
	Session harness.SessionEnv
	// Sink is the view sink whose columns the manager's workspace statuses
	// are, "" for none; Columns are its default columns by semantic status.
	Sink    string
	Columns map[string]string
	New     func() workspace.Manager
	// Ports are the processes and sinks on ws; reloads ws starts on its own run under
	// ctx, and its changes are noted in changes, which may be nil.
	Ports func(ctx context.Context, ws workspace.Manager, store *inventory.Store, cfg config.Config, changes *change.Log) (managerSessions, []views.Sink, error)
	// Homes lists agent homes the manager keeps for kind under userHome.
	Homes func(userHome, kind string) []string
	// Available is nil when the manager is installed and this release can
	// drive it, else why not; nil Available is always available.
	Available func(ctx context.Context) error
	// Advice is what doctor tells about an available manager that it cannot check.
	Advice []Advice
}

// Advice is one doctor line about a manager: what it is, why, and the fix.
type Advice struct{ Name, Info, Fix string }

func (e *Env) managers() map[string]ManagerEntry {
	if e.Managers != nil {
		return e.Managers
	}
	return defaultManagers()
}

// newSet is the managers names enables, in their order; a name with no entry is skipped.
func newSet(all map[string]ManagerEntry, names []string) *workspace.Set {
	var ms []workspace.Manager
	for _, n := range names {
		if k, ok := all[n]; ok && k.New != nil {
			ms = append(ms, k.New())
		}
	}
	return workspace.NewSet(ms...)
}

// enabledEntries are the registry entries of cfg's managers, in order.
func (e *Env) enabledEntries(cfg config.Config) []namedEntry {
	all := e.managers()
	var out []namedEntry
	for _, n := range cfg.WorkspaceManagers {
		if k, ok := all[n]; ok {
			out = append(out, namedEntry{n, k})
		}
	}
	return out
}

type namedEntry struct {
	name string
	ManagerEntry
}

// boardEntry is the first of cfg's managers with a board, whose sink the
// workspace statuses are written to; ok is false when none has one.
func (e *Env) boardEntry(cfg config.Config) (ManagerEntry, bool) {
	for _, k := range e.enabledEntries(cfg) {
		if k.Sink != "" {
			return k.ManagerEntry, true
		}
	}
	return ManagerEntry{}, false
}

// boardSink is boardEntry's sink, "" without one.
func (e *Env) boardSink(cfg config.Config) string {
	k, _ := e.boardEntry(cfg)
	return k.Sink
}

// controlBinary is the binary of the first of cfg's managers that has one,
// which the probe's instructions name; "" for none.
func (e *Env) controlBinary(cfg config.Config) string {
	for _, k := range e.enabledEntries(cfg) {
		if k.Binary != "" {
			return k.Binary
		}
	}
	return ""
}

// controlSurface is what drives every manager of cfg, which a session the loop
// starts may neither run nor read.
func (e *Env) controlSurface(cfg config.Config) turns.Control {
	var c turns.Control
	for _, k := range e.enabledEntries(cfg) {
		if k.Binary != "" {
			c.Commands = append(c.Commands, []string{k.Binary})
		}
		c.Commands = append(c.Commands, k.Control...)
		c.Paths = append(c.Paths, k.ControlPaths...)
	}
	return c
}

// loadConfig is the serve's held config while a serve runs, else the file's.
func (e *Env) loadConfig() (config.Config, error) {
	if e.cfg != nil {
		return e.cfg.Config(), e.cfg.Err()
	}
	return e.readConfig()
}

// readConfig is LoadConfig's config with each sink's default columns.
func (e *Env) readConfig() (config.Config, error) {
	cfg, err := e.LoadConfig()
	return cfg.WithColumns(e.sinkColumns()), err
}

func (e *Env) defaultConfig() config.Config { return config.Defaults().WithColumns(e.sinkColumns()) }

func (e *Env) sinkColumns() map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, m := range e.managers() {
		if m.Sink != "" {
			out[m.Sink] = m.Columns
		}
	}
	return out
}

// column is the column for status of the sink cfg's board writes its
// statuses to, "" without one.
func (e *Env) column(cfg config.Config, status string) string {
	return cfg.Column(e.boardSink(cfg), status)
}

// setColumn moves workspace ws to the column for status, and does nothing
// when its manager has no board or the board no such column.
func (e *Env) setColumn(ctx context.Context, cfg config.Config, ws wsdomain.Workspace, status string) error {
	column := e.column(cfg, status)
	if column == "" {
		return nil
	}
	m, err := e.WS.For(ctx, ws)
	if err != nil {
		return err
	}
	b, ok := workspace.Capability[workspace.Board](m)
	if !ok {
		return nil
	}
	return b.SetStatus(ctx, ws.ID, column)
}

// useConfiguredManager runs WS on the config's workspace managers. A config
// that does not load keeps the defaults, for the command to report.
func (e *Env) useConfiguredManager() error {
	cfg, err := e.LoadConfig()
	if err != nil {
		return nil
	}
	for _, n := range cfg.WorkspaceManagers {
		if _, ok := e.Managers[n]; !ok {
			return unknownManager(n, e.Managers)
		}
	}
	if !slices.Equal(e.WS.Names(), cfg.WorkspaceManagers) {
		e.WS = newSet(e.Managers, cfg.WorkspaceManagers)
	}
	return nil
}

func unknownManager(name string, all map[string]ManagerEntry) error {
	return fmt.Errorf("workspace_managers names %q, which is not supported; supported: %s", name, strings.Join(managerNames(all), ", "))
}

func managerNames(all map[string]ManagerEntry) []string { return slices.Sorted(maps.Keys(all)) }

func execStdout(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

func execSh(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if err != nil {
		return out.Bytes(), fmt.Errorf("%s: %s", name, strings.TrimSpace(firstNonEmpty(out.String(), err.Error())))
	}
	return out.Bytes(), nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// devUsage is the help of the developer tools, which 'help dev' prints.
var devUsage = `DEVELOPER TOOLS
  eval-instructions [--scenario <glob>]...
                                  Judge the rendered instructions of the eval scenarios and write
                                  the report under the state directory's eval/
  slack eval export|run|score|optimize ...
                                  The extraction eval: export fixtures from the store, run and score
                                  them against the bar, propose a prompt from the dev judgments
  profile try <url> --why <file.json>
                                  Render that workflow's why (a JSON workflow.Why; durations and times
                                  as strings) in the review round's instructions and print it
  serve [--no-jobs] [--mcp-port <n>]
                                  --no-jobs serves the APIs and MCP only; --mcp-port serves MCP on
                                  that port instead of mcp.port, for manual checks
`

// command is how text a person reads names the CLI.
func (e *Env) command() string { return brand.Command }

// configuredManagerDisplay names the config's workspace managers, else, when
// the config does not load, the ones WS runs.
func (e *Env) configuredManagerDisplay() string {
	cfg, err := e.loadConfig()
	if err != nil {
		return e.managerDisplay()
	}
	return e.displayOf(cfg.WorkspaceManagers)
}

func Run(ctx context.Context, e *Env, args []string) int {
	if (len(args) == 0 || len(args) == 1 && strings.HasPrefix(args[0], "-psn_")) && macos.InBundle(e.Executable) {
		args = []string{"app"}
	}
	if len(args) == 0 {
		args = []string{"help"}
	}
	if !slices.Contains([]string{"help", "--help", "-h"}, args[0]) {
		if handled, err := e.commandHelp(args); handled {
			if err != nil {
				fmt.Fprintf(e.Err, "%s error: %v\n", e.stamp(), err)
				return 1
			}
			return 0
		}
	}

	var err error
	if e.Managers != nil && !slices.Contains([]string{"hook", "help", "--help", "-h", "version", "--version", "-v"}, args[0]) {
		if err := e.useConfiguredManager(); err != nil && args[0] != "doctor" && args[0] != "config" {
			fmt.Fprintf(e.Err, "%s error: %v\n", e.stamp(), err)
			return 1
		}
	}
	if run, ok := commands()[args[0]]; ok {
		err = run(ctx, e, args[1:])
	} else if d, ok := e.loadedIntents().Registry.Get(dintent.Name(args[0])); ok && d.UserFacing {
		err = runIntent(ctx, e, d, args[1:])
	} else if _, broken := e.loadedIntents().Broken[dintent.Name(args[0])]; broken {
		err = e.brokenIntent(args[0])
	} else if id, ok := e.pluginCommand(args[0]); ok {
		err = runPluginCLI(ctx, e, id, args[1:])
	} else {
		err = e.unknownCommand(args[0])
	}

	var code exitCode
	if errors.As(err, &code) {
		return int(code)
	}
	if errors.Is(err, errRebuilt) {
		return exitRebuilt
	}
	if errors.Is(err, errDetached) {
		return exitDetached
	}
	var usage usageError
	if errors.As(err, &usage) {
		fmt.Fprintln(e.Err, usage.Error())
		return 2
	}
	if err != nil {
		fmt.Fprintf(e.Err, "%s error: %v\n", e.stamp(), err)
		var detailed interface {
			Detail() string
			DetailSource() string
		}
		if errors.As(err, &detailed) && detailed.Detail() != "" {
			if src := detailed.DetailSource(); src != "" {
				fmt.Fprintf(e.Err, "%s: %s\n", src, detailed.Detail())
			} else {
				for _, l := range strings.Split(detailed.Detail(), "\n") {
					fmt.Fprintf(e.Err, "  %s\n", l)
				}
			}
		}
		var hinted interface{ Hint() string }
		if errors.As(err, &hinted) && hinted.Hint() != "" {
			fmt.Fprintln(e.Err, hinted.Hint())
		}
		return 1
	}
	return 0
}

// command runs one of the commands the loop has whatever the registry holds.
type command func(ctx context.Context, e *Env, args []string) error

// exitCode is a command's exit code when it printed its own outcome.
type exitCode int

func (c exitCode) Error() string { return fmt.Sprintf("exit %d", int(c)) }

// commands are the loop's own commands by name; every user-facing intent of
// the registry adds its own.
func commands() map[string]command {
	prAction := func(name string) command {
		return func(ctx context.Context, e *Env, args []string) error { return runPRAction(ctx, e, name, args) }
	}
	version := func(_ context.Context, e *Env, _ []string) error {
		_, err := fmt.Fprintln(e.Out, Version)
		return err
	}
	help := func(_ context.Context, e *Env, args []string) error { return runHelp(e, args) }
	out := map[string]command{
		"status":            runStatus,
		"tui":               runTUI,
		"take-over":         runTakeOver,
		"intake":            runIntake,
		"timings":           runTimings,
		"plan":              runPlan,
		"workflows":         runWorkflows,
		"doctor":            runDoctorCommand,
		"init":              runInit,
		"config":            func(_ context.Context, e *Env, args []string) error { return runConfig(e, args) },
		"instructions":      runInstructions,
		"hook":              func(ctx context.Context, e *Env, args []string) error { return exitCode(runHook(ctx, e, args)) },
		"turn":              func(ctx context.Context, e *Env, args []string) error { return exitCode(runTurn(ctx, e, args)) },
		"probe":             runProbe,
		"eval-instructions": runEvalInstructions,
		"profile":           runProfile,
		"brief":             runBrief,
		"install":           runInstall,
		"trust":             runTrust,
		"uninstall":         runUninstall,
		"serve":             runServe,
		"store":             runStore,
		"app":               runApp,
		"pause":             func(ctx context.Context, e *Env, args []string) error { return runPause(ctx, e, true, args) },
		"resume":            func(ctx context.Context, e *Env, args []string) error { return runPause(ctx, e, false, args) },
		"open":              runOpen,
		"explore":           runExplore,
		"slack":             runSlack,
		"outbox":            runOutbox,
		"version":           version,
		"--version":         version,
		"-v":                version,
		"help":              help,
		"--help":            help,
		"-h":                help,
	}
	for _, name := range []string{"mute", "unmute", "remove", "track", "untrack"} {
		out[name] = prAction(name)
	}
	return out
}

// launchPolicy is the policy of a session of the round's manifest under
// opts. It is built before a manager is chosen: what it needs decides which
// manager may launch the session.
func (e *Env) launchPolicy(ctx context.Context, cfg config.Config, opts roundOptions) turns.Policy {
	policy := policies[cmp.Or(opts.policy, policyRound)](e, opts.intent, e.controlSurface(cfg))
	policy.Dirs = e.dossierGrant(ctx, opts.unit)
	// An untrusted pull request's checkout settings are its author's: they never load.
	policy.UserSettingsOnly = !cfg.ProjectSettingsFor(string(opts.intent), prFacts(opts.pr)) || opts.pr != nil && !opts.trusted
	return policy
}

// needs is what a session of agent a under p and cfg's home asks of the
// manager that launches it.
func needs(a turns.Agent, cfg config.Config, p turns.Policy) workspace.LaunchNeeds {
	custom := cfg.AgentHome != ""
	if d, ok := a.(turns.HomeDefaulter); ok {
		custom = !d.IsDefaultHome(cfg.AgentHome)
	}
	return workspace.LaunchNeeds{UserSettingsOnly: p.UserSettingsOnly, Strict: p.Strict, NoMCP: p.NoMCP, CustomHome: custom}
}

// agentSession checks that a session of the configured agent can start in ws
// under the policy of the round's manifest and opts.trusted, the pull
// request's verdict from trusts, and returns it. The manager that owns ws must
// honor the policy: one that cannot fails the round closed.
func (e *Env) agentSession(ctx context.Context, cfg config.Config, ws wsdomain.Workspace, opts roundOptions) (turns.AgentSession, error) {
	a, err := e.agent(cfg.Agent)
	if err != nil {
		return turns.AgentSession{}, err
	}
	m, err := e.WS.For(ctx, ws)
	if err != nil {
		return turns.AgentSession{}, err
	}
	policy := e.launchPolicy(ctx, cfg, opts)
	if err := workspace.Honors(m, needs(a, cfg, policy)); err != nil {
		return turns.AgentSession{}, fmt.Errorf("%s owns %s and cannot launch this session: %w", e.displayOf([]string{m.Name()}), ws.DisplayName, err)
	}
	s := turns.AgentSession{Worktree: ws.Path, Home: cfg.AgentHome, Policy: policy, Trusted: opts.trusted}
	if err := e.preflight(ctx, a, s, m); err != nil {
		return turns.AgentSession{}, err
	}
	return s, nil
}

// chooseManager is the first of cfg's managers, in its order, that is enabled
// and available, hosts cfg's agent and honors the launch policy of a round of
// opts; with none, it fails closed and says why each one cannot.
func (e *Env) chooseManager(ctx context.Context, cfg config.Config, opts roundOptions) (workspace.Manager, error) {
	a, err := e.agent(cfg.Agent)
	if err != nil {
		return nil, err
	}
	n := needs(a, cfg, e.launchPolicy(ctx, cfg, opts))
	var why []string
	for _, name := range cfg.WorkspaceManagers {
		m, ok := e.WS.Named(name)
		if !ok {
			why = append(why, name+": not enabled")
			continue
		}
		if k, ok := e.managers()[name]; ok && k.Available != nil {
			if err := k.Available(ctx); err != nil {
				why = append(why, name+": "+err.Error())
				continue
			}
		}
		if err := m.Reachable(ctx); err != nil {
			why = append(why, name+": not running")
			continue
		}
		if !m.Hosts(cfg.Agent) {
			why = append(why, name+": does not run "+cfg.Agent)
			continue
		}
		if err := workspace.Honors(m, n); err != nil {
			why = append(why, name+": "+err.Error())
			continue
		}
		if len(why) > 0 {
			fmt.Fprintf(e.Err, "%s %s launches this session (%s)\n", e.stamp(), e.displayOf([]string{name}), strings.Join(why, "; "))
		}
		return m, nil
	}
	return nil, fmt.Errorf("no workspace manager can launch this session (%s)", strings.Join(why, "; "))
}

// preflight runs the agent's preflight and names the command that fixes an
// untrusted worktree; m is the manager that will launch the session.
func (e *Env) preflight(ctx context.Context, a turns.Agent, s turns.AgentSession, m workspace.Manager) error {
	if t, ok := workspace.Capability[workspace.LaunchTruster](m); ok {
		s.TrustedAtLaunch = t.TrustsAtLaunch(a.Name(), s.Home)
	}
	err := a.Preflight(ctx, s)
	var untrusted *turns.UntrustedError
	if errors.As(err, &untrusted) {
		untrusted.Fix = e.trustCommand(untrusted.Home, cmp.Or(untrusted.Root, untrusted.Folder))
	}
	return err
}

func (e *Env) trustCommand(home, folder string) string {
	return setup.TrustCommand(e.loopCommand(), home, folder)
}

// usageError is a command called with arguments it does not take; it exits 2.
type usageError struct{ usage, why string }

func (u usageError) Error() string {
	if u.why == "" {
		return u.usage
	}
	return u.why + "\n" + u.usage
}
