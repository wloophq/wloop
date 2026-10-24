package workspaces

import (
	"context"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/domain/workspace"
)

// Loose lists the untracked and ignored paths of w that removing it would
// lose: a nested repository holding anything its remotes lack, and any
// other path outside the disposable list, not a symbolic link and not
// identical to the main checkout's copy.
func (s *Service) Loose(ctx context.Context, w workspace.Workspace) ([]string, error) {
	main, err := s.mainCheckout(ctx, w)
	if err != nil {
		return nil, err
	}
	return s.lost(ctx, w.Path, main, s.Disposable())
}

// lost is Loose for the repository at dir; main, when set, is the checkout
// whose identical copies are disposable. An ignored directory is one entry:
// the repositories inside it are checked one by one, the rest compared as a
// whole.
func (s *Service) lost(ctx context.Context, dir, main string, list []string) ([]string, error) {
	untracked, ignored, err := s.Tree.Ignored(ctx, dir)
	if err != nil {
		return nil, err
	}
	repos := map[string]bool{}
	reproducible := map[string]bool{}
	held := func(root string) (bool, error) {
		ok, seen := reproducible[root]
		if !seen {
			if ok, err = s.reproducible(ctx, filepath.Join(dir, filepath.FromSlash(root)), list); err != nil {
				return false, err
			}
			reproducible[root] = ok
		}
		return ok, nil
	}
	var lost []string
	for _, p := range slices.Concat(untracked, ignored) {
		rel := strings.TrimSuffix(path.Clean(p), "/")
		at := filepath.Join(dir, filepath.FromSlash(rel))
		if s.Files.Link(at) {
			continue
		}
		if root := s.nestedRepo(dir, rel, repos); root != "" {
			ok, err := held(root)
			if err != nil {
				return nil, err
			}
			if !ok {
				lost = append(lost, p)
			}
			continue
		}
		if strings.HasSuffix(p, "/") {
			inner, err := s.Files.Repos(at)
			if err != nil {
				return nil, err
			}
			for _, r := range inner {
				root := rel + "/" + r
				ok, err := held(root)
				if err != nil {
					return nil, err
				}
				if !ok {
					lost = append(lost, root+"/")
				}
			}
		}
		if Disposable(list, rel) {
			continue
		}
		if main != "" {
			same, err := s.Files.Same(at, filepath.Join(main, filepath.FromSlash(rel)))
			if err != nil {
				return nil, err
			}
			if same {
				continue
			}
		}
		lost = append(lost, p)
	}
	return lost, nil
}

// Disposable tells whether rel, a clean slash path, names an entry of list in
// any of its parts or is compiled Python.
func Disposable(list []string, rel string) bool {
	if strings.HasSuffix(rel, ".pyc") {
		return true
	}
	return slices.ContainsFunc(strings.Split(rel, "/"), func(part string) bool { return slices.Contains(list, part) })
}

// mainCheckout is the path of w's main checkout, "" when unknown or when w
// is it.
func (s *Service) mainCheckout(ctx context.Context, w workspace.Workspace) (string, error) {
	repos, err := s.Manager.Repos(ctx)
	if err != nil {
		return "", err
	}
	for _, r := range repos {
		if r.ID == w.RepoID && r.Path != "" && filepath.Clean(r.Path) != filepath.Clean(w.Path) {
			return r.Path, nil
		}
	}
	return "", nil
}

// nestedRepo is the directory of rel, relative to the checkout at dir, that
// is a git repository of its own, "" when none is; repos caches the answers
// by directory.
func (s *Service) nestedRepo(dir, rel string, repos map[string]bool) string {
	parts := strings.Split(rel, "/")
	for i := 1; i <= len(parts); i++ {
		root := strings.Join(parts[:i], "/")
		is, seen := repos[root]
		if !seen {
			is = s.Files.Exists(filepath.Join(dir, filepath.FromSlash(root), ".git"))
			repos[root] = is
		}
		if is {
			return root
		}
	}
	return ""
}

// reproducible tells whether the repository at dir holds nothing its
// remotes lack: no change, no commit or stash they do not have, and no
// untracked or ignored path outside the disposable list.
func (s *Service) reproducible(ctx context.Context, dir string, list []string) (bool, error) {
	changed, err := s.Tree.Changed(ctx, dir)
	if err != nil || changed > 0 {
		return false, err
	}
	kept, err := s.Tree.Kept(ctx, dir)
	if err != nil || kept != "" {
		return false, err
	}
	lost, err := s.lost(ctx, dir, "", list)
	return len(lost) == 0, err
}
