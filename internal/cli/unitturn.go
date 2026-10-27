package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/instructions"
	coreplacement "github.com/wloophq/wloop/internal/core/placement"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/daemon"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/placement"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/github"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/turn"
	"github.com/wloophq/wloop/internal/workspace"
)

// unitRun is a round on a unit of a provider other than GitHub.
type unitRun struct {
	def      dintent.Def
	cfg      config.Config
	r        turn.Request
	resumeOf int64
	// manager and parent are the request's workspace manager and the
	// caller's worktree, for the worktree the round creates.
	manager, parent string
}

// unitRound sends a round of d the user asked for on u, a unit of a
// provider other than GitHub, in the workspace its placement names; the
// turn keeps the call's items and entry for its drafts.
func (e *Env) unitRound(ctx context.Context, d dintent.Def, u unit.Unit, c daemon.Call) error {
	cfg, err := e.loadConfig()
	if err != nil {
		return err
	}
	if cfg.AgentHome == "" {
		cfg.AgentHome = c.AgentHome
	}
	r := turn.Request{Intent: turn.Intent(d.Name), Trigger: turn.ByHand, Unit: u, Guidance: c.Guidance, Inputs: c.Inputs, Items: c.Items, Entry: c.Entry}
	if r.Inputs, err = e.keptRepo(ctx, d, u, r.Inputs); err != nil {
		return err
	}
	return e.runOnUnit(ctx, unitRun{def: d, cfg: cfg, r: r, manager: c.Manager, parent: c.Parent})
}

// launchUnit is cliLauncher.Launch on a unit of a provider other than
// GitHub: a new turn, or a resume of l.ResumeOf in a new session of the
// unit's placed workspace.
func (e *Env) launchUnit(ctx context.Context, d dintent.Def, l turns.Launch) error {
	cfg, err := e.loadConfig()
	if err != nil {
		return err
	}
	r := turn.Request{Intent: turn.Intent(d.Name), Trigger: requestTrigger(l.Trigger), Unit: l.Unit, Guidance: l.Guidance, Inputs: l.Inputs, Why: l.Why}
	if l.ResumeOf != 0 {
		books, err := e.Turns.Store.Books(ctx, l.Unit)
		if err != nil {
			return err
		}
		b := books[l.Unit]
		b.Unit = l.Unit
		prev, ok := turnOf(b, l.ResumeOf)
		if !ok {
			return fmt.Errorf("%s has no turn %d to resume", l.Unit.Label(), l.ResumeOf)
		}
		r.Trigger, r.Guidance = requestTrigger(prev.Trigger), cmp.Or(l.Guidance, prev.Guidance)
		if r.Inputs == nil {
			r.Inputs = prev.Inputs
		}
		r.Items, r.Entry = turns.ItemsOf(prev), turns.EntryOf(prev)
		r.Resume = instructions.ResumeOf(b, l.ResumeOf, l.Answer, "", "")
		if r.History, err = e.turnHistory(ctx, l.Unit, b); err != nil {
			return err
		}
	}
	return e.runOnUnit(ctx, unitRun{def: d, cfg: cfg, r: r, resumeOf: l.ResumeOf})
}

// runOnUnit builds and stores the round's turn, types it into a session of
// the unit's placed workspace and records it. A start blocked on the
// agent's question is recorded as the unit's start block; a round that
// gets through lifts the block.
func (e *Env) runOnUnit(ctx context.Context, run unitRun) error {
	u := run.r.Unit
	if err := e.roundReady(); err != nil {
		return err
	}
	opts := intentOptions(run.def, roundOptions{allowUserActive: run.r.Trigger == turn.ByHand, resumeOf: run.resumeOf, unit: u})
	ws, shared, err := e.runWorkspace(ctx, run, opts)
	if err != nil {
		return err
	}
	opts.names = e.nameFacts(ctx, u)
	if w := run.def.WorkspaceRepo; w != nil && w.Name.Src != "" {
		if repo, _ := runRepo(run); repo != "" {
			opts.names["workspace.name"] = ws.DisplayName
		}
	}
	activity, err := e.freshActivity(ctx)
	if err != nil {
		return err
	}
	// Activity is the workspace's: in a shared one, a waiting agent may be
	// any unit's, so the round types into no live session.
	switch a := activity[ws.ID]; {
	case a.Waiting && shared:
		opts.newSession = true
	case a.Waiting:
		return errWaiting
	case a.Busy && !shared:
		opts.keepLive, opts.busy = true, true
	}
	sent, err := e.sendTurn(ctx, run.cfg, run.r)
	if err != nil {
		return err
	}
	sent.resume(run.resumeOf)
	handle, verb, err := e.session(ctx, run.cfg, ws, u, opts)
	if err != nil {
		e.blockUnit(ctx, run, err)
		return fmt.Errorf("%s: %w", ws.DisplayName, err)
	}
	queued := opts.busy && verb == verbExisting
	submitted, err := e.deliver(ctx, u, &sent, handle, queued)
	if err != nil {
		return err
	}
	e.liftUnitBlock(ctx, u)
	if err := e.roundStarted(ctx, u, func() error { return nil }); err != nil {
		fmt.Fprintf(e.Err, "warning: %s: moving its cards: %v\n", u.Label(), err)
	}
	if !submitted {
		verb += " (submission not observed; check the session)"
	}
	fmt.Fprintf(e.Out, "%s of %s in %s, %s\n", run.def.Name, u.Label(), ws.DisplayName, verb)
	return nil
}

