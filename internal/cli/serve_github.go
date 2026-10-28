package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"

	"github.com/wloophq/wloop/internal/brief"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dchange "github.com/wloophq/wloop/internal/domain/github/change"
	"github.com/wloophq/wloop/internal/domain/round"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/internal/source"
	"github.com/wloophq/wloop/internal/turn"

	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"
	"github.com/wloophq/wloop/internal/adapter/source/github/present"
	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/config"
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

// githubClients are GitHub's clients while its plugin runs in the host's
// process.
type githubClients struct {
	ghsource.Clients
	// viaPlugin makes serve send the API's requests through the GitHub
	// plugin.
	viaPlugin bool
}

func newGitHubClients() githubClients {
	return githubClients{Clients: ghsource.NewClients(), viaPlugin: true}
}

// restoreBudget sets the client's floors from cfg and seeds its budget with
// the one the GitHub plugin keeps, when it runs.
func (e *Env) restoreBudget(ctx context.Context, cfg config.Config, logf func(string, ...any)) {
	e.github.SetFloors(cfg.GitHub.RateLimitFraction)
	if err := e.syncGitHubBudget(ctx); err != nil && !errors.Is(err, src.ErrUnavailable) && !errors.Is(err, src.ErrUnsupported) {
		logf("GitHub budget: %v", err)
	}
}

// syncGitHubBudget seeds the client's budget with the one the running
// GitHub plugin keeps, which its own reads spent.
func (e *Env) syncGitHubBudget(ctx context.Context) error {
	raw, err := e.forgeCalls().Action(ctx, src.ActionRequest{Action: ghsource.ActionBudget})
	if err != nil {
		return err
	}
	var limits map[dgh.Resource]dgh.Limit
	if err := json.Unmarshal(raw, &limits); err != nil {
		return fmt.Errorf("the GitHub plugin's budget: %w", err)
	}
	e.github.API.Restore(slices.Collect(maps.Values(limits)))
	return nil
}

// githubPlugin is the running GitHub plugin's client.
func (e *Env) githubPlugin() (*src.Client, error) {
	if e.pluginKind == nil {
		return nil, fmt.Errorf("the GitHub plugin: %w", src.ErrUnavailable)
	}
	return e.pluginKind.Client(string(unit.GitHub))
}

// failedSteps reads the failed steps of a check's job from GitHub.
func (e *Env) failedSteps() func(ctx context.Context, owner, repo string, job int64) ([]string, error) {
	return ghsource.FailedSteps(e.github.API)
}

// withGitHubClient is d with GitHub's API client.
func (e *Env) withGitHubClient(d source.Deps) source.Deps {
	d.GitHub = e.github.API
	return d
}

