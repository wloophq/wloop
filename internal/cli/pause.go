package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/domain/pr"
)

// rounds is the daemon's round state; a command line process has none and
// fails here instead of opening the store.
func (e *Env) rounds() (Rounds, error) {
	if e.Rounds == nil {
		return nil, errDaemonDown
	}
	return e.Rounds, nil
}

// runPause pauses or resumes the pull requests and repositories args
// name, or everything when they name none.
func runPause(ctx context.Context, e *Env, paused bool, args []string) error {
	verb := map[bool]string{true: "pause", false: "resume"}[paused]
	usage := "usage: " + brand.Command + " " + verb + " [<pr-url>|<owner/repo>]..."
	var req api.PauseRequest
	req.Paused = paused
	for _, a := range args {
		switch {
		case isRepoName(a):
			req.Repos = append(req.Repos, a)
		case strings.Contains(a, "://"):
			ref, err := pr.ParseURL(a)
			if err != nil {
				return err
			}
			req.PRs = append(req.PRs, ref.String())
		default:
			return fmt.Errorf("%s is neither a pull request URL nor an owner/repo\n%s", a, usage)
		}
	}
	c, err := e.daemonClient(ctx)
	if err != nil {
		return err
	}
	if err := c.SetPause(ctx, req); err != nil {
		return err
	}
	what := "every workflow that does not observe and board moves"
	if named := append(slices.Clone(req.Repos), req.PRs...); len(named) > 0 {
		what += " on " + strings.Join(named, ", ")
	}
	if paused {
		fmt.Fprintf(e.Out, "%s paused; '%s' restarts them\n", what, strings.Join(append([]string{brand.Command, "resume"}, args...), " "))
	} else {
		fmt.Fprintln(e.Out, what+" resumed")
	}
	return nil
}

func isRepoName(s string) bool {
	owner, repo, ok := strings.Cut(s, "/")
	return ok && owner != "" && repo != "" && !strings.ContainsAny(repo, "/# ")
}
