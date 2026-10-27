package cli

import (
	"context"
	"fmt"

	"github.com/wloophq/wloop/internal/config"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/turn"
)

// nudge sends r in a new agent session, so it never types into a
// conversation the user or an earlier round is having, and returns the id
// of the turn sent; resumeOf is the turn it resumes, 0 for none.
func (e *Env) nudge(ctx context.Context, cfg config.Config, r turn.Request, pr *github.PullRequest, resumeOf int64) (string, error) {
	ref := prOf(r.Unit)
	sent, err := e.sendTurn(ctx, cfg, r)
	if err != nil {
		return "", err
	}
	sent.resume(resumeOf)
	repos, err := e.WS.Repos(ctx)
	if err != nil {
		return "", err
	}
	all, err := e.WS.List(ctx)
	if err != nil {
		return "", err
	}
	ws, where, err := nudgeWorkspace(cfg, e.managerDisplay(), repos, all, ref, pr, e.prOf(ctx, pr))
	if err != nil {
		return "", err
	}
	d, _ := e.loadedIntents().Registry.Get(dintent.Name(r.Intent))
	opts := intentOptions(d, roundOptions{allowUserActive: r.Trigger == turn.ByHand, trusted: trusts(cfg, pr), pr: pr})
	opts.unit = r.Unit
	s, err := e.agentSession(ctx, cfg, ws, opts)
	if err != nil {
		return "", err
	}
	name, err := e.roundName(opts, r.Unit)
	if err != nil {
		return "", err
	}
	p, err := e.startSession(ctx, cfg, ws, r.Unit, s, name, opts.keepBlocked, opts.trusted)
	if err != nil {
		return "", fmt.Errorf("%s: starting the agent: %w", ws.DisplayName, err)
	}
	submitted, err := e.deliver(ctx, ref.Unit(), &sent, p, false)
	e.recordGuidance(ctx, ref, r.Guidance)
	if err != nil {
		return "", fmt.Errorf("%s: sending the prompt: %w", ws.DisplayName, err)
	}
	note := ""
	if !submitted {
		note = " (submission not observed; check the session)"
	}
	fmt.Fprintf(e.Out, "%s in %s, the %s%s: %s\n", r.Intent, ws.DisplayName, where, note, pr.Title)
	return sent.ID, nil
}

// nudgeWorkspace prefers the pull request's own workspace, where the agent
// has its context and review rounds already run, and falls back to the
// repository's main checkout on its default branch, since a nudge needs only
// gh.
func nudgeWorkspace(cfg config.Config, manager string, repos []wsdomain.Repo, all []wsdomain.Workspace, ref github.Ref, pr *github.PullRequest, mine func() (bool, error)) (wsdomain.Workspace, string, error) {
	remoteKey := ref.RemoteKey()
	if w, ok := findReviewWorkspace(cfg, repos, all, remoteKey, ref.Number, pr.HeadRefName); ok {
		return w, "review workspace", nil
	}
	w, ok, err := findAuthorWorkspace(cfg, repos, all, ref, pr, mine)
	if err != nil {
		return wsdomain.Workspace{}, "", err
	}
	if ok {
		return w, "author workspace", nil
	}
	for _, r := range wsdomain.FindRepos(repos, remoteKey, "") {
		for _, w := range all {
			if w.RepoID == r.ID && w.HostID == wsdomain.LocalHost && wsdomain.MainOnDefault(w, r) {
				return w, "main checkout", nil
			}
		}
	}
	return wsdomain.Workspace{}, "", fmt.Errorf("no workspace of the pull request and no %[1]s checkout of %[2]s/%[3]s on its default branch to run the nudge from; add the repository to %[1]s", manager, ref.Owner, ref.Repo)
}
