package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/setup"
	"github.com/wloophq/wloop/internal/core/turns"
	coreusage "github.com/wloophq/wloop/internal/core/usage"
	"github.com/wloophq/wloop/internal/core/views"
	"github.com/wloophq/wloop/internal/daemon"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/view"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/gitx"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/source"
	"github.com/wloophq/wloop/internal/termfmt"
	"github.com/wloophq/wloop/internal/transport/mcp"
	"github.com/wloophq/wloop/internal/workspace"
)

var configRetry = time.Minute

const (
	storeFile    = "store.db"
	daemonLock   = "daemon.lock"
	httpInfoFile = "http.json"
)

type storeRecorder struct{ store *inventory.Store }

// Record writes ev once: an event with a key is stored at most once per
// pull request, kind and key.
func (r storeRecorder) Record(ctx context.Context, ev dinv.Event) error {
	return r.store.RecordEvent(ctx, ev)
}

func (r storeRecorder) Overrides(ctx context.Context) (map[github.Ref]dinv.Override, error) {
	return r.store.Overrides(ctx)
}

func (r storeRecorder) RecordUnit(ctx context.Context, ev port.Event) error {
	return r.store.RecordUnitEvent(ctx, ev)
}

func (r storeRecorder) FirstRoundBlocks(ctx context.Context) (map[unit.Unit]port.Event, error) {
	return r.store.FirstRoundBlocks(ctx)
}

func (r storeRecorder) KeepNextGuidance(ctx context.Context, ref github.Ref, guidance string) error {
	return r.store.KeepNextGuidance(ctx, ref, guidance)
}

func (r storeRecorder) TakeNextGuidance(ctx context.Context, ref github.Ref) (string, error) {
	return r.store.TakeNextGuidance(ctx, ref)
}

func (r storeRecorder) OpenQuestions(books map[unit.Unit]dturn.Book) map[github.Ref]map[string]bool {
	out := map[github.Ref]map[string]bool{}
	for u, b := range books {
		ref := prOf(u)
		for _, i := range []dturn.Intent{dturn.Intent(dintent.Review), dturn.Intent(dintent.Address)} {
			if b.OpenQuestion(i) {
				if out[ref] == nil {
					out[ref] = map[string]bool{}
				}
				out[ref][string(i)] = true
			}
		}
	}
	return out
}

// runServe serves until ctx ends, or until the binary is replaced: then it
// stops cleanly and execs the new one in place, so an upgrade needs no
// restart.
func runServe(ctx context.Context, e *Env, args []string) error {
	sctx, stop := context.WithCancel(ctx)
	defer stop()
	upgraded := watchBinary(sctx, e.Executable)
	go func() {
		select {
		case <-upgraded:
			stop()
		case <-sctx.Done():
		}
	}()
	err := serve(sctx, e, args)
	select {
	case <-upgraded:
		if ctx.Err() == nil {
			fmt.Fprintf(e.Err, "%s %s was replaced; restarting in place\n", e.stamp(), e.Executable)
			return e.Reexec()
		}
	default:
	}
	return err
}

func serve(ctx context.Context, e *Env, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	mcpPort := fs.Int("mcp-port", 0, "serve MCP on this port instead of mcp.port, for manual checks")
	fs.BoolVar(&e.noJobs, "no-jobs", false, "run no collector, sweep, gate, brief, usage or retain: serve the APIs and MCP only")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New("usage: " + brand.Command + " serve [--mcp-port <n>] [--no-jobs]")
	}
	if *mcpPort < 0 || *mcpPort > 65535 {
		return fmt.Errorf("--mcp-port must be between 1 and 65535, got %d", *mcpPort)
	}
	e.meter = meter.NewRegistry(nil)
	ctx = meter.WithRegistry(ctx, e.meter)
	logf := func(format string, args ...any) {
		fmt.Fprintf(e.Err, "%s %s\n", e.stamp(), fmt.Sprintf(format, args...))
	}
	e.changes = change.NewLog(change.DefaultWindow, logf)
	db, unlockStore, err := e.openStore(ctx, logf)
	if err != nil || db == nil {
		return err
	}
	defer unlockStore()
	defer db.Close()
	e.WS.UseCustody(db.Worktrees())
	store := inventory.New(db)
	store.UseClock(e.Now)
	e.Rec = storeRecorder{store}
	e.interrupted = new(atomic.Bool)
	e.Rounds = sqlite.NewRounds(db)
	endpoint := mcpEndpoint{port: *mcpPort, log: logf, slack: e.slack, key: e.mcpKey}
	e.observeGit(ctx, &daemon.GitAccessLog{Store: store}, logf)
	startCfg, err := e.readConfig()
	if err != nil {
		startCfg = e.defaultConfig()
	}
	e.cfg = e.configHolder(startCfg)
	if err != nil {
		e.cfg.Break(err)
	}
	vctx, stopViews := context.WithCancel(ctx)
	e.serveTurns(vctx, db, store, startCfg, logf)
	defer func() {
		stopViews()
		<-e.served.done
	}()
	if e.noJobs {
		logf("serving with --no-jobs: no collector, sweep, gate, brief, usage or retain")
	}

	// One loop mutex for every server this process runs: an action the
	// degraded server delegated may still run after it stops.
	loopMu, appState := &sync.Mutex{}, &daemon.AppState{}
	for {
		cfg, cfgErr := e.readConfig()
		if cfgErr == nil {
			e.cfg.Rebase(cfg)
			gctx, stop := context.WithCancel(ctx)
			e.restoreBudget(gctx, db, cfg, logf)
			deps := endpoint.wire(serveDeps(gctx, e, db, cfg, logf, loopMu, appState), cfg)
			deps.ConfigWarning = e.cfg.Warning
			srv := daemon.New(deps)
			e.Briefing = srv
			err := srv.Run(ctx)
			stop()
			return err
		}
		logf("config: %v; serving without collection or sweeps until it loads", cfgErr)
		if err := serveUntilConfigLoads(ctx, e, db, logf, loopMu, appState, endpoint); err != nil || ctx.Err() != nil {
			return err
		}
	}
}

