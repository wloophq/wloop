package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brief"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dchange "github.com/wloophq/wloop/internal/domain/github/change"
	"github.com/wloophq/wloop/internal/domain/round"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
	"github.com/wloophq/wloop/internal/turn"

	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"
	"github.com/wloophq/wloop/internal/adapter/source/github/present"
	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/ingest"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/daemon"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/readmodel"
)

type storeRecorder struct{ store *inventory.Store }

// Record writes ev once: an event with a key is stored at most once per
// pull request, kind and key.
func (r storeRecorder) Record(ctx context.Context, ev dinv.Event) error {
	return r.store.RecordEvent(ctx, ev)
}

func (r storeRecorder) Overrides(ctx context.Context) (map[pr.Ref]dinv.Override, error) {
	return r.store.Overrides(ctx)
}

func (r storeRecorder) RecordUnit(ctx context.Context, ev port.Event) error {
	return r.store.RecordUnitEvent(ctx, ev)
}

func (r storeRecorder) FirstRoundBlocks(ctx context.Context) (map[unit.Unit]port.Event, error) {
	return r.store.FirstRoundBlocks(ctx)
}

func (r storeRecorder) KeepNextGuidance(ctx context.Context, ref pr.Ref, guidance string) error {
	return r.store.KeepNextGuidance(ctx, ref, guidance)
}

func (r storeRecorder) TakeNextGuidance(ctx context.Context, ref pr.Ref) (string, error) {
	return r.store.TakeNextGuidance(ctx, ref)
}