// runWorkspace is where run's turn goes: the repository its intent's
// workspace table names, else u's placement.
func (e *Env) runWorkspace(ctx context.Context, run unitRun, opts roundOptions) (wsdomain.Workspace, bool, error) {
	w := run.def.WorkspaceRepo
	if w == nil {
		return e.placedWorkspace(ctx, run.cfg, run.r.Unit, opts)
	}
	name, err := runRepo(run)
	if err != nil || name == "" {
		if err == nil {
			return e.neutralWorkspace(ctx, run.cfg, run.r.Unit, opts)
		}
		return wsdomain.Workspace{}, false, err
	}
	owner, repo, _ := strings.Cut(name, "/")
	run.cfg = run.cfg.For(owner, repo)
	if run.manager != "" {
		run.cfg.WorkspaceManagers = []string{run.manager}
	}
	return e.repoWorkspace(ctx, run, name, opts)
}

// keptRepo is inputs with the repository input d's newest turn on u kept,
// when inputs leave it out: a later run on the unit works where the first did.
func (e *Env) keptRepo(ctx context.Context, d dintent.Def, u unit.Unit, inputs map[string]any) (map[string]any, error) {
	w := d.WorkspaceRepo
	if w == nil || w.RepoInput == "" || inputSet(inputs[w.RepoInput]) || e.Turns == nil {
		return inputs, nil
	}
	books, err := e.Turns.Store.Books(ctx, u)
	if err != nil {
		return inputs, err
	}
	ts := books[u].Turns
	for i := len(ts) - 1; i >= 0; i-- {
		if string(ts[i].Intent) != string(d.Name) {
			continue
		}
		if repo, ok := ts[i].Inputs[w.RepoInput].(string); ok && repo != "" {
			out := maps.Clone(inputs)
			if out == nil {
				out = map[string]any{}
			}
			out[w.RepoInput] = repo
			return out, nil
		}
		break
	}
	return inputs, nil
}

// runRepo is the repository, owner/name, run's turn runs in: its
// manifest's, else its input's; "" for an optional one the turn leaves out.
func runRepo(run unitRun) (string, error) {
	w := run.def.WorkspaceRepo
	name := w.Repo
	if w.RepoInput != "" {
		name, _ = run.r.Inputs[w.RepoInput].(string)
		switch {
		case name == "" && w.Optional:
			return "", nil
		case name == "":
			return "", fmt.Errorf("%s runs in the repository its input %s names, and the turn has none", run.def.Name, w.RepoInput)
		}
	}
	if !intentload.ValidRepo(name) {
		return "", fmt.Errorf("%q is not a repository: owner/name", name)
	}
	return name, nil
}