// configHolder holds cfg for a serve: the profile setting follows the config
// file and any other change is reported stale.
func (e *Env) configHolder(cfg config.Config) *config.Holder {
	return config.NewHolder(cfg, config.HolderOptions{Path: e.ConfigPath, Load: e.readConfig, Now: e.Now,
		OnProfileChange: func(string) { e.noteConfig() }, OnWarningChange: e.noteConfig})
}

// usageJob reads each ended turn's tokens with the reader of the agent
// kind the turn stored; nil under --no-jobs.
func (e *Env) usageJob(db *sqlite.DB, logf func(string, ...any)) *coreusage.Service {
	if e.noJobs {
		return nil
	}
	return &coreusage.Service{Store: db.Usage(), ReaderFor: func(kind string) coreusage.Reader { return e.Usage[kind] }, Clock: clockFunc(e.Now), Log: logf}
}

// retainJob applies sqlite.DefaultRetention to db; nil under --no-jobs.
func (e *Env) retainJob(db *sqlite.DB) func(context.Context) (string, error) {
	if e.noJobs {
		return nil
	}
	return func(ctx context.Context) (string, error) {
		got, err := db.Retain(ctx, e.Now(), sqlite.DefaultRetention)
		return "retain: " + got.String(), err
	}
}

// mcpEndpoint is the daemon's MCP server over the daemon's turns; port, when
// not 0, overrides mcp.port.
type mcpEndpoint struct {
	port  int
	log   func(string, ...any)
	slack slackEnv
	// key is read on each request, so a key install writes later applies
	// without a restart.
	key func() string
}

func (m mcpEndpoint) wire(d daemon.Deps, cfg config.Config) daemon.Deps {
	d.MCPPort = cfg.MCP.Port
	if m.port != 0 {
		d.MCPPort = m.port
	}
	srv := &mcp.Server{Port: d.MCPPort, Version: Version, Log: m.log, Key: m.key}
	if d.Turns != nil {
		srv.Turns = d.Turns
	}
	m.wireSlack(srv, d, cfg)
	if d.Messages != nil {
		srv.Inbox = d.Messages
	}
	d.MCP = srv
	return d
}

type clockFunc func() time.Time

func (f clockFunc) Now() time.Time { return f() }

// restoreBudget sets the client's floors from cfg and seeds its budget with
// the one the last sweep kept.
func (e *Env) restoreBudget(ctx context.Context, db *sqlite.DB, cfg config.Config, logf func(string, ...any)) {
	e.GitHub.SetFloors(github.DefaultFloors(cfg.Sweep.RateLimitFraction))
	limits, err := storedGitHubBudget(ctx, inventory.New(db))
	if err != nil {
		logf("GitHub budget: %v", err)
		return
	}
	e.GitHub.Restore(limits)
}

// serveDeps wires the daemon's jobs and use cases on db; the views run until
// ctx ends. Under --no-jobs it plans no round and the views write no card.
func (e *Env) noteConfig() {
	if e.changes != nil {
		e.changes.Note(port.TopicConfig)
	}
}

func serveDeps(ctx context.Context, e *Env, db *sqlite.DB, cfg config.Config, logf func(string, ...any), loopMu *sync.Mutex, app *daemon.AppState) daemon.Deps {
	d := serveAllDeps(ctx, e, db, cfg, logf, loopMu, app)
	if e.noJobs {
		d.Collector, d.Sweep, d.Ingest = nil, nil, nil
		d.Runtimes = withoutPluginJobs(d.Runtimes)
	}
	return d
}

