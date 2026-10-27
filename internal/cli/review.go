package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/wloophq/wloop/internal/config"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/gitx"
	"github.com/wloophq/wloop/internal/workspace"
)

// openReview creates the review workspace for ref, or starts a new round in
// the existing one. submitted reports whether the agent was seen taking the
// prompt.
func (e *Env) openReview(ctx context.Context, cfg config.Config, ref github.Ref, pr *github.PullRequest, sent sentTurn, opts roundOptions) (submitted bool, err error) {
	opts.trusted = trusts(cfg, pr)
	repos, err := e.WS.Repos(ctx)
	if err != nil {
		return false, err
	}
	all, err := e.WS.List(ctx)
	if err != nil {
		return false, err
	}
	remoteKey := ref.RemoteKey()
	existing, found := findReviewWorkspace(cfg, repos, all, remoteKey, ref.Number, pr.HeadRefName)
	if w, _, held := branchHolder(repos, all, ref, pr); held && !found {
		return false, fmt.Errorf("%s; free the branch for a review workspace", checkedOutIn(pr.HeadRefName, w.Path))
	}

	repo, err := e.resolveCheckout(ctx, cfg, repos, remoteKey, ref, existing.RepoID)
	if err != nil {
		return false, err
	}
	if err := e.Git.FetchPR(ctx, repo.Path, ref.Number); err != nil {
		return false, err
	}

	ws := existing
	var action string
	if found {
		var verb string
		if submitted, verb, err = e.startRound(ctx, cfg, ws, ref, &sent, opts); err != nil {
			return false, fmt.Errorf("%s: %w", ws.DisplayName, err)
		}
		action = "new round in " + ws.DisplayName + ", " + verb
	} else {
		if cfg.GitHub.FollowupsWithoutWorkspace {
			if err := e.resetLeftoverBranch(ctx, repo.Path, ref, pr); err != nil {
				return false, err
			}
		}
		if why, err := e.localBranchProblem(ctx, repo.Path, pr, true); err != nil || why != "" {
			return false, cmp.Or(err, errors.New(why))
		}
		ws, submitted, err = e.createWithAgent(ctx, cfg, ref, repo, workspace.CreateRequest{
			Name:    cfg.GitHub.ReviewPrefix + pr.HeadRefName,
			BaseRef: "origin/pr/" + fmt.Sprint(ref.Number),
			Unit:    ref.Unit(),

			HeadBranch:      pr.HeadRefName,
			HeadOid:         pr.HeadRefOid,
			BaseBranchName:  pr.BaseRefName,
			CrossRepository: pr.IsCrossRepository,
		}, sent, opts)
		if err != nil {
			return false, err
		}
		e.noteWorkspace(ctx, ref, ws, true)
		action = "created " + ws.DisplayName
	}
	if !submitted {
		action += " (submission not observed; check the session)"
	}

	e.alignWithPR(ctx, ws, pr)
	if err := e.setReviewStatus(ctx, cfg, ref, ws, false); err != nil {
		fmt.Fprintf(e.Err, "warning: setting the board status: %v\n", err)
	}
	fmt.Fprintf(e.Out, "%s: %s\n%s\n", action, pr.Title, ws.Path)
	return submitted, nil
}

// resetLeftoverBranch moves the PR's local head branch, left behind by a
// review workspace the user deleted, to the PR head when its tip is a head
// the loop stored for the pull request: a force push leaves it off the PR.
func (e *Env) resetLeftoverBranch(ctx context.Context, dir string, ref github.Ref, p *github.PullRequest) error {
	branch, head := p.HeadRefName, p.HeadRefOid
	if e.Turns == nil || p.IsCrossRepository || branch == "" || head == "" {
		return nil
	}
	tip, err := e.Git.LocalBranchTip(ctx, dir, branch)
	if err != nil || tip == "" || tip == head {
		return err
	}
	if path, err := e.Git.CheckedOutAt(ctx, dir, branch); err != nil || path != "" {
		return err
	}
	books, err := e.Turns.Store.Books(ctx, pr.Ref(ref).Unit())
	if err != nil {
		return err
	}
	if !storedHead(books[pr.Ref(ref).Unit()], tip) {
		return nil
	}
	return e.Git.UpdateRef(ctx, dir, "refs/heads/"+branch, head, tip)
}

func storedHead(b dturn.Book, oid string) bool {
	return slices.ContainsFunc(b.Turns, func(t dturn.Turn) bool { return t.Head == oid }) ||
		slices.ContainsFunc(b.Records, func(r dturn.Record) bool { return r.Head == oid })
}