// repoWorkspace is a workspace of the repository name (owner/name) for u:
// the main checkout for an intent that reuses its live session, else u's
// own worktree, created and kept by the manager that creates it.
func (e *Env) repoWorkspace(ctx context.Context, run unitRun, name string, opts roundOptions) (wsdomain.Workspace, bool, error) {
	cfg, u, d := run.cfg, run.r.Unit, run.def
	repo, _, err := e.repoCheckout(ctx, cfg, name)
	if err != nil {
		return wsdomain.Workspace{}, false, err
	}
	all, err := e.WS.List(ctx)
	if err != nil {
		return wsdomain.Workspace{}, false, err
	}
	if d.Session.Strategy != sessionNew {
		i := slices.IndexFunc(all, func(w wsdomain.Workspace) bool {
			return w.RepoID == repo.ID && (w.IsMain || filepath.Clean(w.Path) == filepath.Clean(repo.Path))
		})
		if i < 0 {
			return wsdomain.Workspace{}, false, fmt.Errorf("%s runs in the main checkout of %s, which %s does not list", d.Name, name, e.managerDisplay())
		}
		return all[i], true, nil
	}
	if ws, ok, err := e.unitWorkspace(ctx, u, repo, all); err != nil || ok {
		return ws, false, err
	}
	m, err := e.chooseManager(ctx, cfg, opts)
	if err != nil {
		return wsdomain.Workspace{}, false, err
	}
	in, err := e.repoIn(ctx, m, repo)
	if err != nil {
		return wsdomain.Workspace{}, false, err
	}
	wsName, err := e.loadedIntents().WorkspaceName(d.Name, e.nameFacts(ctx, u))
	if err != nil {
		return wsdomain.Workspace{}, false, err
	}
	req := workspace.CreateRequest{RepoID: in.ID, Name: cmp.Or(wsName, unitWorkspaceName(u)), Unit: u, DefaultBase: true, Parent: run.parent}
	ws, err := e.createWorkspace(ctx, m, req, repo.Path)
	if err == nil && ws.ParentDropped {
		fmt.Fprintf(e.Out, "%s keeps %s under no parent: it lists no worktree at %s\n", e.displayOf([]string{m.Name()}), ws.DisplayName, run.parent)
	}
	return ws, false, err
}

// repoCheckout is the main checkout of the repository name (owner/name):
// the one a manager knows, else the one in the project's projects_dir,
// cloned there when absent; cloned says it was cloned now.
func (e *Env) repoCheckout(ctx context.Context, cfg config.Config, name string) (wsdomain.Repo, bool, error) {
	repos, err := e.WS.Repos(ctx)
	if err != nil {
		return wsdomain.Repo{}, false, err
	}
	owner, repo, _ := strings.Cut(name, "/")
	ref := github.Ref{Owner: owner, Repo: repo}
	return e.checkout(ctx, cfg, repos, ref.RemoteKey(), ref, "")
}

// placedWorkspace is the workspace u's placement names, and whether that
// placement shares it with other units. A unit with no placement runs in
// the neutral repository: its turn never goes to whatever terminal was
// used last, nor into a project's checkout.
func (e *Env) placedWorkspace(ctx context.Context, cfg config.Config, u unit.Unit, opts roundOptions) (wsdomain.Workspace, bool, error) {
	p, err := e.placements(cfg).Place(ctx, u, "")
	if errors.Is(err, coreplacement.ErrUnplaced) {
		return e.neutralWorkspace(ctx, cfg, u, opts)
	}
	if err != nil {
		return wsdomain.Workspace{}, false, err
	}
	all, err := e.WS.List(ctx)
	if err != nil {
		return wsdomain.Workspace{}, false, err
	}
	if filepath.IsAbs(p.Checkout) {
		i := slices.IndexFunc(all, func(w wsdomain.Workspace) bool { return filepath.Clean(w.Path) == filepath.Clean(p.Checkout) })
		if i < 0 {
			return wsdomain.Workspace{}, false, fmt.Errorf("%s is placed in %s, which no %s workspace holds", u.Label(), p.Checkout, e.managerDisplay())
		}
		return all[i], p.Shared, nil
	}
	repos, err := e.WS.Repos(ctx)
	if err != nil {
		return wsdomain.Workspace{}, false, err
	}
	repo, ok := placedRepo(repos, p.Repo)
	if !ok {
		return wsdomain.Workspace{}, false, fmt.Errorf("%s is placed in the repository %q, which %s does not know", u.Label(), p.Repo, e.managerDisplay())
	}
	if p.Checkout == placement.CheckoutMain {
		i := slices.IndexFunc(all, func(w wsdomain.Workspace) bool {
			return w.RepoID == repo.ID && (w.IsMain || filepath.Clean(w.Path) == filepath.Clean(repo.Path))
		})
		if i < 0 {
			return wsdomain.Workspace{}, false, fmt.Errorf("%s is placed in the main checkout of %s, which %s does not list", u.Label(), p.Repo, e.managerDisplay())
		}
		return all[i], p.Shared, nil
	}
	if ws, ok, err := e.unitWorkspace(ctx, u, repo, all); err != nil || ok {
		return ws, p.Shared, err
	}
	m, err := e.chooseManager(ctx, cfg, opts)
	if err != nil {
		return wsdomain.Workspace{}, false, err
	}
	in, err := e.repoIn(ctx, m, repo)
	if err != nil {
		return wsdomain.Workspace{}, false, err
	}
	ws, err := e.createUnitWorkspace(ctx, m, in, repo.Path, u)
	return ws, p.Shared, err
}