func serveAllDeps(ctx context.Context, e *Env, db *sqlite.DB, cfg config.Config, logf func(string, ...any), loopMu *sync.Mutex, app *daemon.AppState) daemon.Deps {
	store := inventory.New(db)
	store.UseClock(e.Now)
	sessions := e.serveTurns(ctx, db, store, cfg, logf)
	var briefs *interruptFirst
	var intakeBriefs intake.Briefs
	var unavailable string
	if cfg.Brief.Enabled && !e.noJobs && cfg.PluginEnabled(string(unit.GitHub)) {
		var q *brief.Queue
		if q, unavailable = e.briefQueue(cfg, store, logf); q != nil {
			briefs, intakeBriefs = &interruptFirst{done: e.interruptedFlag(), Queue: q}, brief.Intakes{Q: q}
			if err := q.Interrupted(ctx); err != nil {
				logf("brief: marking the runs a stop cut short: %v", err)
			} else {
				e.interruptedFlag().Store(true)
			}
			go func() {
				lctx, scope := meter.Start(ctx, meter.Loop("brief"))
				defer scope.End()
				q.Start(lctx)
			}()
		}
	}
	sd := source.Deps{DB: db, Inventory: store, GitHub: e.GitHub, WS: e.WS, Sink: e.boardSink(cfg), Turns: e.Turns,
		Changes: e.changes, Clock: clockFunc(e.Now), Log: logf, Git: e.Git, Agents: e.Agents,
		Intents: func() *dintent.Registry {
			if l := e.loadedIntents(); l != nil {
				return l.Registry
			}
			return nil
		}}
	if briefs != nil {
		sd.Briefs = briefs.Queue
	}
	d := withStore(daemon.Deps{
		Meter:     e.meter,
		Fallbacks: fallbacksOf(e.WS),
		GitAccess: e.gitAccess,
		Pauses:    e.Rounds,
		LoopMu:    loopMu,
		App:       app,
		Changes:   e.changes,
		Snapshot:  db,

		Intakes:           db.Intakes(),
		IntakeBriefs:      intakeBriefs,
		BriefsUnavailable: unavailable,
		ByHandRef:         e.byHandRef,
		Facts:             e.prFacts(cfg),

		Usage:    e.usageJob(db, logf),
		Retain:   e.retainJob(db),
		Explorer: e.GitHub,
		Budget:   e.GitHub,
		Turns:    e.Turns,
		Sessions: sessions,
		Agents:   lastAgents(sessions),
		Views:    e.Views,
		Actions:  envActions{e, store},
		Intents:  e.loadedIntents,
		Cfg:      cfg,
		Viewer: func(ctx context.Context) (string, []string, error) {
			login, err := store.Viewer(ctx, unit.GitHub)
			if err != nil {
				return "", nil, err
			}
			if login == "" {
				if login, err = e.GitHub.Viewer(ctx); err != nil {
					return "", nil, err
				}
				if err := store.SetViewer(ctx, unit.GitHub, login); err != nil {
					logf("viewer: %v", err)
				}
			}
			teams, err := githubTeams(ctx, store)
			return login, teams, err
		},
		Now:      e.Now,
		Version:  Version,
		Log:      logf,
		StateDir: e.StateDir,
		Socket:   e.Socket,
		LockPath: filepath.Join(e.StateDir, daemonLock),
		InfoPath: filepath.Join(e.StateDir, httpInfoFile),
		Restart:  restartHint(),
		Store:    e.storeStatus,

		WorkspaceManager:  e.healthManager(),
		WorkspaceManagers: e.managerDisplays(),
		ManagerNames:      e.WS.Names(),

		NotifierStart: notifierStart(),

		FailedSteps: failedSteps(e.GitHub),

		TurnByHand: e.byHand(loopMu),
		Startup:    e.startupOnce(cfg, logf),
		Homes:      e.liveHomes(cfg),
		DryRunTurn: e.dryRunTurn,
		Doctor:     e.doctorWith(db),
	}, store)
	if briefs != nil {
		d.Brief, d.BriefStatus, d.BriefViews = briefs, briefs, briefs
	}
	d.Items = e.itemsLedger(db, cfg, logf)
	e.serveTurnLedger(db, cfg, logf)
	links := e.linkDeps(db, cfg, logf, &sd)
	d = e.serveMessages(ctx, d, db, cfg, logf)
	mods, err := buildSources(e.sourcePlugins(), cfg, sd)
	if err != nil {
		logf("sources: %v", err)
	}
	d = e.withCards(withoutGitHub(withSources(d, plugins, mods)), db, cfg, mods, logf)
	d = e.serveGTD(d, db, cfg, mods, logf)
	e.serveProposals(ctx, db, cfg, mods, logf)
	d = e.serveOutbox(d, db, cfg, mods, logf)
	e.servePlacement(mods)
	d = links.serve(d, mods)
	dossierSvc := e.serveDossiers(ctx, db, cfg, logf)
	serveProjects(d.GTD, db, dossierSvc)
	e.serveDossierTurns(ctx, dossierSvc, db, cfg, logf)
	e.servePayloads(mods)
	d = e.serveHeadless(ctx, d, db, cfg, mods, logf)
	d.Placer = sessionPlacer{e: e, cfg: cfg}
	e.viewFacts().set(authorFacts(mods))
	notices := &noticeFeed{}
	d.Notices = notices
	ed := engineDeps{DB: db, Modules: mods, Pub: notices, Log: logf}
	if briefs != nil {
		ed.Briefs = briefs.Queue
	}
	if !e.noJobs {
		if a, err := e.asker(cfg, filepath.Join(e.StateDir, "classify")); err == nil {
			ed.Asker = a
		}
	}
	return withEngine(d, e, ed)
}

// withEngine is d with the workflows' engine as the sweep, its pending
// items as the rows' pending rounds, and the plan served from an engine of
// its own, so looking at the plan never moves the sweep's Seen; the plan's
// workspace provider shares the sweep's checkout reads.
func withEngine(d daemon.Deps, e *Env, ed engineDeps) daemon.Deps {
	if ed.WorkspaceFacts == nil {
		ed.WorkspaceFacts = e.workspaceFacts()
	}
	eng := meteredEngine{e.newEngine(ed)}
	d.Sweep = eng
	pd := ed
	pd.WorkspaceFacts = siblingFacts(ed.WorkspaceFacts)
	d.Plan = servedPlan{eng: eng, plan: e.planEngine(pd)}
	d.Pending = enginePending{e}
	return d
}

// servedPlan is the daemon's plan: evaluations by the plan engine, the
// stored plan, actions and due runs by the sweep's.
type servedPlan struct {
	eng  meteredEngine
	plan *automation.Engine
}

var _ daemon.Plan = servedPlan{}

func (p servedPlan) Evaluate(ctx context.Context, t workflow.TriggerKind) (automation.Plan, error) {
	return p.plan.Evaluate(ctx, t)
}

func (p servedPlan) Items(ctx context.Context, f automation.Filter) ([]workflow.Item, error) {
	if p.eng.Ledger == nil {
		return nil, nil
	}
	return p.eng.Ledger.Items(ctx, f)
}

func (p servedPlan) Act(ctx context.Context, id string, a workflow.Action, guidance string) (workflow.Item, error) {
	return p.eng.Act(ctx, id, a, guidance)
}

