// Package github is the GitHub source plugin: what its binary serves
// through the SDK, over its own store in the state directory the host
// gives it.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"

	sdk "github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/gh"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/store"
)

const (
	// CommandClone clones a repository: clone <owner>/<name> <path>.
	CommandClone = "clone"
	// CommandAuth checks that gh is installed and logged in; it exits
	// ExitNoGH when gh is not installed.
	CommandAuth = "auth"
	// CommandReviewedRepos lists the repositories of the pull requests the
	// viewer reviewed since a date: reviewed-repos <yyyy-mm-dd>, one
	// owner/name a line, once per pull request.
	CommandReviewedRepos = "reviewed-repos"
	// CommandTeams lists the viewer's teams, one org/slug a line.
	CommandTeams = "teams"
)

// Commands are the commands the plugin serves in CLI mode.
var Commands = []string{CommandClone, CommandAuth, CommandReviewedRepos, CommandTeams}

// ExitNoGH is the exit of a command that found no gh to run, the shell's
// "command not found".
const ExitNoGH = 127

// Plugin is the GitHub source plugin as the SDK serves it.
type Plugin struct {
	// GH nil is gh on the PATH.
	GH gh.Runner

	mu sync.Mutex
	db *store.DB
}

var (
	_ src.Source    = (*Plugin)(nil)
	_ src.Presenter = (*Plugin)(nil)
	_ src.CLIRunner = (*Plugin)(nil)
)

func (p *Plugin) gh() gh.Runner {
	if p.GH != nil {
		return p.GH
	}
	return gh.Exec{}
}

func (p *Plugin) store() *store.DB {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.db
}

// Start opens the plugin's store in its state directory, importing what
// the host exported once.
func (p *Plugin) Start(ctx context.Context, req src.StartRequest) error {
	if err := os.MkdirAll(req.StateDir, 0o700); err != nil {
		return err
	}
	db, err := store.Open(ctx, req.StateDir)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.db != nil {
		db.Close()
		return errors.New("github: the plugin has started")
	}
	p.db = db
	return nil
}

// Stop closes the store.
func (p *Plugin) Stop(context.Context) error {
	p.mu.Lock()
	db := p.db
	p.db = nil
	p.mu.Unlock()
	if db == nil {
		return nil
	}
	return db.Close()
}

// Health is loading until the store is open.
func (p *Plugin) Health(context.Context) (sdk.Health, error) {
	if p.store() == nil {
		return sdk.Health{Status: sdk.StatusLoading, Reason: "starting"}, nil
	}
	return sdk.Health{Status: sdk.StatusServing}, nil
}

func (p *Plugin) Notify(context.Context, []sdk.Unit, []sdk.Topic) error { return nil }

// Identity is the viewer as gh names them, kept in the store once started.
func (p *Plugin) Identity(ctx context.Context) (src.Identity, error) {
	out, err := p.gh().Run(ctx, "api", "user", "--jq", ".login")
	if err != nil {
		return src.Identity{}, fmt.Errorf("%w: %w", src.ErrUnavailable, err)
	}
	login := strings.TrimSpace(string(out))
	if login == "" {
		return src.Identity{}, fmt.Errorf("%w: gh named no viewer", src.ErrUnavailable)
	}
	if db := p.store(); db != nil {
		if err := db.SetViewer(ctx, login); err != nil {
			return src.Identity{}, err
		}
	}
	return src.Identity{Viewer: src.Person{Key: pr.Provider + ":" + login, Name: login}}, nil
}

func (p *Plugin) Action(_ context.Context, req src.ActionRequest) (json.RawMessage, error) {
	return nil, fmt.Errorf("%w: no action %s", src.ErrUnsupported, req.Action)
}

func (p *Plugin) View(_ context.Context, req src.ViewRequest) (json.RawMessage, error) {
	return nil, fmt.Errorf("%w: no view %s", src.ErrUnsupported, req.View)
}

// RunCLI runs one of the commands plugin.toml declares, with gh.
func (p *Plugin) RunCLI(ctx context.Context, req src.CLIRequest, stdout, stderr io.Writer) (int, error) {
	args, ok := ghArgs(req.Args)
	if !ok {
		fmt.Fprintf(stderr, "unknown command %q\n", req.Args)
		return 2, nil
	}
	out, err := p.gh().Run(ctx, args...)
	switch {
	case errors.Is(err, exec.ErrNotFound):
		fmt.Fprintln(stderr, err)
		return ExitNoGH, nil
	case err != nil:
		fmt.Fprintln(stderr, err)
		return 1, nil
	}
	if !slices.Contains([]string{CommandClone, CommandAuth}, req.Args[0]) {
		_, _ = stdout.Write(out)
	}
	return 0, nil
}

// ghArgs is the gh command a plugin command runs.
func ghArgs(args []string) ([]string, bool) {
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