// createUnitWorkspace creates u's worktree of repo in m and records m's
// custody of it.
func (e *Env) createUnitWorkspace(ctx context.Context, m workspace.Manager, repo wsdomain.Repo, path string, u unit.Unit) (wsdomain.Workspace, error) {
	return e.createWorkspace(ctx, m, workspace.CreateRequest{RepoID: repo.ID, Name: unitWorkspaceName(u), Unit: u}, path)
}

// createWorkspace creates req's worktree in m and records m's custody of
// it for req's unit.
func (e *Env) createWorkspace(ctx context.Context, m workspace.Manager, req workspace.CreateRequest, path string) (wsdomain.Workspace, error) {
	u := req.Unit
	ws, _, err := m.Create(ctx, req)
	if err != nil {
		return wsdomain.Workspace{}, err
	}
	ws.Manager = m.Name()
	if err := e.WS.Keep(ctx, m, ws, path, true, u); err != nil {
		fmt.Fprintf(e.Err, "warning: %s: recording who owns it: %v\n", ws.DisplayName, err)
	}
	e.WS.Invalidate(wsdomain.PartWorkspaces)
	fmt.Fprintf(e.Out, "created %s for %s\n", ws.DisplayName, u.Label())
	return ws, nil
}

// neutralDir is the repository sessions on a unit with no placement run
// in: empty, under the state directory, so no project's checkout is open
// to them.
func (e *Env) neutralDir() string { return filepath.Join(e.StateDir, "neutral") }

// neutralRepo creates the neutral repository with one empty commit, so a
// workspace manager can add it and branch from it.
func (e *Env) neutralRepo(ctx context.Context) (string, error) {
	dir := e.neutralDir()
	if fi, err := os.Stat(filepath.Join(dir, ".git")); err == nil && fi.IsDir() {
		return dir, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	who := []string{"GIT_AUTHOR_NAME=" + brand.Name, "GIT_AUTHOR_EMAIL=" + brand.Name + "@localhost",
		"GIT_COMMITTER_NAME=" + brand.Name, "GIT_COMMITTER_EMAIL=" + brand.Name + "@localhost"}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "neutral"}} {
		if _, err := e.Git.R.Run(ctx, dir, who, args...); err != nil {
			return "", fmt.Errorf("creating the neutral repository %s: %w", dir, err)
		}
	}
	return dir, nil
}

// neutralWorkspace is where a session on u, a unit with no placement,
// runs: the neutral repository's main checkout when the manager lists it,
// shared by every such unit, else a worktree of it for u.
func (e *Env) neutralWorkspace(ctx context.Context, cfg config.Config, u unit.Unit, opts roundOptions) (wsdomain.Workspace, bool, error) {
	dir, err := e.neutralRepo(ctx)
	if err != nil {
		return wsdomain.Workspace{}, false, err
	}
	main := func(all []wsdomain.Workspace) int {
		return slices.IndexFunc(all, func(w wsdomain.Workspace) bool { return filepath.Clean(w.Path) == filepath.Clean(dir) })
	}
	all, err := e.WS.List(ctx)
	if err != nil {
		return wsdomain.Workspace{}, false, err
	}
	if i := main(all); i >= 0 {
		return all[i], true, nil
	}
	repos, err := e.WS.Repos(ctx)
	if err != nil {
		return wsdomain.Workspace{}, false, err
	}
	neutral := slices.DeleteFunc(slices.Clone(all), func(w wsdomain.Workspace) bool {
		return !slices.ContainsFunc(repos, func(r wsdomain.Repo) bool {
			return r.Manager == w.Manager && r.ID == w.RepoID && filepath.Clean(r.Path) == filepath.Clean(dir)
		})
	})
	if ws, ok, err := e.unitWorkspace(ctx, u, wsdomain.Repo{}, neutral); err != nil || ok {
		return ws, false, err
	}
	m, err := e.chooseManager(ctx, cfg, opts)
	if err != nil {
		return wsdomain.Workspace{}, false, err
	}
	in, err := e.repoIn(ctx, m, wsdomain.Repo{Path: dir})
	if err != nil {
		return wsdomain.Workspace{}, false, err
	}
	e.WS.Invalidate(wsdomain.PartWorkspaces)
	if all, err = e.WS.List(ctx); err != nil {
		return wsdomain.Workspace{}, false, err
	}
	if i := main(all); i >= 0 {
		return all[i], true, nil
	}
	ws, err := e.createUnitWorkspace(ctx, m, in, dir, u)
	return ws, false, err
}