func (p servedPlan) Drop(ctx context.Context, u unit.Unit, why string) error {
	return p.eng.Drop(ctx, u, why)
}

func (p servedPlan) DueRun(ctx context.Context) error { return p.eng.DueRun(ctx) }

// authorFacts is the github module's facts provider as the views read it;
// nil without one.
func authorFacts(mods []source.Module) views.Facts {
	for _, m := range mods {
		if f, ok := m.Facts.(views.Facts); ok && m.ID == unit.GitHub {
			return f
		}
	}
	return nil
}

// lateFacts is the views' facts provider, set once the sources are built:
// the views start before them and outlive a reload.
type lateFacts struct{ p atomic.Pointer[views.Facts] }

func (l *lateFacts) set(f views.Facts) {
	if f == nil {
		l.p.Store(nil)
		return
	}
	l.p.Store(&f)
}

// Carded lists the pull requests whose cards the facts move; none before
// the sources are built.
func (l *lateFacts) Carded(ctx context.Context) ([]unit.Unit, error) {
	if c, ok := l.cards(); ok {
		return c.Carded(ctx)
	}
	return nil, nil
}

func (l *lateFacts) Prefetch(ctx context.Context, units []unit.Unit) error {
	if c, ok := l.cards(); ok {
		return c.Prefetch(ctx, units)
	}
	return nil
}

func (l *lateFacts) cards() (cards, bool) {
	f := l.p.Load()
	if f == nil {
		return nil, false
	}
	c, ok := (*f).(cards)
	return c, ok
}

func (l *lateFacts) Author(ctx context.Context, u unit.Unit, needLocal bool) (*view.AuthorFacts, error) {
	f := l.p.Load()
	if f == nil {
		return nil, nil
	}
	return (*f).Author(ctx, u, needLocal)
}

// slowJobs are the sources' slow jobs, run one after the other as the
// daemon's slow job.
type slowJobs []func(ctx context.Context) (summary, skipped string, err error)

func (s slowJobs) SlowJob(ctx context.Context) (string, string, error) {
	var summaries, skips []string
	var errs []error
	for _, job := range s {
		summary, skipped, err := job(ctx)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		summaries, skips = appendSet(summaries, summary), appendSet(skips, skipped)
	}
	return strings.Join(summaries, "; "), strings.Join(skips, "; "), errors.Join(errs...)
}

func appendSet(out []string, s string) []string {
	if s == "" {
		return out
	}
	return append(out, s)
}

// withStore is d with every inventory role on st.
func withStore(d daemon.Deps, st *inventory.Store) daemon.Deps {
	u := inventory.ByUnit{Store: st}
	d.Rows, d.PRs, d.Events, d.Overrides, d.Meta, d.Records = st, u, u, u, st, u
	return d
}

const kindReconcileReport = "reconcile_report"

// startupOnce is the daemon's start-up reconcile, run once per process: a
// server restarted after the config loads gets the first run's report, and
// reruns a reconcile the restart cancelled.
func (e *Env) startupOnce(cfg config.Config, logf func(string, ...any)) func(context.Context) (api.ReconcileReport, error) {
	if e.started == nil {
		e.started = &startedOnce{}
	}
	once := e.started
	return func(ctx context.Context) (api.ReconcileReport, error) {
		once.mu.Lock()
		defer once.mu.Unlock()
		if once.done {
			return once.rep, once.err
		}
		rep, err := e.startup(ctx, cfg, logf)
		if ctx.Err() == nil {
			once.rep, once.err, once.done = rep, err, true
		}
		return rep, err
	}
}

type startedOnce struct {
	mu   sync.Mutex
	done bool
	rep  api.ReconcileReport
	err  error
}

// startup marks the brief runs a stopped daemon left running as
// interrupted, closes the turns whose sessions stopped while the daemon was
// down, brings each view sink back to the turns and records what it found.
// It runs under the daemon's lock and before any job.
func (e *Env) startup(ctx context.Context, cfg config.Config, logf func(string, ...any)) (api.ReconcileReport, error) {
	rep := api.ReconcileReport{At: e.Now()}
	var errs []error
	if e.Store != nil && (e.interrupted == nil || !e.interrupted.Load()) {
		err := brief.New(brief.Deps{Store: e.Store, Now: e.Now, Log: logf}).Interrupted(ctx)
		if err == nil && e.interrupted != nil {
			e.interrupted.Store(true)
		}
		errs = append(errs, err)
	}
	if e.Turns != nil {
		tr, err := e.Turns.Reconcile(ctx, true)
		errs = append(errs, err)
		rep.Unrecorded, rep.Closed, rep.GoneSessions = refsOfUnits(tr.Unrecorded), refsOfUnits(tr.Closed), tr.GoneSessions
		for _, w := range tr.Warnings {
			logf("reconcile: %s", w)
		}
	}
	if e.Views != nil {
		n, err := e.Views.Reconcile(ctx)
		errs = append(errs, err)
		rep.Rewritten = n
	}
	if e.Store != nil {
		data, err := json.Marshal(rep)
		if err == nil {
			err = e.Store.RecordEvent(context.WithoutCancel(ctx), dinv.Event{Kind: kindReconcileReport, At: rep.At, Data: string(data)})
		}
		errs = append(errs, err)
	}
	return rep, errors.Join(errs...)
}

// liveHomes checks the agent homes at each call.
func (e *Env) liveHomes(cfg config.Config) func(context.Context) ([]api.HomeFinding, error) {
	return func(ctx context.Context) ([]api.HomeFinding, error) { return e.checkHomes(ctx, cfg) }
}

