package ghsource

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// CommandClone is the command plugin.toml declares that clones a
// repository: clone <owner>/<name> <path>.
const CommandClone = "clone"

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
func (l Local) RunCLI(ctx context.Context, req src.CLIRequest, _, stderr io.Writer) (int, error) {
	if len(req.Args) == 3 && req.Args[0] == CommandClone {
		if _, err := l.GH.Run(ctx, "repo", "clone", req.Args[1], req.Args[2]); err != nil {
			fmt.Fprintln(stderr, err)
			return 1, nil
		}
		return 0, nil
	}
	fmt.Fprintf(stderr, "unknown command %q\n", req.Args)
	return 2, nil
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