// unitWorkspace is the workspace of repo where u's newest session ran, else
// one the custody record says stands for u, when a manager still lists it.
func (e *Env) unitWorkspace(ctx context.Context, u unit.Unit, repo wsdomain.Repo, all []wsdomain.Workspace) (wsdomain.Workspace, bool, error) {
	ofRepo := func(w wsdomain.Workspace) bool { return w.Manager != repo.Manager || w.RepoID == repo.ID }
	if c := e.WS.Custody(); c != nil {
		kept, err := c.Worktrees(ctx)
		if err != nil {
			return wsdomain.Workspace{}, false, err
		}
		for _, k := range kept {
			if !slices.Contains(k.Units, u) {
				continue
			}
			if i := slices.IndexFunc(all, func(w wsdomain.Workspace) bool { return w.Path == k.Path && ofRepo(w) }); i >= 0 {
				return all[i], true, nil
			}
		}
	}
	if e.Turns == nil {
		return wsdomain.Workspace{}, false, nil
	}
	sessions, err := e.Turns.Registry.Sessions(ctx)
	if err != nil {
		return wsdomain.Workspace{}, false, err
	}
	sessions = slices.DeleteFunc(sessions, func(s dturn.Session) bool { return s.Unit != u })
	slices.SortStableFunc(sessions, func(a, b dturn.Session) int { return b.StartedAt.Compare(a.StartedAt) })
	for _, s := range sessions {
		if i := slices.IndexFunc(all, func(w wsdomain.Workspace) bool {
			return w.ID == s.WorkspaceID && (s.Manager == "" || w.Manager == s.Manager) && ofRepo(w)
		}); i >= 0 {
			return all[i], true, nil
		}
	}
	return wsdomain.Workspace{}, false, nil
}

// placedRepo is the repository a placement names as "owner/name".
func placedRepo(repos []wsdomain.Repo, name string) (wsdomain.Repo, bool) {
	owner, repo, ok := strings.Cut(name, "/")
	if !ok {
		return wsdomain.Repo{}, false
	}
	found := wsdomain.FindRepos(repos, "github.com/"+owner+"/"+repo, "")
	if len(found) == 0 {
		return wsdomain.Repo{}, false
	}
	return found[0], true
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// unitWorkspaceName names the workspace created for u.
func unitWorkspaceName(u unit.Unit) string {
	return string(u.Provider) + "-" + strings.Trim(unsafeName.ReplaceAllString(u.Key, "-"), "-")
}

// blockUnit records the start block of a round on u whose session waits on
// the agent's question, as the sweep records a pull request's.
func (e *Env) blockUnit(ctx context.Context, run unitRun, err error) {
	var untrusted *turns.UntrustedError
	var asked *turns.QuestionError
	var block inventory.StartBlock
	why := err.Error()
	switch {
	case errors.As(err, &untrusted):
		block = inventory.StartBlock{Question: turns.QuestionTrust, Root: untrusted.Root, Folder: untrusted.Folder, Home: untrusted.Home,
			Homes: e.trustHomes(run.cfg, untrusted.Home)}
		why = untrustedWhy(untrusted)
	case errors.As(err, &asked):
		block = inventory.StartBlock{Question: asked.Question, Root: asked.Root}
	default:
		return
	}
	if e.Rec == nil {
		return
	}
	block.Round = string(run.def.Name)
	now := e.Now()
	ev := port.Event{Unit: run.r.Unit, Kind: dinv.KindRoundBlocked, Key: block.Key(now), At: now, Data: why}
	if rerr := e.Rec.RecordUnit(context.WithoutCancel(ctx), ev); rerr != nil {
		fmt.Fprintf(e.Err, "warning: %s: recording the blocked round: %v\n", run.r.Unit.Label(), rerr)
	}
}

// liftUnitBlock records u's start block lifted by a round that went out.
func (e *Env) liftUnitBlock(ctx context.Context, u unit.Unit) {
	if e.Rec == nil {
		return
	}
	if _, err := e.startBlockOf(ctx, u); err == nil {
		e.recordLifted(context.WithoutCancel(ctx), u)
	}
}