// checkHomes checks the homes the loop installs into and every home a
// session reported, and returns their problems.
func (e *Env) checkHomes(ctx context.Context, cfg config.Config) ([]api.HomeFinding, error) {
	byKind := e.homesByKind(cfg)
	homes, err := e.sessionHomes(ctx, cfg)
	if err != nil {
		return nil, err
	}
	for _, h := range homes {
		byKind[h.kind] = append(byKind[h.kind], h.home)
	}
	return e.kindHomeProblems(ctx, cfg, byKind)
}

type sessionHome struct{ home, kind string }

// sessionHomes lists, in the registry's order, each home a session reported
// with the agent of its pull request's project, the top-level kind for a
// session with none.
func (e *Env) sessionHomes(ctx context.Context, cfg config.Config) ([]sessionHome, error) {
	if e.Turns == nil {
		return nil, nil
	}
	sessions, err := e.Turns.Registry.Sessions(ctx)
	if err != nil {
		return nil, err
	}
	var handles []string
	for _, s := range sessions {
		if s.Home != "" {
			handles = append(handles, s.ID)
		}
	}
	units, err := e.Turns.Store.UnitsOf(ctx, handles)
	if err != nil {
		return nil, err
	}
	var out []sessionHome
	for _, s := range sessions {
		if s.Home == "" {
			continue
		}
		kind := cfg.Agent
		if u, found := units[s.ID]; found {
			kind = cfg.For(prOf(u).Owner, prOf(u).Repo).Agent
		}
		out = append(out, sessionHome{s.Home, kind})
	}
	return out, nil
}

// homeProblems checks the homes of dirs that exist, each as the kind of the
// session that reported it or of the homes that hold it, and returns their
// problems.
func (e *Env) homeProblems(ctx context.Context, cfg config.Config, dirs []string) ([]api.HomeFinding, error) {
	homes, err := e.sessionHomes(ctx, cfg)
	if err != nil {
		return nil, err
	}
	byKind := map[string][]string{}
	for _, d := range dirs {
		kind := e.kindOfHome(cfg, d)
		if i := slices.IndexFunc(homes, func(h sessionHome) bool { return h.home == d }); i >= 0 {
			kind = homes[i].kind
		}
		byKind[kind] = append(byKind[kind], d)
	}
	return e.kindHomeProblems(ctx, cfg, byKind)
}

