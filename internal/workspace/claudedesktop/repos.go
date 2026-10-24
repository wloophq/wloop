package claudedesktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/gitx"
)

const reposFile = ".repos.json"

type reposState struct {
	Repos []string `json:"repos"`
}

func (m *Manager) reposPath() string { return filepath.Join(m.Root, reposFile) }

func (m *Manager) readRepos() ([]string, error) {
	b, err := os.ReadFile(m.reposPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s reposState
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("reading %s: %w", m.reposPath(), err)
	}
	return s.Repos, nil
}

func (m *Manager) Repos(ctx context.Context) ([]wsdomain.Repo, error) {
	paths, err := m.readRepos()
	if err != nil {
		return nil, err
	}
	out := make([]wsdomain.Repo, 0, len(paths))
	for _, p := range paths {
		out = append(out, repoAt(p))
	}
	return out, nil
}

func repoAt(path string) wsdomain.Repo {
	r := wsdomain.Repo{ID: path, Path: path, DefaultBranch: gitx.DefaultBranch(path)}
	if host, name := gitx.ParseRemote(gitx.RemoteURL(path, "origin")); host != "" && name != "" {
		r.RemoteKey = host + "/" + name
	}
	return r
}

// AddRepo registers the main checkout at path; registering it again is a no-op.
func (m *Manager) AddRepo(ctx context.Context, path string) (wsdomain.Repo, error) {
	path, err := canonical(path)
	if err != nil {
		return wsdomain.Repo{}, err
	}
	if fi, err := os.Stat(filepath.Join(path, ".git")); err != nil || !fi.IsDir() {
		return wsdomain.Repo{}, fmt.Errorf("%s is not the main checkout of a git repository", path)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	paths, err := m.readRepos()
	if err != nil {
		return wsdomain.Repo{}, err
	}
	if !slices.Contains(paths, path) {
		if err := m.writeRepos(append(paths, path)); err != nil {
			return wsdomain.Repo{}, err
		}
	}
	return repoAt(path), nil
}

func (m *Manager) writeRepos(paths []string) error {
	if err := os.MkdirAll(m.Root, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(reposState{Repos: paths}, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.reposPath() + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.reposPath())
}

// canonical is path absolute with its symlinks resolved, as git and Claude
// Code record a working directory.
func canonical(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real, nil
	}
	return abs, nil
}
