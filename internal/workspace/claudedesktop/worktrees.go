package claudedesktop

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/workspace"
)

// root is Root as Claude Code records a working directory under it.
func (m *Manager) root() string {
	r, err := canonical(m.Root)
	if err != nil {
		return filepath.Clean(m.Root)
	}
	return r
}

func (m *Manager) List(context.Context) ([]wsdomain.Workspace, error) {
	return m.worktrees()
}

// Workspaces lists the worktrees with a scope that is complete whether the app
// runs or not, since they are read from disk. While the app runs, the scope
// names its runtime, as the terminals' does.
func (m *Manager) Workspaces(ctx context.Context) ([]wsdomain.Workspace, wsdomain.Scope, error) {
	out, err := m.worktrees()
	if err != nil {
		return nil, wsdomain.Scope{}, err
	}
	scope := wsdomain.Scope{Hosts: []string{wsdomain.LocalHost}, Complete: true}
	if id, err := m.runtime(ctx); err == nil {
		scope.RuntimeID = id
	} else if ctx.Err() != nil {
		return nil, wsdomain.Scope{}, ctx.Err()
	}
	return out, scope, nil
}

// worktrees are the git worktrees at <Root>/<repo name>/<name>, then those
// the app created for its sessions, read from their files so a sweep spawns no
// git process.
func (m *Manager) worktrees() ([]wsdomain.Workspace, error) {
	out, err := m.rootWorktrees()
	if err != nil {
		return nil, err
	}
	made, err := m.appWorktrees()
	if err != nil {
		return nil, err
	}
	for _, w := range made {
		if !slices.ContainsFunc(out, func(o wsdomain.Workspace) bool { return o.Path == w.Path }) {
			out = append(out, w)
		}
	}
	return out, nil
}

// appWorktrees are the worktrees the app made for a session of its store, in
// the store's order, while they are still on disk.
func (m *Manager) appWorktrees() ([]wsdomain.Workspace, error) {
	files, err := m.storeFiles(hostPrefix + "*.json")
	if err != nil {
		return nil, err
	}
	var out []wsdomain.Workspace
	for _, f := range files {
		s, err := readStored(f)
		if err != nil || s.WorktreePath == "" {
			continue
		}
		path := filepath.Clean(s.WorktreePath)
		if slices.ContainsFunc(out, func(o wsdomain.Workspace) bool { return o.Path == path }) {
			continue
		}
		if ws, ok := worktreeAt(path); ok {
			out = append(out, ws)
		}
	}
	return out, nil
}

func (m *Manager) rootWorktrees() ([]wsdomain.Workspace, error) {
	root := m.root()
	repos, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", root, err)
	}
	var out []wsdomain.Workspace
	for _, r := range repos {
		if !r.IsDir() || strings.HasPrefix(r.Name(), ".") {
			continue
		}
		names, err := os.ReadDir(filepath.Join(root, r.Name()))
		if err != nil {
			return nil, fmt.Errorf("listing %s: %w", filepath.Join(root, r.Name()), err)
		}
		for _, n := range names {
			if ws, ok := worktreeAt(filepath.Join(root, r.Name(), n.Name())); ok && n.IsDir() {
				out = append(out, ws)
			}
		}
	}
	return out, nil
}

// worktreeAt is the linked worktree checked out at path; a main checkout or
// anything else is not one.
func worktreeAt(path string) (wsdomain.Workspace, bool) {
	gitDir, ok := linkedGitDir(path)
	if !ok {
		return wsdomain.Workspace{}, false
	}
	common := commonDir(gitDir)
	repo, err := canonical(filepath.Dir(common))
	if err != nil {
		return wsdomain.Workspace{}, false
	}
	name := filepath.Base(path)
	if b, err := os.ReadFile(filepath.Join(gitDir, displayNameFile)); err == nil && strings.TrimSpace(string(b)) != "" {
		name = strings.TrimSpace(string(b))
	}
	return wsdomain.Workspace{ID: path, RepoID: repo, Path: path, DisplayName: name,
		Branch: headBranch(gitDir), HostID: wsdomain.LocalHost, Created: true}, true
}

// displayNameFile keeps, in a worktree's git directory, the name the loop gave
// it: the directory is a slug, and the review prefix and a branch's slashes
// must survive for the loop to find the workspace by name.
const displayNameFile = "loop-display-name"

// linkedGitDir is the git directory a linked worktree's .git file points at.
func linkedGitDir(path string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(path, ".git"))
	if err != nil {
		return "", false
	}
	dir, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
	if !ok {
		return "", false
	}
	dir = strings.TrimSpace(dir)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(path, dir)
	}
	return dir, true
}

func commonDir(gitDir string) string {
	b, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return gitDir
	}
	common := strings.TrimSpace(string(b))
	if !filepath.IsAbs(common) {
		common = filepath.Join(gitDir, common)
	}
	return filepath.Clean(common)
}