func (e *Env) kindHomeProblems(ctx context.Context, cfg config.Config, byKind map[string][]string) ([]api.HomeFinding, error) {
	if _, err := e.homeKind(cfg.Agent); err != nil {
		return nil, err
	}
	names := agentNames(cfg)
	for _, name := range slices.Sorted(maps.Keys(byKind)) {
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	out := []api.HomeFinding{}
	var errs []error
	for _, name := range names {
		kind, err := e.homeKind(name)
		if err != nil || len(byKind[name]) == 0 {
			continue
		}
		present, _ := existingHomes(cleanHomes(byKind[name]))
		findings, err := setup.Service{Home: kind.New(e.homeSpecFor(cfg, name))}.Check(ctx, present)
		for _, f := range findings {
			if f.Problem != "" {
				out = append(out, api.HomeFinding{Home: f.Home, Entry: f.Entry, Problem: f.Problem, Fix: f.Fix})
			}
		}
		errs = append(errs, err)
	}
	return out, errors.Join(errs...)
}

// viewFacts is the views' facts provider, kept on e across reloads.
func (e *Env) viewFacts() *lateFacts {
	if e.authorFacts == nil {
		e.authorFacts = &lateFacts{}
	}
	return e.authorFacts
}

// trackedBooks are the turns' books, with an empty one for every pull
// request a workspace on the board targets, so the views project the cards
// its facts move; their facts are read at once.
type trackedBooks struct {
	books views.Books
	cards cards
}

// cards are the github facts as the board's books read them.
type cards interface {
	Carded(ctx context.Context) ([]unit.Unit, error)
	Prefetch(ctx context.Context, units []unit.Unit) error
}

func (b trackedBooks) Books(ctx context.Context, refs ...unit.Unit) (map[unit.Unit]dturn.Book, error) {
	out, err := b.books.Books(ctx, refs...)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = map[unit.Unit]dturn.Book{}
	}
	units := refs
	if len(refs) == 0 && b.cards != nil {
		if units, err = b.cards.Carded(ctx); err != nil {
			return nil, err
		}
	}
	for _, u := range units {
		if _, ok := out[u]; !ok && u.Provider == unit.GitHub {
			out[u] = dturn.Book{Unit: u}
		}
	}
	if len(refs) == 0 && b.cards != nil {
		all := slices.SortedFunc(maps.Keys(out), func(a, b unit.Unit) int { return api.CompareUnits(a, b) })
		if err := b.cards.Prefetch(ctx, all); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// servedTurns is what serveTurns built once for the daemon's lifetime.
type servedTurns struct {
	sessions turns.Sessions
	db       *sqlite.DB
	// sinks are the views' sinks, in the order the ports returned them.
	sinks []*viewSink
	done  chan struct{}

	mu  sync.Mutex
	cfg config.Config
}

func (s *servedTurns) setConfig(cfg config.Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg = cfg
}

func (s *servedTurns) config() config.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

// agentFor resolves the agent of ref's project in the config the daemon
// loaded last.
func (e *Env) agentFor(s *servedTurns) func(unit.Unit) turns.Resumer {
	return func(u unit.Unit) turns.Resumer {
		c := s.config()
		if ref := prOf(u); ref != (pr.Ref{}) {
			c = c.For(ref.Owner, ref.Repo)
		}
		a, err := e.agent(c.Agent)
		if err != nil {
			return nil
		}
		return a
	}
}

// serveTurns sets the daemon's store, turns and views on e and starts the
// views the first time, and returns the sessions the turns type into. A later
// call, after the config reloads, only moves each sink to its columns; the
// fast job interval stays the one of the first call. Under --no-jobs the
// views have no sink, so no card is written.
func (e *Env) serveTurns(ctx context.Context, db *sqlite.DB, store *inventory.Store, cfg config.Config, logf func(string, ...any)) turns.Sessions {
	portsLog := logf
	if e.served != nil {
		portsLog = func(string, ...any) {}
	}
	sessions, sinks := e.servedPorts(ctx, db, store, cfg, portsLog)
	if s := e.served; s != nil {
		if running := e.WS.Names(); !slices.Equal(cfg.WorkspaceManagers, running) && !slices.Equal(cfg.WorkspaceManagers, s.config().WorkspaceManagers) {
			logf("workspace_managers is %q, the daemon runs %q; restart the daemon to switch", cfg.WorkspaceManagers, running)
		}
		s.setConfig(cfg)
		if !e.noJobs {
			s.reload(sinks, logf)
		}
		return s.sessions
	}
	served := &servedTurns{sessions: sessions, db: db, done: make(chan struct{}), cfg: cfg}
	var viewSinks []views.Sink
	if !e.noJobs {
		for _, sink := range sinks {
			b := &viewSink{name: sink.Name()}
			b.set(sink)
			served.sinks = append(served.sinks, b)
			viewSinks = append(viewSinks, views.Sink{ViewSink: b, Placements: b})
		}
	}
	repo := sqlite.New(db)
	clock := clockFunc(func() time.Time { return e.Now() })
	v := &views.Service{Books: trackedBooks{books: repo, cards: e.viewFacts()}, Intents: registryPort{e}, State: repo, Sinks: viewSinks, Clock: clock, Log: logf, Paused: e.boardPaused, Debounce: cmp.Or(e.ViewDebounce, views.FlushDebounce),
		Facts: e.viewFacts()}
	if e.changes != nil {
		v.Changes = e.changes
	}
	fast := time.Duration(cfg.Sweep.Fast)
	if fast <= 0 {
		fast = time.Duration(config.Defaults().Sweep.Fast)
	}
	e.Store, e.Views, e.served = store, v, served
	e.Turns = &turns.Service{Store: repo, Registry: repo, Sessions: sessions, Launcher: cliLauncher{e}, GitHub: inventoryFacts{store},
		Changed: v.Changed, Clock: clock, Command: e.loopCommand(), FastJob: fast,
		AgentFor:      e.agentFor(served),
		AgentSessions: agentSessions{db}, Log: logf, Intents: registryPort{e},
		Prompts: transcriptPrompts(db.AgentTranscript)}
	go func() {
		defer close(served.done)
		lctx, scope := meter.Start(ctx, meter.Loop("views"))
		defer scope.End()
		v.Run(lctx)
	}()
	return sessions
}

type agentSessions struct{ db *sqlite.DB }

func (a agentSessions) SaveAgentSession(ctx context.Context, s turns.AgentSessionInfo) error {
	return a.db.SaveAgentSession(ctx, sqlite.AgentSession{ID: s.ID, Kind: s.Kind, Handle: s.Session, Home: s.Home, Worktree: s.Worktree,
		Transcript: s.Transcript, FirstSeen: s.At, LastSeen: s.At})
}

// viewSink is the views' sink with the columns of the config the daemon
// loaded last.
type viewSink struct {
	name  string
	mu    sync.Mutex
	sink  views.ViewSink
	place views.PlacementReader
}

func (b *viewSink) set(s views.Sink) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sink, b.place = s.ViewSink, s.Placements
}

func (b *viewSink) current() (views.ViewSink, views.PlacementReader) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sink, b.place
}

func (b *viewSink) Name() string { return b.name }

func (b *viewSink) Manages(role view.Role, column string) bool {
	s, _ := b.current()
	return s.Manages(role, column)
}

// Status is the column's status on the sink, None when the sink cannot
// tell: the views place author cards by it.
func (b *viewSink) Status(role view.Role, column string) view.Status {
	s, _ := b.current()
	if sr, ok := s.(views.StatusReader); ok {
		return sr.Status(role, column)
	}
	return view.None
}

func (b *viewSink) Apply(ctx context.Context, items []view.Item, force map[unit.Unit]bool) ([]view.Applied, error) {
	s, _ := b.current()
	return s.Apply(ctx, items, force)
}

func (b *viewSink) Placements(ctx context.Context) (map[unit.Unit]string, error) {
	_, p := b.current()
	if p == nil {
		return nil, nil
	}
	return p.Placements(ctx)
}

// reload moves each sink the views write to onto the reloaded config's sink
// of the same name.
func (s *servedTurns) reload(sinks []views.Sink, logf func(string, ...any)) {
	for _, sink := range sinks {
		i := slices.IndexFunc(s.sinks, func(b *viewSink) bool { return b.name == sink.Name() })
		if i < 0 {
			logf("view sink %q is new since the daemon started; ignored until a restart", sink.Name())
			continue
		}
		s.sinks[i].set(sink)
	}
}

// healthManager names the managers WS runs, for the health answer.
func (e *Env) healthManager() string { return e.managerDisplay() }

// managerDisplays are the display names of the managers WS runs, in order.
func (e *Env) managerDisplays() []string {
	var out []string
	for _, n := range e.WS.Names() {
		out = append(out, cmp.Or(e.managers()[n].Display, n))
	}
	return out
}