// createWithAgent creates the workspace in repo, under the first manager that
// can launch the round's session, and starts its agent, so the session always
// runs with the home and settings the loop chose. A workspace whose agent
// never got its prompt, or has no loop MCP tool to take it, is removed, so the
// next round creates it again. A sent turn with no prompt starts the agent
// alone. submitted reports whether the agent was seen taking the prompt.
func (e *Env) createWithAgent(ctx context.Context, cfg config.Config, ref github.Ref, repo wsdomain.Repo, req workspace.CreateRequest, sent sentTurn, opts roundOptions) (wsdomain.Workspace, bool, error) {
	opts.unit = ref.Unit()
	m, err := e.chooseManager(ctx, cfg, opts)
	if err != nil {
		return wsdomain.Workspace{}, false, err
	}
	in, err := e.repoIn(ctx, m, repo)
	if err != nil {
		return wsdomain.Workspace{}, false, err
	}
	req.RepoID = in.ID
	ws, _, err := m.Create(ctx, req)
	if err != nil {
		return ws, false, err
	}
	ws.Manager = m.Name()
	if err := e.WS.Keep(ctx, m, ws, repo.Path, true, req.Unit); err != nil {
		fmt.Fprintf(e.Err, "warning: %s: recording who owns it: %v\n", ws.DisplayName, err)
	}
	if !req.CrossRepository && ws.BranchRenamed {
		return ws, false, e.discard(ctx, ws, fmt.Errorf("%s created %s on branch %s instead of %s, which is taken", e.displayOf([]string{m.Name()}), ws.DisplayName, ws.Branch, req.HeadBranch))
	}
	s, err := e.agentSession(ctx, cfg, ws, opts)
	if err != nil {
		return ws, false, e.discard(ctx, ws, err)
	}
	if sent.Prompt == "" {
		start, err := e.startRequest(cfg, ws, s, "")
		if err != nil {
			return ws, false, e.discard(ctx, ws, err)
		}
		start.Title = cfg.Agent
		p := proc{id: start.Session, m: m}
		if p.handle, err = m.StartAgentSession(ctx, start); err != nil {
			return ws, false, fmt.Errorf("%s: starting the agent: %w", ws.DisplayName, e.explainStart(ctx, cfg, ws, err, opts.trusted))
		}
		if err := e.launched(ctx, cfg, ws, ref.Unit(), p); err != nil {
			fmt.Fprintf(e.Err, "warning: %s: registering the agent session: %v\n", ws.DisplayName, err)
		}
		return ws, true, nil
	}
	name, err := e.roundName(opts, ref.Unit())
	if err != nil {
		return ws, false, e.discard(ctx, ws, err)
	}
	p, err := e.startSession(ctx, cfg, ws, ref.Unit(), s, name, opts.keepBlocked, opts.trusted)
	if blocked := (*workspace.BlockedError)(nil); errors.As(err, &blocked) && opts.keepBlocked(blocked.Reason) {
		return ws, false, fmt.Errorf("%s: starting the agent: %w", ws.DisplayName, err)
	}
	if err != nil {
		return ws, false, e.discard(ctx, ws, fmt.Errorf("%s: starting the agent: %w", ws.DisplayName, err))
	}
	submitted, err := e.deliver(ctx, ref.Unit(), &sent, p, false)
	if err != nil {
		return ws, false, e.discard(ctx, ws, fmt.Errorf("%s: sending the prompt: %w", ws.DisplayName, err))
	}
	return ws, submitted, nil
}

// repoIn is repo as m registers it, registering it first when m lacks it.
func (e *Env) repoIn(ctx context.Context, m workspace.Manager, repo wsdomain.Repo) (wsdomain.Repo, error) {
	if repo.Manager == m.Name() && repo.ID != "" {
		return repo, nil
	}
	repos, err := m.Repos(ctx)
	if err != nil {
		return wsdomain.Repo{}, err
	}
	for _, r := range repos {
		if filepath.Clean(r.Path) == filepath.Clean(repo.Path) {
			r.Manager = m.Name()
			return r, nil
		}
	}
	r, err := m.AddRepo(ctx, repo.Path)
	r.Manager = m.Name()
	return r, err
}

// noteWorkspace links a workspace a round created to its pull request in the
// inventory, so the views find its card before the next collection.
func (e *Env) noteWorkspace(ctx context.Context, ref github.Ref, ws wsdomain.Workspace, review bool) {
	if e.Store == nil {
		return
	}
	if err := e.Store.SetWorkspace(ctx, ref, dinv.Workspace{Manager: ws.Manager, ID: ws.ID, DisplayName: ws.DisplayName, IsReview: review, IsMain: ws.IsMain}); err != nil {
		fmt.Fprintf(e.Err, "warning: %s: linking %s in the inventory: %v\n", ref, ws.DisplayName, err)
	}
}

