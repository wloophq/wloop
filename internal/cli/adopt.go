package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/wloophq/wloop/internal/domain/pr"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/round"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/workspace"
)

// runTakeOver makes a PR someone else opened yours to develop: its review
// workspace, or a new one, becomes an author workspace.
func runTakeOver(ctx context.Context, e *Env, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: " + brand.Command + " take-over <https://github.com/<owner>/<repo>/pull/<n>>")
	}
	ref, err := github.ParseURL(args[0])
	if err != nil {
		return err
	}
	return e.action(ctx, ref, "take_over", api.ActionRequest{}, "done by the daemon")
}

// markAdopted records the adoption in the inventory, as the take-over action
// does, so the pull request shows in "mine".
func (e *Env) markAdopted(ctx context.Context, ref github.Ref) error {
	store, err := e.store()
	if err != nil {
		return err
	}
	if err := store.SetAdopted(ctx, ref); err != nil {
		return err
	}
	return store.RecordEvent(ctx, dinv.Event{PR: ref, Kind: dinv.KindAdopted, At: e.Now()})
}

// adoptPR uses profile as the agent home when no project sets one,
// and returns the author workspace it settled on.
func adoptPR(ctx context.Context, e *Env, cfg config.Config, ref github.Ref, profile string) (wsdomain.Workspace, error) {
	cfg = prConfig(cfg, ref, profile)
	pr, err := e.basics(ctx, ref)
	if err != nil {
		return wsdomain.Workspace{}, err
	}
	if pr.IsCrossRepository {
		fmt.Fprintf(e.Err, "warning: %s comes from a fork; pushing to it needs the author's permission\n", ref)
	}
	repos, err := e.WS.Repos(ctx)
	if err != nil {
		return wsdomain.Workspace{}, err
	}
	all, err := e.WS.List(ctx)
	if err != nil {
		return wsdomain.Workspace{}, err
	}
	ws, action, err := e.authorWorkspace(ctx, cfg, ref, pr, e.prOf(ctx, pr), repos, all)
	if err != nil {
		return wsdomain.Workspace{}, err
	}
	fmt.Fprintf(e.Out, "%s: %s\n%s\n", action, pr.Title, ws.Path)
	return ws, e.clearFingerprint(ctx, ref)
}

// clearFingerprint forgets the reviews the address rounds on ref covered, so
// a pull request taken over starts its triage from none of them.
func (e *Env) clearFingerprint(ctx context.Context, ref github.Ref) error {
	if e.Rounds == nil {
		return nil
	}
	st, err := e.Rounds.Load(ctx)
	if err != nil || st.Mark(ref.String(), round.AddressedReviews) == "" {
		return err
	}
	st.SetMark(ref.String(), round.AddressedReviews, "")
	return e.Rounds.Save(ctx, st)
}

// authorWorkspace finds the PR's author workspace, else turns its review
// workspace into one, else creates one at the PR head with an idle agent. It
// links the workspace to the PR and moves it to in-progress. action says
// which of the three happened.
func (e *Env) authorWorkspace(ctx context.Context, cfg config.Config, ref github.Ref, pr *pr.PullRequest, mine func() (bool, error), repos []wsdomain.Repo, all []wsdomain.Workspace) (ws wsdomain.Workspace, action string, err error) {
	remoteKey := ref.RemoteKey()
	ws, found, err := findAuthorWorkspace(cfg, repos, all, ref, pr, mine)
	if err != nil {
		return ws, "", err
	}
	action = "already an author workspace: " + ws.DisplayName
	if !found {
		if ws, found = findReviewWorkspace(cfg, repos, all, remoteKey, ref.Number, pr.HeadRefName); found {
			if err := e.rename(ctx, ws, pr.HeadRefName); err != nil {
				return ws, "", err
			}
			action = "renamed " + ws.DisplayName + " to " + pr.HeadRefName
			ws.DisplayName = pr.HeadRefName
		}
	}
	if !found {
		if w, _, held := branchHolder(repos, all, ref, pr); held {
			return ws, "", fmt.Errorf("%s; free the branch for a new workspace", checkedOutIn(pr.HeadRefName, w.Path))
		}
		repo, err := e.resolveCheckout(ctx, cfg, repos, remoteKey, ref, "")
		if err != nil {
			return ws, "", err
		}
		if err := e.Git.FetchPR(ctx, repo.Path, ref.Number); err != nil {
			return ws, "", err
		}
		if why, err := e.localBranchProblem(ctx, repo.Path, pr, true); err != nil || why != "" {
			return ws, "", cmp.Or(err, errors.New(why))
		}
		ws, _, err = e.createWithAgent(ctx, cfg, ref, repo, workspace.CreateRequest{
			Name:    pr.HeadRefName,
			BaseRef: "origin/pr/" + fmt.Sprint(ref.Number),
			Unit:    ref.Unit(),

			HeadBranch:      pr.HeadRefName,
			HeadOid:         pr.HeadRefOid,
			BaseBranchName:  pr.BaseRefName,
			CrossRepository: pr.IsCrossRepository,
		}, sentTurn{}, roundOptions{allowUserActive: true, trusted: trusts(cfg, pr)})
		if err != nil {
			return ws, "", err
		}
		action = "created " + ws.DisplayName
	}

	e.alignWithPR(ctx, ws, pr)
	if err := e.roundStarted(ctx, ref.Unit(), func() error { return e.setColumn(ctx, cfg, ws, config.ColumnInProgress) }); err != nil {
		fmt.Fprintf(e.Err, "warning: setting the board status: %v\n", err)
	}
	return ws, action, nil
}