func (r storeRecorder) OpenQuestions(books map[unit.Unit]dturn.Book) map[pr.Ref]map[string]bool {
	out := map[pr.Ref]map[string]bool{}
	for u, b := range books {
		ref := prOf(u)
		for _, i := range roleIntents(b, builtin.RoleReview, builtin.RoleAddress) {
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

// roleIntents are the intents of b's turns whose intent held one of roles.
func roleIntents(b dturn.Book, roles ...string) []dturn.Intent {
	var out []dturn.Intent
	for _, t := range b.Turns {
		if slices.Contains(roles, t.Role) && !slices.Contains(out, t.Intent) {
			out = append(out, t.Intent)
		}
	}
	return out
}

// githubBudget is the budget of the GitHub plugin's API as it last
// named it, which the daemon's health and the brief queue read with no
// call.
type githubBudget struct{ last atomic.Pointer[namedBudget] }

type namedBudget struct {
	low bool
	rl  api.RateLimit
}

// Low says the budget refuses background reads.
func (b *githubBudget) Low() bool {
	l := b.last.Load()
	return l != nil && l.low
}

// RateLimit is the GraphQL bucket, which the searches and the sweeps spend.
func (b *githubBudget) RateLimit() api.RateLimit {
	if l := b.last.Load(); l != nil {
		return l.rl
	}
	return api.RateLimit{}
}

// allow says whether a read of p may spend the budget now: a user's
// always may.
func (b *githubBudget) allow(p dgh.Priority) bool { return p == dgh.PriorityUser || !b.Low() }

// syncGitHubBudget keeps the budget the running GitHub plugin names.
func (e *Env) syncGitHubBudget(ctx context.Context) error {
	raw, err := e.forgeCalls().View(ctx, src.ViewRequest{View: ghsource.ViewBudget})
	if err != nil {
		return err
	}
	var b struct {
		Low       bool      `json:"low"`
		Remaining int       `json:"remaining"`
		Limit     int       `json:"limit"`
		ResetAt   time.Time `json:"reset_at"`
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		return fmt.Errorf("the GitHub plugin's budget: %w", err)
	}
	e.budget.last.Store(&namedBudget{low: b.Low, rl: api.RateLimit{Remaining: b.Remaining, Limit: b.Limit, ResetAt: b.ResetAt}})
	return nil
}

// githubPlugin is the running GitHub plugin's client.
func (e *Env) githubPlugin() (*src.Client, error) {
	if e.pluginKind == nil {
		return nil, fmt.Errorf("the GitHub plugin: %w", src.ErrUnavailable)
	}
	return e.pluginKind.Client(string(unit.GitHub))
}

// withGitHubBriefs is d briefing pull requests the GitHub plugin reads,
// under its budget.
func (e *Env) withGitHubBriefs(d brief.Deps) brief.Deps {
	d.Source = newBriefInputs(func() (briefReader, error) {
		if e.briefReader != nil {
			return e.briefReader()
		}
		return e.githubPlugin()
	})
	d.Allow = e.budget.allow
	return d
}

// briefHome runs each brief under the agent home the configuration
// names for the pull request's repository, when that project runs the
// brief's agent.
func briefHome(cfg config.Config) func(pr.Ref) string {
	return func(ref pr.Ref) string {
		c := cfg.For(ref.Owner, ref.Repo)
		if c.Agent != cfg.Agent {
			return cfg.AgentHome
		}
		return c.AgentHome
	}
}

// githubPresenter are the GitHub presenter's deps over the env's client.
func (e *Env) githubPresenter(store *inventory.Store, host src.Host, briefs *interruptFirst) present.Deps {
	deps := present.Deps{Store: store, Reports: e.sourceReports(store), Host: host,
		Mutes: readmodel.WakeMutes(store.DB().Wakes().UnitWakes),
		Plugin: func(ctx context.Context, req src.ActionRequest) (json.RawMessage, error) {
			return e.forgeCalls().Action(ctx, req)
		},
		View: func(ctx context.Context, req src.ViewRequest) (json.RawMessage, error) {
			return e.forgeCalls().View(ctx, req)
		}}
	if briefs != nil {
		deps.Briefs = briefs
	}
	return deps
}

// withGitHub is d with the GitHub plugin's presenter over deps, its
// background priority and its pull requests' repositories.
func withGitHub(d daemon.Deps, deps present.Deps) daemon.Deps {
	if d.Presenters == nil {
		d.Presenters = map[unit.ProviderID]func(daemon.PresenterHost) daemon.Presenter{}
	}
	d.Presenters[unit.GitHub] = func(h daemon.PresenterHost) daemon.Presenter { return present.New(deps, present.Host(h)) }
	d.Background = func(ctx context.Context) context.Context { return dgh.WithPriority(ctx, dgh.PriorityBackground) }
	d.Repo = present.Repo
	return d
}

// githubIdentity is the viewer as the GitHub plugin last named them, which
// st keeps for the in-process rows and facts; asked of the plugin while st
// keeps none.
func (e *Env) githubIdentity(st *inventory.Store, logf func(string, ...any)) func(context.Context) (src.Identity, error) {
	return func(ctx context.Context) (src.Identity, error) {
		if login, err := st.Viewer(ctx, unit.GitHub); err != nil || login != "" {
			teams, terr := ghsource.MyTeams(ctx, st)
			p := src.Person{Key: string(unit.GitHub) + ":" + login, Name: login}
			return src.Identity{Viewer: p, Teams: teams}, errors.Join(err, terr)
		}
		id, err := e.forgeCalls().Identity(ctx)
		if err != nil {
			return src.Identity{}, err
		}
		if err := ghsource.KeepIdentity(ctx, st, id); err != nil {
			logf("GitHub viewer: %v", err)
		}
		return id, nil
	}
}

// forgeCalls is the forge's plugin as the round commands call it: the one
// set on e, else the running plugin's client, else its binary run in CLI
// mode, which serves its commands alone.
func (e *Env) forgeCalls() forgeCalls {
	if e.forge != nil {
		return e.forge
	}
	if e.pluginKind != nil {
		if c, err := e.pluginKind.Client(string(unit.GitHub)); err == nil {
			return c
		}
	}
	return binaryForge{e}
}

// binaryForge is the GitHub plugin's binary, run in CLI mode for each
// command; what only the running plugin answers is unavailable.
type binaryForge struct{ e *Env }

func (binaryForge) Identity(context.Context) (src.Identity, error) {
	return src.Identity{}, fmt.Errorf("the GitHub plugin does not run: %w", src.ErrUnavailable)
}

func (binaryForge) Action(_ context.Context, req src.ActionRequest) (json.RawMessage, error) {
	return nil, fmt.Errorf("the GitHub plugin does not run, so %s waits: %w", req.Action, src.ErrUnavailable)
}

func (binaryForge) View(_ context.Context, req src.ViewRequest) (json.RawMessage, error) {
	return nil, fmt.Errorf("the GitHub plugin does not run, so %s waits: %w", req.View, src.ErrUnavailable)
}

func (f binaryForge) RunCLI(ctx context.Context, req src.CLIRequest, stdout, stderr io.Writer) (int, error) {
	cfg, err := f.e.loadConfig()
	if err != nil {
		return 0, err
	}
	err = f.e.runPluginBinaryTo(ctx, cfg, string(unit.GitHub), req.Args, req.Stdin, stdout)
	var code exitCode
	if errors.As(err, &code) {
		return int(code), nil
	}
	return 0, err
}

// forgeAuthenticated asks the forge's plugin whether gh is installed and
// logged in; missing says gh is not installed.
func (e *Env) forgeAuthenticated(ctx context.Context) (missing bool, err error) {
	err = e.forgeCommand(ctx, ghsource.CommandAuth)
	var x *forgeExit
	return errors.As(err, &x) && x.code == ghsource.ExitNoGH, err
}

// forgeReviewedRepos are the repositories, one a pull request, of what the
// viewer reviewed since the day of since.
func (e *Env) forgeReviewedRepos(ctx context.Context, since time.Time) ([]byte, error) {
	return e.forgeOutput(ctx, ghsource.CommandReviewedRepos, since.UTC().Format(time.DateOnly))
}

// forgeTeams are the viewer's teams, as org/slug lines.
func (e *Env) forgeTeams(ctx context.Context) ([]byte, error) {
	return e.forgeOutput(ctx, ghsource.CommandTeams)
}

// forgeViewer is the viewer's login as the forge's plugin knows them.
func (e *Env) forgeViewer(ctx context.Context) (string, error) {
	id, err := e.forgeCalls().Identity(ctx)
	if err != nil {
		return "", err
	}
	login := readmodel.Login(unit.GitHub, id)
	if login == "" {
		return "", errors.New("the forge's plugin knows no viewer")
	}
	return login, nil
}

// forgeBasics are ref's basics as the forge's plugin reads them.
func (e *Env) forgeBasics(ctx context.Context, ref pr.Ref) (*pr.PullRequest, error) {
	raw, err := e.forgeCalls().Action(ctx, src.ActionRequest{Unit: psource.Wire(ref.Unit()), Action: ghsource.ActionBasics})
	if err != nil {
		return nil, err
	}
	var basics pr.PullRequest
	if err := json.Unmarshal(raw, &basics); err != nil {
		return nil, fmt.Errorf("%s: the plugin's basics: %w", ref, err)
	}
	return &basics, nil
}

// The GitHub plugin's round marks the command line's in-process GitHub
// paths still read and write.
const (
	markFirstSkipped      = ghsource.MarkFirstSkipped
	markAddressedReviews  = ghsource.MarkAddressedReviews
	markAddressedChecks   = ghsource.MarkAddressedChecks
	markAddressedConflict = ghsource.MarkAddressedConflict
)

// reportedPRs are the pull requests the GitHub plugin last reported.
type reportedPRs interface {
	dinv.PlanInputs
	PR(ctx context.Context, ref pr.Ref) (dinv.PR, bool, error)
}

// reportedInputs are the pull requests the GitHub plugin last reported, as
// the host's facts, holdings, placements and round sends read them.
// reportedPRs are the pull requests the running GitHub plugin last
// reported.
func (e *Env) reportedPRs() prrow.ReportedPRs {
	return prrow.ReportedPRs{Reports: e.sourceReports(e.Store)}
}

func (e *Env) reportedInputs() reportedPRs {
	return ghsource.ReportedInputs{Reports: e.sourceReports(e.Store), Store: e.Store}
}

// adoptInPlugin has the GitHub plugin take ref over, its basics as read:
// it keeps the adoption and forgets the reviews the address rounds on ref
// covered, so the pull request starts its triage from none of them.
func (e *Env) adoptInPlugin(ctx context.Context, ref pr.Ref, basics *pr.PullRequest) error {
	args, err := json.Marshal(basics)
	if err != nil {
		return err
	}
	_, err = e.forgeCalls().Action(ctx, src.ActionRequest{Unit: psource.Wire(ref.Unit()), Action: ghsource.ActionAdopt, Args: args})
	return err
}

// recordReviewed marks the reviews of full a held review round in ws
// covered.
func (e *Env) recordReviewed(st *round.Store, cfg config.Config, ref pr.Ref, ws wsdomain.Workspace, full *pr.PullRequest, viewer string) {
	setMarks(st, ref.String(), ghsource.ReviewedMarks(dchange.ReviewerInput{PR: full.Facts, Viewer: viewer, WorkspaceStatus: ws.Status,
		CompletedStatus: e.column(cfg, config.ColumnCompleted), TrustedOwners: []string{ref.Owner}, TrustedAuthors: cfg.GitHub.TrustedAuthors}))
}

// roundsPaused says whether the pause holds u's rounds: everything is
// paused, or u's repository.
func (e *Env) roundsPaused(ctx context.Context, u unit.Unit) (bool, error) {
	if e.Rounds == nil {
		return false, nil
	}
	p, err := e.Rounds.Pause(ctx)
	if err != nil || p.On {
		return p.On, err
	}
	repo, err := present.Repo(ctx, u)
	return repo != "" && p.HoldsRepo(repo), err
}

// withStore is d with every store role on st and its database.
func withStore(d daemon.Deps, st *inventory.Store) daemon.Deps {
	d.Events, d.Overrides, d.Meta, d.Records = inventory.ByUnit{Store: st}, st.DB(), st, present.Records{Store: st}
	d.TaskRecords, d.RecordWriter, d.Placements = st.DB(), st.DB(), inventory.Placements{Store: st}
	return d
}

// githubBriefs is the brief queue keyed by unit, as the daemon runs it.
func githubBriefs(q *interruptFirst) present.BriefQueue { return present.BriefQueue{Q: q} }

// githubPayloads is GitHub's turn payload: the running GitHub plugin's,
// and the pull request a host round hands the turn.
func (e *Env) githubPayloads() turn.PayloadReader {
	return ghsource.TurnPayloads{Plugin: psource.Payloads{ID: string(unit.GitHub), Now: e.Now, Planner: e.githubPayloadPlanner}}
}

// githubPayloadPlanner is the GitHub plugin that builds a turn's payload:
// the one a test sets, else the running one.
func (e *Env) githubPayloadPlanner() (psource.PayloadPlanner, error) {
	if e.payloadPlanner != nil {
		return e.payloadPlanner()
	}
	return e.githubPlugin()
}

// turnEffects applies the effects of provider p's plugin while it runs;
// false when it does not.
func (e *Env) turnEffects(p unit.ProviderID) (effectApplier, bool) { return e.runningApplier(p) }

// runningApplier is the effect applier of provider p's running plugin.
func (e *Env) runningApplier(p unit.ProviderID) (effectApplier, bool) {
	if e.pluginEffects != nil {
		return e.pluginEffects(p)
	}
	if e.pluginKind == nil {
		return nil, false
	}
	if _, err := e.pluginKind.Client(string(p)); err != nil {
		return nil, false
	}
	return runningPlanner{kind: e.pluginKind, id: string(p)}, true
}

// draftAppliers apply the draft effects of each provider's running plugin.
func (e *Env) draftAppliers() func(p unit.ProviderID) (effectApplier, bool) { return e.runningApplier }

// unitConfig is the configuration of u's repository, with the agent home of
// the session that asked for its rounds when no project sets one.
func (e *Env) unitConfig(ctx context.Context, cfg config.Config, u unit.Unit, st *round.Store) (config.Config, error) {
	repo, err := present.Repo(ctx, u)
	if err != nil {
		return config.Config{}, err
	}
	if owner, name, ok := strings.Cut(repo, "/"); ok {
		cfg = cfg.For(owner, name)
	}
	if cfg.AgentHome == "" {
		cfg.AgentHome = st.AgentHome(u.Key)
	}
	return cfg, nil
}

// The GitHub plugin's jobs, as its manifest names them.
const (
	githubJobFast = "fast"
	githubJobSlow = "slow"
)

// githubModule is the GitHub module: its rows, facts and presenter run in
// the host's process over what the GitHub plugin reports; its ingestion,
// its slow sweep, its tracks and its checkouts are the plugin's, which serve
// runs. Outside serve it reads nothing.
func (e *Env) githubModule(build moduleBuild) moduleBuild {
	return func(cfg config.Config, d moduleDeps) (pluginModule, error) {
		m, err := build(cfg, d)
		if err != nil || e.pluginKind == nil {
			return m, err
		}
		return e.relayGitHub(cfg, m)
	}
}

// relayGitHub is m with its ingestion, slow sweep, tracks, checkouts,
// publisher and proposals relayed to the GitHub plugin serve runs.
func (e *Env) relayGitHub(cfg config.Config, m pluginModule) (pluginModule, error) {
	r := relay{id: unit.GitHub, kind: e.pluginKind, now: e.Now, units: psource.Units{Codecs: e.unitCodecs()}}
	every := time.Duration(cmp.Or(cfg.Sweep.Fast, config.Defaults().Sweep.Fast))
	m.Ingest = ingest.Job{Every: every, Prepare: func(ctx context.Context, now time.Time) (ingest.Plan, string, error) {
		p, skip, err := r.prepare(githubJobFast, m.Codec)(ctx, now)
		e.afterGitHubJob(ctx)
		return p, skip, err
	}}
	m.Slow = func(ctx context.Context) (string, string, error) {
		summary, skipped, err := r.ingest(ctx, githubJobSlow)
		if errors.Is(err, src.ErrUnavailable) {
			return "skipped", err.Error(), nil
		}
		e.afterGitHubJob(ctx)
		return summary, skipped, err
	}
	m.Track, m.Untrack = r.track, r.untrack
	m.Checkouts = pluginCheckouts{psource.Checkouts{ID: string(unit.GitHub), Units: r.units, Kind: e.pluginKind}}
	s, err := e.pluginSource(unit.GitHub)
	if err != nil {
		return pluginModule{}, err
	}
	m.Publisher = psource.Publisher{ID: string(unit.GitHub), Source: s, Units: r.units, Kind: e.pluginKind}
	m.Proposals = psource.Proposals{ID: string(unit.GitHub), Units: r.units, Kind: e.pluginKind}.Proposals
	return m, nil
}

// afterGitHubJob keeps the budget the plugin's job left.
func (e *Env) afterGitHubJob(ctx context.Context) {
	if err := e.syncGitHubBudget(ctx); err != nil && ctx.Err() == nil && !errors.Is(err, src.ErrUnavailable) && !errors.Is(err, src.ErrUnsupported) {
		e.warnf("GitHub budget: %v", err)
	}
}

// pluginCheckouts are a plugin's checkouts as a module serves them.
type pluginCheckouts struct{ c psource.Checkouts }

func (p pluginCheckouts) Checkout(ctx context.Context, u unit.Unit) (unitCheckout, bool, error) {
	c, ok, err := p.c.Checkout(ctx, u)
	return unitCheckout{Repo: c.Repo, Branch: c.Branch, Head: c.Head}, ok, err
}

// withGitHubBroker is b, the broker of plugin id, with what only the GitHub
// plugin is served: the store keeps the viewer it names, which the rows
// and facts read with no call.
func (e *Env) withGitHubBroker(b *psource.Broker, id string, db *sqlite.DB) *psource.Broker {
	if id != string(unit.GitHub) {
		return b
	}
	st := inventory.New(db)
	note := b.Note
	b.Note = func(t port.Topic, units ...unit.Unit) {
		if note != nil {
			note(t, units...)
		}
		if t&port.TopicViewer != 0 {
			go e.keepGitHubIdentity(context.Background(), st)
		}
	}
	return b
}

// keepGitHubIdentity keeps the viewer the running GitHub plugin names in st.
func (e *Env) keepGitHubIdentity(ctx context.Context, st *inventory.Store) {
	c, err := e.githubPlugin()
	if err != nil {
		return
	}
	id, err := c.Identity(ctx)
	if err == nil {
		err = ghsource.KeepIdentity(ctx, st, id)
	}
	if err != nil && !errors.Is(err, src.ErrUnavailable) {
		e.warnf("GitHub viewer: %v", err)
	}
}

// githubLoaded is what serve does once the GitHub plugin loaded: its
// budget and its viewer reach the host.
func (e *Env) githubLoaded(ctx context.Context, db *sqlite.DB) {
	e.afterGitHubJob(ctx)
	e.keepGitHubIdentity(ctx, inventory.New(db))
}

// workspaceListing is the host's workspaces of repo, "owner/name", every
// one when "": the managers' repositories and workspaces, each with its
// repository, its agents' states and the pull request its manager links;
// partial when a manager could not be listed.
func (e *Env) workspaceListing(ctx context.Context, repo string) (src.Listing, error) {
	if e.WS == nil {
		return src.Listing{}, nil
	}
	e.WS.Invalidate(wsdomain.PartMutable)
	repos, err := e.WS.Repos(ctx)
	if err != nil {
		return src.Listing{}, err
	}
	list, failed, err := e.WS.ListAll(ctx)
	if err != nil {
		return src.Listing{}, err
	}
	activity, err := e.WS.AgentActivity(ctx)
	if err != nil {
		e.warnf("agent activity: %v", err)
	}
	out := src.Listing{Partial: len(failed) > 0}
	type key struct{ manager, id string }
	byID := map[key]wsdomain.Repo{}
	for _, r := range repos {
		byID[key{r.Manager, r.ID}] = r
		out.Repos = append(out.Repos, src.Repo{ID: r.ID, Remote: r.RemoteKey, Path: r.Path, DefaultBranch: r.DefaultBranch})
	}
	for _, w := range list {
		r := byID[key{w.Manager, w.RepoID}]
		name := ""
		if owner, n, ok := r.OwnerName(); ok {
			name = owner + "/" + n
		}
		if repo != "" && !strings.EqualFold(repo, name) {
			continue
		}
		lw := src.Workspace{Manager: w.Manager, ID: w.ID, Repo: name, RepoID: w.RepoID, Path: w.Path, DisplayName: w.DisplayName,
			Comment: w.Comment, Branch: w.Branch, PushBranch: w.PushBranch, Status: w.Status, IsMain: w.IsMain, Local: w.HostID == wsdomain.LocalHost}
		for _, s := range activity[w.ID].States {
			lw.AgentStates = append(lw.AgentStates, s.String())
		}
		if owner, n, ok := r.OwnerName(); ok && w.LinkedPR != 0 {
			lw.Units = append(lw.Units, psource.Wire(pr.Ref{Owner: owner, Repo: n, Number: w.LinkedPR}.Unit()))
		}
		out.Workspaces = append(out.Workspaces, lw)
	}
	return out, nil
}

// warnf writes a warning to e's error output.
func (e *Env) warnf(format string, args ...any) {
	if e.Err != nil {
		fmt.Fprintf(e.Err, "warning: "+format+"\n", args...)
	}
}

// refreshPR has the GitHub plugin read ref in full and report it.
func (e *Env) refreshPR(ctx context.Context, ref pr.Ref) error {
	_, err := e.forgeCalls().Action(ctx, src.ActionRequest{Unit: psource.Wire(ref.Unit()), Action: ghsource.ActionRefresh})
	return err
}

// pluginLoaded is what serve does once plugin id loaded.
func (e *Env) pluginLoaded(ctx context.Context, db *sqlite.DB, id string) {
	if id == string(unit.GitHub) {
		e.githubLoaded(ctx, db)
	}
}

// sourceReports are what the plugins the host runs last reported, read when
// asked, as the plugins start after the modules are built; a test's
// reports over store when it sets them.
func (e *Env) sourceReports(store *inventory.Store) readmodel.Reports {
	if e.reports != nil {
		return e.reports(store)
	}
	return lateReports{e}
}

type lateReports struct{ e *Env }

func (r lateReports) Units(ctx context.Context, p unit.ProviderID) ([]unit.Unit, error) {
	if r.e.pluginKind == nil {
		return nil, nil
	}
	return r.e.pluginKind.Snapshots.Units(ctx, p)
}

func (r lateReports) Reported(ctx context.Context, units []unit.Unit) (map[unit.Unit]src.Snapshot, error) {
	if r.e.pluginKind == nil {
		return nil, nil
	}
	return r.e.pluginKind.Snapshots.Reported(ctx, units)
}

// githubHostModule is the GitHub source as it runs in the host's process: the pull
// requests the GitHub plugin reports, served as prrow's rows. Its ingestion
// job reads nothing and it publishes nothing: the composition root makes
// the plugin's job, publisher and proposals its own.
func githubHostModule(cfg config.Config, d moduleDeps) (pluginModule, error) {
	var briefs prrow.Briefs
	if d.Briefs != nil {
		briefs = d.Briefs
	}
	var cat readmodel.Presenter = psource.BundledCatalog(string(unit.GitHub))
	if d.Catalog != nil {
		cat = d.Catalog(unit.GitHub)
	}
	return pluginModule{
		ID:     unit.GitHub,
		Codec:  pr.Codec{},
		Ingest: githubIdleJob(cfg),
		Rows: func(rd readmodel.RowDeps) readmodel.Provider {
			return prrow.New(prrow.Source{RowDeps: rd, Reports: d.Reports, Store: d.Inventory, Briefs: briefs, Catalog: cat, Mutes: githubMutes(d)})
		},
		Facts:      githubFactsOf(cfg, d),
		Slow:       func(context.Context) (string, string, error) { return "skipped", "the GitHub plugin does not run", nil },
		Items:      ghsource.Cards{Reports: d.Reports}.Items,
		Card:       ghsource.Cards{Reports: d.Reports}.Card,
		Identity:   ghsource.IdentityOf(d.Inventory),
		UnitEvents: ghsource.UnitEvents{Store: d.Inventory},
		Detail:     true,
		Briefs:     brief.GitHubSource{Store: prrow.ReportedPRs{Reports: d.Reports}, Retry: time.Duration(cfg.Sweep.Slow)},
	}, nil
}

func githubFactsOf(cfg config.Config, d moduleDeps) automation.Units {
	fd := ghsource.FactsDeps{Reports: d.Reports, Store: d.Inventory, WS: d.WS, Git: d.Git, Cfg: cfg, Clock: d.Clock, Intents: d.Intents}
	if d.Changes != nil {
		fd.Changes = d.Changes
	}
	if d.DB != nil {
		fd.Rounds = sqlite.NewRounds(d.DB)
	}
	if d.Turns != nil {
		fd.Books = d.Turns.Store
	}
	fd.Mutes = githubMutes(d)
	return ghsource.FactsOf(fd)
}

// githubMutes are the mutes the wakes of the store's tasks hold; none
// without a store.
func githubMutes(d moduleDeps) readmodel.Mutes {
	if d.DB == nil {
		return nil
	}
	return readmodel.WakeMutes(d.DB.Wakes().UnitWakes)
}

// githubIdleJob is the ingestion job of a module whose plugin does not run: it
// reads nothing.
func githubIdleJob(cfg config.Config) ingest.Job {
	return ingest.Job{Every: time.Duration(cmp.Or(cfg.Sweep.Fast, config.Defaults().Sweep.Fast)),
		Prepare: func(context.Context, time.Time) (ingest.Plan, string, error) {
			return ingest.Plan{}, "the GitHub plugin does not run", nil
		}}
}
