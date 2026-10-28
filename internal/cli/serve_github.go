package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	src "github.com/wloophq/wloop/pkg/plugin/source"

	dgh "github.com/wloophq/wloop/internal/domain/github"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/turn"

	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"
	"github.com/wloophq/wloop/internal/adapter/source/github/present"
	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/daemon"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/ghcli"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/termfmt"
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

// restoreBudget sets the client's floors from cfg and seeds its budget with
// the one the last sweep kept.
func (e *Env) restoreBudget(ctx context.Context, db *sqlite.DB, cfg config.Config, logf func(string, ...any)) {
	e.GitHub.SetFloors(github.DefaultFloors(cfg.GitHub.RateLimitFraction))
	limits, err := storedGitHubBudget(ctx, inventory.New(db))
	if err != nil {
		logf("GitHub budget: %v", err)
		return
	}
	e.GitHub.Restore(limits)
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

// githubPresenter are the GitHub presenter's deps over the env's client.
func (e *Env) githubPresenter(store *inventory.Store, host src.Host, briefs *interruptFirst) present.Deps {
	deps := present.Deps{Store: store, Host: host, Explorer: e.GitHub, Budget: e.githubBudget(), FailedSteps: failedSteps(e.GitHub)}
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

// githubSource is the GitHub source a pull request is read through outside
// the jobs: no workspaces, sink or log.
func (e *Env) githubSource(cfg config.Config) *ghsource.Collector {
	return &ghsource.Collector{Store: e.Store, GH: e.GitHub, Cfg: cfg, Now: e.Now}
}

// storedGitHubBudget is the GitHub budget the last sweep kept.
func storedGitHubBudget(ctx context.Context, st *inventory.Store) ([]dgh.Limit, error) {
	return ghsource.StoredBudget(ctx, st)
}

func (e *Env) githubBudget() daemon.Budget { return ghsource.Budget{Limiter: e.GitHub} }

// githubIdentity is the viewer as the GitHub plugin knows them.
func (e *Env) githubIdentity(st *inventory.Store, logf func(string, ...any)) func(context.Context) (src.Identity, error) {
	return ghsource.Identity(st, e.GitHub, logf)
}

// githubLocator is the pull request of the checkout's branch, as gh reads it.
type githubLocator struct{ e *Env }

func (g githubLocator) Locate(ctx context.Context, dir string) (unit.Unit, bool, error) {
	out, err := ghcli.RunIn(ctx, g.e.GH, dir, "pr", "view", "--json", "url", "--jq", ".url")
	if err != nil {
		return unit.Unit{}, false, err
	}
	ref, err := pr.ParseURL(strings.TrimSpace(string(out)))
	if err != nil {
		return unit.Unit{}, false, err
	}
	return ref.Unit(), true, nil
}

func (e *Env) githubLocators() []unit.Locator { return []unit.Locator{githubLocator{e}} }

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
		if fp := e.reviewFingerprint(ctx, r.cfg, r.ref); fp != "" {
			return e.updateRounds(ctx, func(st *round.Store) { st.SetMark(r.ref.String(), round.Reviewed, fp) })
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