// findAuthorWorkspace finds a workspace outside the review prefix that is
// linked to the PR or sits on its head branch. A main checkout counts, whatever
// its name, only for a PR of mine from the same repository whose head branch
// it has checked out, other than the default branch; a worktree wins over it.
func findAuthorWorkspace(cfg config.Config, repos []wsdomain.Repo, all []wsdomain.Workspace, ref github.Ref, pr *pr.PullRequest, mine func() (bool, error)) (wsdomain.Workspace, bool, error) {
	ofRepo := reposOf(repos, ref)
	var main wsdomain.Workspace
	for _, w := range all {
		r, ok := ofRepo[w.RepoID]
		if !ok || w.HostID != wsdomain.LocalHost || wsdomain.MainOnDefault(w, r) || (!w.IsMain && strings.HasPrefix(w.DisplayName, cfg.GitHub.ReviewPrefix)) {
			continue
		}
		onHead := pr.HeadRefName != "" && w.PRBranch() == pr.HeadRefName
		switch {
		case w.IsMain:
			if onHead && !pr.IsCrossRepository {
				main = w
			}
		case w.LinkedPR == ref.Number || onHead:
			return w, true, nil
		}
	}
	if main.ID == "" {
		return main, false, nil
	}
	ok, err := mine()
	if !ok || err != nil {
		return wsdomain.Workspace{}, false, err
	}
	return main, true, nil
}

// prOf answers whether the PR is the viewer's, asking GitHub once and only
// when called.
func (e *Env) prOf(ctx context.Context, pr *pr.PullRequest) func() (bool, error) {
	var asked, mine bool
	var err error
	return func() (bool, error) {
		if !asked {
			asked = true
			var viewer string
			viewer = aheadViewer(ctx)
			if viewer == "" {
				viewer, err = e.storedViewer(ctx)
			}
			if err != nil || viewer == "" {
				viewer, err = e.GitHub.Viewer(ctx)
			}
			mine = err == nil && strings.EqualFold(pr.Author, viewer)
		}
		return mine, err
	}
}

// branchHolder is the local checkout of the repository that has the head
// branch of a PR from the same repository checked out, with its repository:
// git keeps a branch in one checkout, so the workspace manager would put a new
// workspace for the PR on a renamed branch.
func branchHolder(repos []wsdomain.Repo, all []wsdomain.Workspace, ref github.Ref, pr *pr.PullRequest) (wsdomain.Workspace, wsdomain.Repo, bool) {
	if pr.IsCrossRepository || pr.HeadRefName == "" {
		return wsdomain.Workspace{}, wsdomain.Repo{}, false
	}
	ofRepo := reposOf(repos, ref)
	for _, w := range all {
		if r, ok := ofRepo[w.RepoID]; ok && w.HostID == wsdomain.LocalHost && w.Branch == pr.HeadRefName {
			return w, r, true
		}
	}
	return wsdomain.Workspace{}, wsdomain.Repo{}, false
}

func checkedOutIn(branch, path string) string { return branch + " is checked out in " + path }

// localBranchProblem says why the workspace manager would not reuse the PR's local head branch
// in dir: it reuses one only when no worktree has it checked out and it sits
// at the PR head. Once the PR head is fetched, a branch behind it is moved up
// to it, which loses nothing; before, a head the repository lacks is left for
// that later check.
func (e *Env) localBranchProblem(ctx context.Context, dir string, pr *pr.PullRequest, fetched bool) (string, error) {
	branch, head := pr.HeadRefName, pr.HeadRefOid
	if pr.IsCrossRepository || branch == "" || head == "" {
		return "", nil
	}
	tip, err := e.Git.LocalBranchTip(ctx, dir, branch)
	if err != nil || tip == "" {
		return "", err
	}
	if path, err := e.Git.CheckedOutAt(ctx, dir, branch); err != nil || path != "" {
		return checkedOutIn(branch, path), err
	}
	if tip == head {
		return "", nil
	}
	if !fetched {
		if known, err := e.Git.HasCommit(ctx, dir, head); err != nil || !known {
			return "", err
		}
	}
	behind, err := e.Git.IsAncestor(ctx, dir, tip, head)
	switch {
	case err != nil:
		return "", err
	case !behind:
		return fmt.Sprintf("local branch %s has commits not on the PR: push them or `git branch -D %s`", branch, branch), nil
	case fetched:
		return "", e.Git.UpdateRef(ctx, dir, "refs/heads/"+branch, head, tip)
	}
	return "", nil
}

func reposOf(repos []wsdomain.Repo, ref github.Ref) map[string]wsdomain.Repo {
	remoteKey := ref.RemoteKey()
	byID := map[string]wsdomain.Repo{}
	for _, r := range repos {
		if strings.EqualFold(r.RemoteKey, remoteKey) {
			byID[r.ID] = r
		}
	}
	return byID
}

// rename gives ws the display name on a manager whose workspaces carry one.
func (e *Env) rename(ctx context.Context, ws wsdomain.Workspace, name string) error {
	m, err := e.WS.For(ctx, ws)
	if err != nil {
		return err
	}
	if r, ok := workspace.Capability[workspace.Renamer](m); ok {
		return r.Rename(ctx, ws.ID, name)
	}
	return nil
}