// withGitHubBriefs is d briefing pull requests read from GitHub, under its
// budget.
func (e *Env) withGitHubBriefs(d brief.Deps) brief.Deps {
	d.Source, d.Allow = brief.Source{GitHub: e.github.API}, e.github.API.Allow
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
	deps := present.Deps{Store: store, Reports: e.sourceReports(store), Host: host, Explorer: e.github.API, Budget: e.githubBudget(), FailedSteps: e.failedSteps()}
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

func (e *Env) githubBudget() daemon.Budget { return ghsource.Budget{Limiter: e.github.API} }

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
// set on e, else the running plugin's client, else GitHub's in process.
func (e *Env) forgeCalls() forgeCalls {
	if e.forge != nil {
		return e.forge
	}
	if e.pluginKind != nil {
		if c, err := e.pluginKind.Client(string(unit.GitHub)); err == nil {
			return c
		}
	}
	return e.github.Local()
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

// clearFingerprint forgets the reviews the address rounds on ref covered, so
// a pull request taken over starts its triage from none of them.
func (e *Env) clearFingerprint(ctx context.Context, ref pr.Ref) error {
	if e.Rounds == nil {
		return nil
	}
	st, err := e.Rounds.Load(ctx)
	if err != nil {
		return err
	}
	marks := ghsource.TakenOverMarks()
	if !marksChange(st, ref.String(), marks) {
		return nil
	}
	setMarks(st, ref.String(), marks)
	return e.Rounds.Save(ctx, st)
}

func marksChange(st *round.Store, key string, marks map[string]string) bool {
	for name, v := range marks {
		if st.Mark(key, name) != v {
			return true
		}
	}
	return false
}

// recordAddressed marks the reviews so far and the checks and the conflict
// of head as covered by a round asked for by hand.
func (e *Env) recordAddressed(st *round.Store, ref pr.Ref, head string) {
	setMarks(st, ref.String(), ghsource.AddressedMarks(e.Now()))
	st.SetHeads(ref.String(), head, head)
}

// recordReviewed marks the reviews of full a held review round in ws
// covered.
func (e *Env) recordReviewed(st *round.Store, cfg config.Config, ref pr.Ref, ws wsdomain.Workspace, full *pr.PullRequest, viewer string) {
	setMarks(st, ref.String(), ghsource.ReviewedMarks(dchange.ReviewerInput{PR: full.Facts, Viewer: viewer, WorkspaceStatus: ws.Status,
		CompletedStatus: e.column(cfg, config.ColumnCompleted), TrustedOwners: []string{ref.Owner}, TrustedAuthors: cfg.GitHub.TrustedAuthors}))
}

// reviewedMarks cover what the review round just sent saw, so the sweep
// plans no follow-up of the same reviews; nil when they cannot be read.
func (e *Env) reviewedMarks(ctx context.Context, cfg config.Config, ref pr.Ref) map[string]string {
	full, viewer, err := e.coveredPR(ctx, ref)
	if err != nil {
		fmt.Fprintf(e.Err, "warning: %s: reading the reviews the round covers: %v\n", ref, err)
		return nil
	}
	return ghsource.ReviewedMarks(dchange.ReviewerInput{PR: full.Facts, Viewer: viewer, TrustedOwners: []string{ref.Owner}, TrustedAuthors: cfg.GitHub.TrustedAuthors})
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

// githubPayloads is GitHub's turn payload, over the read rounds make.
func (e *Env) githubPayloads() turn.PayloadReader { return ghsource.TurnPayloads{PRs: basicsReader{e}} }

// nudgedWho is the input nudge.prepare fills with whom the round asks,
// which pr.nudged records.
const nudgedWho = "who"

// githubEffects is the GitHub plugin's ApplyEffect while it runs in
// process: the turn effects of r, a round that went out on route rt. A
// draft's pr.nudged is the publisher's, applied as it posts.
type githubEffects struct {
	rt route
	r  *sentRound
}

func (g githubEffects) ApplyEffect(ctx context.Context, req src.EffectRequest) error {
	r := g.r
	if req.Draft != nil || r == nil {
		return nil
	}
	e := r.env
	switch req.Effect {
	case ghsource.EffectNudged:
		e.recordTurnEvent(ctx, r.ref, dinv.KindNudged, r.id, strings.Join(inputList(r.inputs[nudgedWho]), ", "))
	case ghsource.EffectReviewed:
		if g.rt != routeLaunch || !r.submitted {
			return nil
		}
		if marks := e.reviewedMarks(ctx, r.cfg, r.ref); marks != nil {
			return e.updateRounds(ctx, func(st *round.Store) { setMarks(st, r.ref.String(), marks) })
		}
	case ghsource.EffectAddressed:
		if r.pr == nil {
			return nil
		}
		return e.updateRounds(ctx, func(st *round.Store) { e.recordAddressed(st, r.ref, r.pr.HeadRefOid) })
	}
	return nil
}

// turnEffects applies the effects of provider p's plugin on a turn r that
// went out on route rt: the plugin's while it runs, else GitHub's in
// process; false when neither does.
func (e *Env) turnEffects(p unit.ProviderID, rt route, r *sentRound) (effectApplier, bool) {
	if a, ok := e.runningApplier(p); ok {
		return a, true
	}
	if p == unit.GitHub {
		return githubEffects{rt: rt, r: r}, true
	}
	return nil, false
}

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

// draftAppliers apply the draft effects of each provider's plugin: the
// running plugin's, else GitHub's in process.
func (e *Env) draftAppliers() func(p unit.ProviderID) (effectApplier, bool) {
	return func(p unit.ProviderID) (effectApplier, bool) { return e.turnEffects(p, 0, nil) }
}

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
func (e *Env) githubModule(build source.Build) source.Build {
	return func(cfg config.Config, d source.Deps) (source.Module, error) {
		m, err := build(cfg, d)
		if err != nil || e.pluginKind == nil {
			return m, err
		}
		return e.relayGitHub(cfg, m), nil
	}
}

// relayGitHub is m with its ingestion, slow sweep, tracks and checkouts
// relayed to the GitHub plugin serve runs.
func (e *Env) relayGitHub(cfg config.Config, m source.Module) source.Module {
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
	return m
}

// afterGitHubJob seeds the host's GitHub client with the budget the
// plugin's job left.
func (e *Env) afterGitHubJob(ctx context.Context) {
	if err := e.syncGitHubBudget(ctx); err != nil && !errors.Is(err, src.ErrUnavailable) && !errors.Is(err, src.ErrUnsupported) {
		e.warnf("GitHub budget: %v", err)
	}
}

// pluginCheckouts are a plugin's checkouts as a module serves them.
type pluginCheckouts struct{ c psource.Checkouts }

func (p pluginCheckouts) Checkout(ctx context.Context, u unit.Unit) (source.Checkout, bool, error) {
	c, ok, err := p.c.Checkout(ctx, u)
	return source.Checkout{Repo: c.Repo, Branch: c.Branch, Head: c.Head}, ok, err
}

// withGitHubBroker is b, the broker of plugin id, with what only the GitHub
// plugin is served: the store keeps the pull requests it reports and the
// viewer it names, for the rows, facts and presenter that still run in the
// host's process.
func (e *Env) withGitHubBroker(b *psource.Broker, id string, db *sqlite.DB) *psource.Broker {
	if id != string(unit.GitHub) {
		return b
	}
	st := inventory.New(db)
	b.Reported = ghsource.Reported(st)
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

// viaGitHubPlugin points e's GitHub client at the running GitHub plugin,
// which sends every request with its credentials, unless a test set the
// client.
func (e *Env) viaGitHubPlugin() {
	if !e.github.viaPlugin {
		return
	}
	e.github.API = ghsource.PluginClient(func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		c, err := e.githubPlugin()
		if err != nil {
			return nil, err
		}
		return c.Action(ctx, src.ActionRequest{Action: ghsource.ActionForward, Args: args})
	}, e.Now)
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
