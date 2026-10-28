package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	src "github.com/wloophq/wloop/pkg/plugin/source"

	dgh "github.com/wloophq/wloop/internal/domain/github"
	"github.com/wloophq/wloop/internal/github"

	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"
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

// withoutGitHub is d with nothing of GitHub's when its plugin is disabled:
// no brief queue, intakes, explorer, budget or viewer read.
func withoutGitHub(d daemon.Deps) daemon.Deps {
	if d.Cfg.PluginEnabled(string(unit.GitHub)) {
		return d
	}
	d.Brief, d.BriefStatus, d.BriefViews, d.BriefsUnavailable = nil, nil, nil, ""
	d.Intakes, d.IntakeBriefs, d.Explorer, d.Budget, d.Identity = nil, nil, nil, nil, nil
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

// githubBudget is the GitHub client's budget.
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

// githubLocators are the built-in GitHub module's locator.
func (e *Env) githubLocators() []unit.Locator { return []unit.Locator{githubLocator{e}} }