// discard removes a workspace created a moment ago.
func (e *Env) discard(ctx context.Context, ws wsdomain.Workspace, cause error) error {
	m, err := e.WS.For(ctx, ws)
	if err == nil {
		err = m.Remove(ctx, ws.ID, true)
	}
	if err != nil {
		return fmt.Errorf("%w; removing it: %v", cause, err)
	}
	if c := e.WS.Custody(); c != nil {
		if d, ok := c.(interface {
			Drop(ctx context.Context, path string) error
		}); ok {
			_ = d.Drop(ctx, ws.Path)
		}
	}
	return fmt.Errorf("%w; removed it", cause)
}

// findReviewWorkspace matches by name or by linked PR, scoped to checkouts of
// the PR's repository. The workspace manager may clear the link after a
// merge, hence the name.
func findReviewWorkspace(cfg config.Config, repos []wsdomain.Repo, all []wsdomain.Workspace, remoteKey string, number int, headRef string) (wsdomain.Workspace, bool) {
	ofRepo := map[string]bool{}
	for _, r := range repos {
		if strings.EqualFold(r.RemoteKey, remoteKey) {
			ofRepo[r.ID] = true
		}
	}
	for _, w := range all {
		if !ofRepo[w.RepoID] || w.HostID != wsdomain.LocalHost || w.IsMain {
			continue
		}
		if w.DisplayName == cfg.GitHub.ReviewPrefix+headRef ||
			(w.LinkedPR == number && strings.HasPrefix(w.DisplayName, cfg.GitHub.ReviewPrefix)) {
			return w, true
		}
	}
	return wsdomain.Workspace{}, false
}

func (e *Env) resolveCheckout(ctx context.Context, cfg config.Config, repos []wsdomain.Repo, remoteKey string, ref github.Ref, preferRepoID string) (wsdomain.Repo, error) {
	repo, _, err := e.checkout(ctx, cfg, repos, remoteKey, ref, preferRepoID)
	return repo, err
}

// clones serializes the clones into one folder: a start racing another
// waits for its clone instead of taking a half-written one for a checkout.
var clones sync.Map

func lockClone(path string) func() {
	mu, _ := clones.LoadOrStore(path, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	return mu.(*sync.Mutex).Unlock
}

// checkout is the main checkout of ref's repository: one of repos, else
// the folder of projects_dir named after it, cloned there when absent and
// refused when it holds another repository; cloned says it was cloned now.
func (e *Env) checkout(ctx context.Context, cfg config.Config, repos []wsdomain.Repo, remoteKey string, ref github.Ref, preferRepoID string) (wsdomain.Repo, bool, error) {
	if found := wsdomain.FindRepos(repos, remoteKey, preferRepoID); len(found) > 0 {
		return found[0], false, nil
	}
	if cfg.ProjectsDir == "" {
		return wsdomain.Repo{}, false, fmt.Errorf("%s/%s is not registered in %s and projects_dir is not set in %s; set it to the directory where repositories should be cloned", ref.Owner, ref.Repo, e.managerDisplay(), e.ConfigPath)
	}
	path := filepath.Join(cfg.ProjectsDir, ref.Repo)
	unlock := lockClone(path)
	defer unlock()
	_, err := os.Stat(path)
	switch {
	case os.IsNotExist(err):
		fmt.Fprintf(e.Out, "cloning %s/%s into %s\n", ref.Owner, ref.Repo, path)
		if _, err := e.GH.Run(ctx, "repo", "clone", ref.Owner+"/"+ref.Repo, path); err != nil {
			return wsdomain.Repo{}, false, err
		}
		return wsdomain.Repo{Path: path, RemoteKey: remoteKey}, true, nil
	case err != nil:
		return wsdomain.Repo{}, false, err
	}
	if _, name := gitx.ParseRemote(gitx.RemoteURL(path, "origin")); !strings.EqualFold(name, ref.Owner+"/"+ref.Repo) {
		return wsdomain.Repo{}, false, fmt.Errorf("%s exists and is not a checkout of %s/%s; move it, or clone %s/%s yourself and add it to %s", path, ref.Owner, ref.Repo, ref.Owner, ref.Repo, e.managerDisplay())
	}
	return wsdomain.Repo{Path: path, RemoteKey: remoteKey}, false, nil
}