func headBranch(gitDir string) string {
	b, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	branch, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "ref: refs/heads/")
	if !ok {
		return ""
	}
	return branch
}

// Create adds a worktree of the repository at <Root>/<repo name>/<name>, on
// HeadBranch when it is set, else on a branch named after the workspace. A
// branch that exists is checked out; a new one starts at BaseRef, or HEAD.
func (m *Manager) Create(ctx context.Context, req workspace.CreateRequest) (wsdomain.Workspace, string, error) {
	repo, err := canonical(req.RepoID)
	if err != nil {
		return wsdomain.Workspace{}, "", err
	}
	name := slug(req.Name)
	if name == "" {
		return wsdomain.Workspace{}, "", errors.New("creating a workspace needs a name")
	}
	dir := filepath.Join(m.root(), filepath.Base(repo))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return wsdomain.Workspace{}, "", err
	}
	path := filepath.Join(dir, name)
	branch := req.HeadBranch
	if branch == "" {
		branch = name
	}
	renamed := false
	args := []string{"worktree", "add"}
	if tip, exists := m.branchTip(ctx, repo, branch); exists && (req.CrossRepository || req.HeadOid != "" && tip != req.HeadOid) {
		branch, renamed = m.freeBranch(ctx, repo, branch), true
		args = append(args, "--no-track", "-b", branch, path, cmp.Or(req.BaseRef, req.HeadOid, "HEAD"))
	} else if exists {
		args = append(args, path, branch)
	} else {
		args = append(args, "--no-track", "-b", branch, path, cmp.Or(req.BaseRef, "HEAD"))
	}
	if _, err := m.git(ctx, repo, args...); err != nil {
		return wsdomain.Workspace{}, "", fmt.Errorf("creating the workspace %s: %w", name, err)
	}
	if gitDir, ok := linkedGitDir(path); ok {
		if err := os.WriteFile(filepath.Join(gitDir, displayNameFile), []byte(strings.TrimSpace(req.Name)+"\n"), 0o644); err != nil {
			return wsdomain.Workspace{}, "", err
		}
	}
	ws, ok := worktreeAt(path)
	if !ok {
		return wsdomain.Workspace{}, "", fmt.Errorf("git created no worktree at %s", path)
	}
	ws.BranchRenamed = renamed
	return ws, "", nil
}

// branchTip is the commit of the local branch, false when there is none.
func (m *Manager) branchTip(ctx context.Context, repo, branch string) (string, bool) {
	out, err := m.git(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// freeBranch is branch with the first "-N" suffix no local branch has, as
// Orca names a pull request's branch whose name is taken.
func (m *Manager) freeBranch(ctx context.Context, repo, branch string) string {
	for n := 2; ; n++ {
		b := fmt.Sprintf("%s-%d", branch, n)
		if _, exists := m.branchTip(ctx, repo, b); !exists {
			return b
		}
	}
}

// slug is name as one path element.
func slug(name string) string {
	name = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ' ', ':', '\t', '\n':
			return '-'
		}
		return r
	}, strings.TrimSpace(name))
	return strings.Trim(name, ".-")
}

// Remove deletes the worktree and its checkout.
func (m *Manager) Remove(ctx context.Context, id string, force bool) error {
	path, err := m.owned(id)
	if err != nil {
		return err
	}
	gitDir, ok := linkedGitDir(path)
	if !ok {
		return fmt.Errorf("%s is not a worktree", path)
	}
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	if _, err := m.git(ctx, filepath.Dir(commonDir(gitDir)), append(args, path)...); err != nil {
		return fmt.Errorf("removing the workspace %s: %w", filepath.Base(path), err)
	}
	return nil
}

// Unregister drops a worktree whose checkout is gone from the repositories it
// may belong to: the registered ones named as its parent directory.
func (m *Manager) Unregister(ctx context.Context, id string) error {
	path, err := m.owned(id)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("the checkout of %s is still there; remove it instead", path)
	}
	repos, err := m.readRepos()
	if err != nil {
		return err
	}
	for _, r := range repos {
		if filepath.Base(r) != filepath.Base(filepath.Dir(path)) {
			continue
		}
		if _, err := m.git(ctx, r, "worktree", "prune"); err != nil {
			return fmt.Errorf("unregistering %s: %w", path, err)
		}
	}
	return nil
}

// owned is the workspace id as a path, checked to be a worktree place under
// Root or a worktree the app made.
func (m *Manager) owned(id string) (string, error) {
	path := filepath.Clean(id)
	rel, err := filepath.Rel(m.root(), path)
	if err == nil && len(strings.Split(rel, string(filepath.Separator))) == 2 && !strings.HasPrefix(rel, "..") {
		return path, nil
	}
	made, err := m.appWorktrees()
	if err != nil {
		return "", err
	}
	if !slices.ContainsFunc(made, func(w wsdomain.Workspace) bool { return w.Path == path }) {
		return "", fmt.Errorf("%s is not a workspace under %s nor one the app made", id, m.root())
	}
	return path, nil
}
