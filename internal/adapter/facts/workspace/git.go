package factworkspace

import (
	"context"
	"strconv"
	"strings"

	"github.com/wloophq/wloop/internal/gitx"
)

// LocalGit is Git over git commands that never refresh the index, so an
// agent's or the user's git command in the checkout never meets our lock.
type LocalGit struct{ R gitx.Runner }

var _ Git = LocalGit{}

func (g LocalGit) run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return g.R.Run(ctx, dir, nil, append([]string{"--no-optional-locks"}, args...)...)
}

// Head reads the repository files first, as gitx does, and asks git only
// when they do not answer.
func (g LocalGit) Head(ctx context.Context, dir string) (string, error) {
	if oid, _, ok := gitx.ReadHead(dir); ok {
		return oid, nil
	}
	out, err := g.run(ctx, dir, "rev-parse", "HEAD")
	return strings.TrimSpace(string(out)), err
}

// Status leaves ignored files out: listing them walks every ignored
// directory.
func (g LocalGit) Status(ctx context.Context, dir string) (dirty int, untracked []string, err error) {
	out, err := g.run(ctx, dir, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return 0, nil, err
	}
	entries := strings.Split(string(out), "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 4 {
			continue
		}
		if e[:2] == "??" {
			untracked = append(untracked, e[3:])
			continue
		}
		dirty++
		if e[0] == 'R' || e[0] == 'C' {
			i++
		}
	}
	return dirty, untracked, nil
}

func (g LocalGit) Unpushed(ctx context.Context, dir string) (int, error) {
	out, err := g.run(ctx, dir, "rev-list", "--count", "HEAD", "--not", "--remotes")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}
