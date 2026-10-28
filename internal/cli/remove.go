package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/domain/pr"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/workspaces"
	"github.com/wloophq/wloop/internal/daemon"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/workspace"
)

// Remove deletes the local workspace of a pull request, the one the call
// names or else the one the inventory links, and unlinks it when linked. It checks what the
// workspace manager's own removal does not: an agent at work, commits GitHub
// does not hold, and ignored files that may matter; the manager refuses
// uncommitted and untracked files itself. With force it checks nothing but
// the pull request's state and has the manager remove the workspace whatever
// it holds.
func (a envActions) Remove(ctx context.Context, u unit.Unit, c daemon.Call) error {
	ref := prOf(u)
	e := a.call(c)
	pr, err := e.basics(ctx, ref)
	if err != nil {
		return err
	}
	linked := ""
	if a.store != nil {
		stored, ok, err := a.store.PR(ctx, ref)
		if err != nil {
			return err
		}
		if ok {
			linked = stored.Workspace.ID
		}
	}
	w, err := e.finishedWorkspace(ctx, ref, pr, cmp.Or(c.WorkspaceID, linked))
	if err != nil {
		return err
	}
	info := workspaces.PRInfo{Unit: u, Number: ref.Number, Head: pr.HeadRefOid, Open: pr.State == dgh.StateOpen}
	err = e.workspaces(ctx, ref, c.Force).Remove(ctx, w, info, c.Force)
	var refused workspaces.Refusal
	if errors.As(err, &refused) {
		return refusalOf(refused, w)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(e.Out, "removed %s (%s)\n", w.DisplayName, w.Path)
	if linked == "" || linked != w.ID {
		return nil
	}
	return a.store.SetWorkspace(ctx, ref, dinv.Workspace{})
}

// finishedWorkspace is the workspace id names, or without one the only local
// workspace of the pull request: linked to it, else on its branch when the
// pull request comes from the same repository.
func (e *Env) finishedWorkspace(ctx context.Context, ref pr.Ref, pr *pr.PullRequest, id string) (wsdomain.Workspace, error) {
	repos, err := e.WS.Repos(ctx)
	if err != nil {
		return wsdomain.Workspace{}, err
	}
	list, err := e.WS.List(ctx)
	if err != nil {
		return wsdomain.Workspace{}, err
	}
	ofRepo := map[string]bool{}
	for _, r := range repos {
		if owner, name, ok := r.OwnerName(); ok && owner == ref.Owner && name == ref.Repo {
			ofRepo[r.ID] = true
		}
	}
	if id != "" {
		for _, w := range list {
			if w.ID != id {
				continue
			}
			switch {
			case w.IsMain:
				return w, daemon.Refusal{Reason: w.Path + " is the main checkout"}
			case w.HostID != wsdomain.LocalHost:
				return w, daemon.Refusal{Reason: w.DisplayName + " is not local"}
			case !ofRepo[w.RepoID]:
				return w, daemon.Refusal{Reason: w.DisplayName + " is not of " + ref.Owner + "/" + ref.Repo}
			}
			return w, nil
		}
		return wsdomain.Workspace{}, daemon.Refusal{Reason: "the workspace linked to " + ref.String() + " is gone"}
	}
	var linked, onBranch []wsdomain.Workspace
	for _, w := range list {
		if w.IsMain || w.HostID != wsdomain.LocalHost || !ofRepo[w.RepoID] {
			continue
		}
		switch {
		case w.LinkedPR == ref.Number:
			linked = append(linked, w)
		case w.LinkedPR == 0 && !pr.IsCrossRepository && pr.HeadRefName != "" && w.HeadBranch() == pr.HeadRefName:
			onBranch = append(onBranch, w)
		}
	}
	candidates := linked
	if len(candidates) == 0 {
		candidates = onBranch
	}
	switch len(candidates) {
	case 0:
		return wsdomain.Workspace{}, daemon.Refusal{Reason: "no local workspace for " + ref.String()}
	case 1:
		return candidates[0], nil
	}
	paths := make([]string, len(candidates))
	for i, w := range candidates {
		paths[i] = w.Path
	}
	return wsdomain.Workspace{}, daemon.Refusal{Reason: fmt.Sprintf("%d workspaces could be %s: %s; remove it from the cockpit's Done view", len(candidates), ref, strings.Join(paths, ", "))}
}

// workspaces is the removal use case of a removal by hand of ref's workspace.
// It reads no fetch log: Remove fetches itself.
func (e *Env) workspaces(ctx context.Context, ref pr.Ref, force bool) *workspaces.Service {
	remote, tree, files := checkouts(e.Git)
	if e.Files != nil {
		files = e.Files
	}
	return &workspaces.Service{
		Manager: removalManager{e.WS, e.managerDisplay(), force}, Remote: remote, Tree: byHandTree{tree}, Sessions: agentGuard{e}, Files: files,
		Rounds: envRounds{e},
		Disposable: func() []string {
			cfg, _ := e.loadConfig()
			return cfg.Cleanup.Disposable
		},
		Viewer: func() string {
			v, _ := e.viewerLogin(ctx)
			return v
		},
		Pub: removalLog{e, ref}, Clock: clockFunc(func() time.Time { return e.Now() }),
	}
}

// refusalOf is the daemon's refusal of a removal's check, which force gets
// past.
func refusalOf(r workspaces.Refusal, w wsdomain.Workspace) daemon.Refusal {
	out := daemon.Refusal{Reason: r.Reason, Forceable: true}
	if r.Backup != "" {
		out.Detail = "back them up with: " + r.Backup
	}
	if len(r.Paths) > 0 {
		shown := r.Paths[:min(len(r.Paths), 10)]
		out.Detail = strings.Join(shown, "\n")
		if more := len(r.Paths) - len(shown); more > 0 {
			out.Detail += fmt.Sprintf("\nand %d more", more)
		}
		out.Reason = fmt.Sprintf("%s that may matter (e.g. %s) in %s", plural(len(r.Paths), "ignored file", "ignored files"), r.Paths[0], w.Path)
	}
	return out
}

// removalManager reports the manager's failure to remove as a refusal with
// its own message.
type removalManager struct {
	*workspace.Set
	display string
	force   bool
}

func (m removalManager) Remove(ctx context.Context, id string, force bool) error {
	err := m.Set.Remove(ctx, id, force)
	if err == nil {
		return nil
	}
	source := ""
	if owner, oerr := m.Of(ctx, id); oerr == nil {
		source = owner.Name()
	}
	detail := err.Error()
	var detailed interface{ Detail() string }
	if errors.As(err, &detailed) {
		detail = detailed.Detail()
	}
	reason := m.display + " refused to remove it"
	if strings.Contains(detail, "PTY") {
		reason = "its terminals could not be stopped; " + m.display + " could not confirm they exited"
	}
	return daemon.Refusal{Reason: reason, Detail: detail, Source: source, Forceable: !m.force}
}

// byHandTree leaves modified and untracked files to the workspace manager,
// which refuses to remove a workspace holding them.
type byHandTree struct{ workspaces.Tree }

func (byHandTree) DirtyTracked(context.Context, string) (int, error) { return 0, nil }

func (t byHandTree) Ignored(ctx context.Context, dir string) ([]string, []string, error) {
	_, ignored, err := t.Tree.Ignored(ctx, dir)
	return nil, ignored, err
}

// agentGuard refuses a workspace whose agent works, waits for an answer or
// the user has open. Closing leaves the terminals to the manager's removal.
type agentGuard struct{ e *Env }

func (g agentGuard) Refusal(ctx context.Context, id string) (string, error) {
	g.e.WS.Invalidate(wsdomain.PartPanes)
	activity, err := g.e.WS.AgentActivity(ctx)
	if err != nil {
		return "", err
	}
	switch a := activity[id]; {
	case a.Busy:
		return "its agent is working", nil
	case a.Waiting:
		return "its agent is waiting for an answer", nil
	case a.UserActive:
		return "you have it open", nil
	}
	return "", nil
}

func (agentGuard) CloseAll(context.Context, string, string) error { return nil }

type envRounds struct{ e *Env }

func (r envRounds) SkipFirstRound(ctx context.Context, u unit.Unit) error {
	return r.e.withState(ctx, func(s *sweep) error { s.st.SetMark(u.Key, markFirstSkipped, "1"); return nil })
}

// removalLog records a removal's event on the pull request in the store,
// when there is one.
type removalLog struct {
	e   *Env
	ref pr.Ref
}

func (l removalLog) Publish(evs ...port.Event) {
	if l.e.Store == nil {
		return
	}
	for _, ev := range evs {
		if err := l.e.Store.RecordEvent(context.Background(), dinv.Event{PR: l.ref, Kind: ev.Kind, Key: ev.Key, At: ev.At, Data: ev.Data}); err != nil {
			fmt.Fprintf(l.e.Err, "recording %s: %v\n", ev.Kind, err)
		}
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
