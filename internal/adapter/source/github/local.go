package ghsource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"

	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// CommandClone is the command plugin.toml declares that clones a
// repository: clone <owner>/<name> <path>.
const CommandClone = "clone"

// CommandAuth checks that gh is installed and logged in; it exits ExitNoGH
// when gh is not installed.
const CommandAuth = "auth"

// CommandReviewedRepos lists the repositories of the pull requests the
// viewer reviewed since a date: reviewed-repos <yyyy-mm-dd>, one
// owner/name a line, once per pull request.
const CommandReviewedRepos = "reviewed-repos"

// CommandTeams lists the viewer's teams, one org/slug a line.
const CommandTeams = "teams"

// ExitNoGH is the exit of a command that found no gh to run, the shell's
// "command not found".
const ExitNoGH = 127

// ActionBasics is the action that reads a pull request's basics: its
// answer is the pr.PullRequest as JSON.
const ActionBasics = "pr.basics"

type ghRunner interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

// Local is the GitHub plugin as the host calls it while the plugin runs in
// the host's process: its commands, run with gh, and the viewer as GitHub
// reads it.
type Local struct {
	GH  ghRunner
	API localAPI
}

type localAPI interface {
	viewerReader
	Basics(ctx context.Context, ref pr.Ref) (*pr.PullRequest, error)
}

func (l Local) Identity(ctx context.Context) (src.Identity, error) {
	login, err := l.API.Viewer(ctx)
	if err != nil {
		return src.Identity{}, err
	}
	p := person(login)
	return src.Identity{Viewer: src.Person{Key: p.Key, Name: p.Name}}, nil
}

// RunCLI runs one of the commands plugin.toml declares.
func (l Local) RunCLI(ctx context.Context, req src.CLIRequest, stdout, stderr io.Writer) (int, error) {
	args, ok := l.ghArgs(req.Args)
	if !ok {
		fmt.Fprintf(stderr, "unknown command %q\n", req.Args)
		return 2, nil
	}
	out, err := l.GH.Run(ctx, args...)
	switch {
	case errors.Is(err, exec.ErrNotFound):
		fmt.Fprintln(stderr, err)
		return ExitNoGH, nil
	case err != nil:
		fmt.Fprintln(stderr, err)
		return 1, nil
	}
	if req.Args[0] != CommandClone && req.Args[0] != CommandAuth {
		_, _ = stdout.Write(out)
	}
	return 0, nil
}

// ghArgs is the gh command a plugin command runs.
func (Local) ghArgs(args []string) ([]string, bool) {
	switch {
	case len(args) == 3 && args[0] == CommandClone:
		return []string{"repo", "clone", args[1], args[2]}, true
	case len(args) == 1 && args[0] == CommandAuth:
		return []string{"auth", "status"}, true
	case len(args) == 2 && args[0] == CommandReviewedRepos:
		return []string{"search", "prs", "--reviewed-by=@me", "--updated=>=" + args[1], "--limit", "100",
			"--json", "repository", "--jq", ".[].repository.nameWithOwner"}, true
	case len(args) == 1 && args[0] == CommandTeams:
		return []string{"api", "user/teams", "--paginate", "--jq", `.[] | .organization.login + "/" + .slug`}, true
	}
	return nil, false
}

// Action runs the plugin's actions the host calls while it runs in process.
func (l Local) Action(ctx context.Context, req src.ActionRequest) (json.RawMessage, error) {
	if req.Action != ActionBasics {
		return nil, fmt.Errorf("%w: no action %s", src.ErrUnsupported, req.Action)
	}
	ref, ok := pr.RefOf(unit.Unit{Provider: unit.ProviderID(req.Unit.Provider), Key: req.Unit.Key})
	if !ok {
		return nil, fmt.Errorf("%w: %s is not a pull request", src.ErrNotFound, req.Unit.Key)
	}
	basics, err := l.API.Basics(ctx, ref)
	if err != nil {
		return nil, err
	}
	return json.Marshal(basics)
}