// managerDisplay names the workspace managers WS runs in messages, such as
// "Claude Desktop or Orca".
func (e *Env) managerDisplay() string {
	if e.WS.Len() == 0 {
		return "the workspace manager"
	}
	return e.displayOf(e.WS.Names())
}

// displayOf names the managers in messages, by their registry display.
func (e *Env) displayOf(names []string) string {
	var out []string
	for _, n := range names {
		out = append(out, cmp.Or(e.managers()[n].Display, n))
	}
	switch len(out) {
	case 0:
		return "the workspace manager"
	case 1:
		return out[0]
	}
	return strings.Join(out[:len(out)-1], ", ") + " or " + out[len(out)-1]
}

const noSessionsAdapter = "cleanup, warm answers and done-pane detection are off for it"

// servedPorts is the sessions of every manager WS runs, behind one router, and
// their sinks, or e.ports' when set; a manager without an entry or ports only
// takes typed prompts and has no sink.
func (e *Env) servedPorts(ctx context.Context, db *sqlite.DB, store *inventory.Store, cfg config.Config, logf func(string, ...any)) (turns.Sessions, []views.Sink) {
	if e.ports != nil {
		return e.ports(store, cfg)
	}
	var managers []managerSessions
	var sinks []views.Sink
	for _, m := range e.WS.All() {
		k, known := e.managers()[m.Name()]
		if !known || k.Ports == nil {
			logf("workspace manager %q has no sessions adapter; "+noSessionsAdapter, m.Name())
			managers = append(managers, typeOnly{m})
			continue
		}
		sessions, ms, err := k.Ports(ctx, m, store, cfg, e.changes)
		if err != nil {
			logf("workspace manager %q: %v; "+noSessionsAdapter, m.Name(), err)
			managers = append(managers, typeOnly{m})
			continue
		}
		managers = append(managers, sessions)
		sinks = append(sinks, ms...)
	}
	r := turns.NewRouter(sqlite.New(db))
	for _, m := range managers {
		r = r.With(m)
	}
	return r, sinks
}

// lastAgents reads the agents from the sessions' last snapshot; sessions
// without one leave the agents unknown.
func lastAgents(s turns.Sessions) readmodel.Agents {
	if l, ok := s.(turns.LastAgents); ok {
		return readmodel.AgentsFunc(l.LastAgents)
	}
	return nil
}

// typeOnly is a manager with no sessions adapter: it takes typed prompts and
// lists nothing.
type typeOnly struct{ ws workspace.Manager }

var _ turns.ManagerLastAgents = typeOnly{}

func (t typeOnly) Name() string { return t.ws.Name() }

func (typeOnly) LastAgents() ([]turns.AgentObservation, bool) { return nil, true }

func (t typeOnly) Processes(context.Context, string) ([]turns.Process, error) {
	return nil, fmt.Errorf("workspace manager %q cannot list its processes", t.ws.Name())
}
func (typeOnly) Agents(context.Context) ([]turns.AgentObservation, error) { return nil, nil }
func (t typeOnly) Census(context.Context) (turns.ProcessCensus, error) {
	return turns.ProcessCensus{}, fmt.Errorf("workspace manager %q cannot list its processes", t.ws.Name())
}
func (t typeOnly) Type(ctx context.Context, handle, line string) (bool, error) {
	return t.ws.SendPrompt(ctx, handle, line, false)
}
func (t typeOnly) Close(ctx context.Context, handle string) error {
	if c, ok := workspace.Capability[workspace.TerminalCloser](t.ws); ok {
		return c.CloseTerminal(ctx, handle)
	}
	return nil
}

// boardPaused holds the views' writes of a repository the pause holds, and
// of every one while the pause cannot be read.
func (e *Env) boardPaused(ctx context.Context, u unit.Unit) bool {
	ref := prOf(u)
	if e.Rounds == nil {
		return false
	}
	p, err := e.Rounds.Pause(ctx)
	return err != nil || p.Holds(ref.Owner, ref.Repo)
}

func failedSteps(gh *github.Client) func(ctx context.Context, owner, repo string, job int64) ([]string, error) {
	return func(ctx context.Context, owner, repo string, job int64) ([]string, error) {
		resp, err := gh.REST(dgh.WithPriority(ctx, dgh.PriorityUser), github.Request{Path: fmt.Sprintf("repos/%s/%s/actions/jobs/%d", owner, repo, job)})
		if err != nil {
			return nil, err
		}
		var body struct {
			Steps []struct {
				Name       string `json:"name"`
				Conclusion string `json:"conclusion"`
			} `json:"steps"`
		}
		if err := json.Unmarshal(resp.Body, &body); err != nil {
			return nil, err
		}
		var steps []string
		for _, s := range body.Steps {
			if s.Conclusion == "failure" {
				steps = append(steps, termfmt.OneLine(s.Name, 120))
			}
		}
		return steps, nil
	}
}

// briefHome runs each brief under the agent home the configuration
// names for the pull request's repository, when that project runs the
// brief's agent.
func briefHome(cfg config.Config) func(github.Ref) string {
	return func(ref github.Ref) string {
		c := cfg.For(ref.Owner, ref.Repo)
		if c.Agent != cfg.Agent {
			return cfg.AgentHome
		}
		return c.AgentHome
	}
}

// serveUntilConfigLoads serves health, with the config error as the sweep's
// warning, and returns once the config loads so the caller starts the jobs.
// Delegated actions load the config themselves and answer with its error.
func serveUntilConfigLoads(ctx context.Context, e *Env, db *sqlite.DB, logf func(string, ...any), loopMu *sync.Mutex, app *daemon.AppState, endpoint mcpEndpoint) error {
	cfg := e.defaultConfig()
	dctx, cancel := context.WithCancel(ctx)
	defer cancel()
	e.restoreBudget(dctx, db, cfg, logf)
	deps := endpoint.wire(serveDeps(dctx, e, db, cfg, logf, loopMu, app), cfg)
	deps.Collector, deps.Ingest = nil, nil
	deps.Runtimes = withoutPluginJobs(deps.Runtimes)
	deps.Brief, deps.Intakes, deps.IntakeBriefs = nil, nil, nil
	deps.Plan, deps.Pending = nil, nil
	if !e.noJobs {
		deps.Sweep = daemon.SweepFunc(func(context.Context) (string, string, error) {
			if _, err := e.readConfig(); err != nil {
				return "", "config: " + err.Error(), nil
			}
			return "", "", nil
		})
	}
	go func() {
		lctx, scope := meter.Start(dctx, meter.Loop("config"))
		defer scope.End()
		t := time.NewTicker(configRetry)
		defer t.Stop()
		for {
			select {
			case <-lctx.Done():
				return
			case <-t.C:
				_, err := e.readConfig()
				if err != nil {
					e.cfg.Break(err)
				}
				if err == nil {
					logf("config loaded; restarting with collection and sweeps")
					cancel()
					return
				}
			}
		}
	}()
	return daemon.New(deps).Run(dctx)
}

// briefQueue is nil, with the reason logged and answered, when the agent
// cannot brief.
func (e *Env) briefQueue(cfg config.Config, store *inventory.Store, logf func(string, ...any)) (*brief.Queue, string) {
	asker, err := e.asker(cfg, filepath.Join(e.StateDir, "brief"))
	if err != nil {
		logf("brief: %v; no briefs", err)
		return nil, err.Error()
	}
	var changes port.Changes
	if e.changes != nil {
		changes = e.changes
	}
	return brief.New(brief.Deps{
		Enabled:      cfg.Brief.Enabled,
		Store:        store,
		Source:       brief.Source{GitHub: e.GitHub},
		Runner:       brief.AskRunner{Asker: asker},
		Taxonomy:     func(github.Ref) string { return brief.DefaultTaxonomy },
		Home:         briefHome(cfg),
		Tier:         cfg.Brief.Tier,
		MaxDiffBytes: cfg.Brief.MaxDiffBytes,
		Retry:        time.Duration(cfg.Sweep.Slow),
		Allow:        e.GitHub.Allow,
		Changes:      changes,
		Now:          e.Now,
		Log:          logf,
	}), ""
}

func (e *Env) interruptedFlag() *atomic.Bool {
	if e.interrupted == nil {
		e.interrupted = new(atomic.Bool)
	}
	return e.interrupted
}

// interruptFirst retries, before its first run, the start-up's marking of
// the runs a stopped daemon left running, when that failed.
type interruptFirst struct {
	*brief.Queue
	done *atomic.Bool
}

func (q interruptFirst) Run(ctx context.Context) (string, error) {
	if !q.done.Load() {
		if err := q.Interrupted(ctx); err != nil {
			return "", err
		}
		q.done.Store(true)
	}
	return q.Queue.Run(ctx)
}

// observeGit keeps every git network operation of this process in log: the
// daemon's own fetches under launchd are what doctor reads as proof of git
// access.
func (e *Env) observeGit(ctx context.Context, log *daemon.GitAccessLog, logf func(string, ...any)) {
	e.gitAccess = log
	rec := newAccessRecorder(ctx, func(ctx context.Context, r api.GitAccessRecord) {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := log.Record(ctx, r); err != nil {
			logf("git access of %s: %v", r.Host, err)
		}
	})
	e.gitFlush = rec.flush
	e.Git = gitx.Git{C: e.Git.C, R: gitx.Observed{R: e.Git.R, Now: e.Now, Observe: func(a gitx.Access) {
		rec.submit(api.GitAccessRecord{Host: a.Host, GitAccessOutcome: api.GitAccessOutcome{At: a.At, Op: a.Op, Error: a.Err}})
	}}}
}

// accessRecorder hands records to one goroutine so a slow store never delays
// the caller; when the buffer is full the record is dropped.
type accessRecorder struct {
	ch      chan api.GitAccessRecord
	pending sync.WaitGroup
}

func newAccessRecorder(ctx context.Context, record func(context.Context, api.GitAccessRecord)) *accessRecorder {
	r := &accessRecorder{ch: make(chan api.GitAccessRecord, 16)}
	lctx, scope := meter.Start(context.WithoutCancel(ctx), meter.Loop("git-access"))
	go func() {
		defer scope.End()
		for rec := range r.ch {
			record(lctx, rec)
			r.pending.Done()
		}
	}()
	return r
}

func (r *accessRecorder) submit(rec api.GitAccessRecord) {
	r.pending.Add(1)
	select {
	case r.ch <- rec:
	default:
		r.pending.Done()
	}
}

func (r *accessRecorder) flush() { r.pending.Wait() }

func refsOfUnits(units []unit.Unit) []github.Ref {
	out := make([]github.Ref, 0, len(units))
	for _, u := range units {
		out = append(out, prOf(u))
	}
	return out
}

// fallbacksOf counts the CLI fallbacks of every manager of set that keeps a count.
func fallbacksOf(set *workspace.Set) func() int64 {
	var counters []func() int64
	for _, m := range set.All() {
		if f, ok := workspace.Direct(m).(interface{ Fallbacks() int64 }); ok {
			counters = append(counters, f.Fallbacks)
		}
	}
	if len(counters) == 0 {
		return nil
	}
	return func() int64 {
		var n int64
		for _, c := range counters {
			n += c()
		}
		return n
	}
}
