// Package github is the GitHub source plugin: what its binary serves
// through the SDK, over its own store in the state directory the host
// gives it.
package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	sdk "github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/api"
	"github.com/wloophq/wloop/plugins/github/collect"
	"github.com/wloophq/wloop/plugins/github/gh"
	"github.com/wloophq/wloop/plugins/github/plan"
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
	// API are the API client's options; the zero value is GitHub's API at
	// the host gh names, with gh's token. The plugin sets its meter and
	// user agent.
	API api.Options
	// Now nil is the clock.
	Now func() time.Time
	// Git nil reads the checkouts with the git binary.
	Git plan.Git

	mu  sync.Mutex
	run *running
	// stop ends the first report, which loading waits for.
	stop    context.CancelFunc
	loading sync.WaitGroup
	// watching ends with the reports at their due times.
	watching sync.WaitGroup
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
	if p.run == nil {
		return nil
	}
	return p.run.db
}

// Start opens the plugin's store in its state directory, importing what
// the host exported once, and reports every stored pull request; the
// plugin is loading until it has.
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
	if p.run != nil {
		db.Close()
		return errors.New("github: the plugin has started")
	}
	r := p.start(ctx, db, req.Host)
	p.run = r
	lctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	p.stop = cancel
	p.loading.Go(func() { r.load(lctx) })
	p.watching.Go(func() {
		r.watchDue(lctx, func(ctx context.Context, refs []pr.Ref) error {
			r.mark(refs...)
			return r.report(ctx, false)
		})
	})
	return nil
}

// Stop ends the first report and closes the store.
func (p *Plugin) Stop(context.Context) error {
	p.mu.Lock()
	r, stop := p.run, p.stop
	p.run, p.stop = nil, nil
	p.mu.Unlock()
	if r == nil {
		return nil
	}
	stop()
	p.loading.Wait()
	p.watching.Wait()
	return r.db.Close()
}

// Health is loading until the store is open and every stored pull request
// was reported.
func (p *Plugin) Health(context.Context) (sdk.Health, error) {
	p.mu.Lock()
	r := p.run
	p.mu.Unlock()
	if r == nil {
		return sdk.Health{Status: sdk.StatusLoading, Reason: "starting"}, nil
	}
	loaded, why := r.isLoaded()
	switch {
	case !loaded:
		return sdk.Health{Status: sdk.StatusLoading, Reason: "reporting the pull requests"}, nil
	case why != "":
		return sdk.Health{Status: sdk.StatusDegraded, Reason: "reporting the pull requests: " + why}, nil
	}
	return sdk.Health{Status: sdk.StatusServing}, nil
}

// Notify links the workspaces again when the host's listing moved, reads
// the configuration again when it changed, and reports again the pull
// requests whose rows read what changed: their events and turns, the
// viewer, the configuration.
func (p *Plugin) Notify(ctx context.Context, units []sdk.Unit, topics []sdk.Topic) error {
	r, err := p.running()
	if err != nil {
		return nil
	}
	if slices.Contains(topics, sdk.TopicWorkspaces) {
		r.lists.forget()
		if err := r.col.Relink(ctx); err != nil {
			return err
		}
	}
	if slices.Contains(topics, sdk.TopicConfig) {
		r.reloadConfig(ctx)
	}
	all := slices.ContainsFunc(topics, func(t sdk.Topic) bool {
		return t == sdk.TopicViewer || t == sdk.TopicConfig || t == sdk.TopicWorkspaces
	})
	if slices.ContainsFunc(topics, func(t sdk.Topic) bool {
		return t == sdk.TopicEvents || t == sdk.TopicTurns || t == sdk.TopicRounds || t == sdk.TopicMarks || t == sdk.TopicOverrides
	}) {
		if len(units) == 0 {
			all = true
		}
		for _, u := range units {
			if ref, ok := pr.RefOf(u); ok {
				r.mark(ref)
			}
		}
	}
	return r.report(ctx, all)
}

// Identity is the viewer: the login the store keeps, else the one GitHub's
// API names once started, or gh in CLI mode, which the store then keeps;
// and the scope teams the viewer is in.
func (p *Plugin) Identity(ctx context.Context) (src.Identity, error) {
	p.mu.Lock()
	r := p.run
	p.mu.Unlock()
	var db *store.DB
	login := ""
	if r != nil {
		db = r.db
		v, err := db.Viewer(ctx)
		if err != nil {
			return src.Identity{}, err
		}
		login = v
	}
	if login == "" {
		var err error
		if login, err = p.viewer(ctx, r); err != nil {
			return src.Identity{}, fmt.Errorf("%w: %w", src.ErrUnavailable, err)
		}
		if login == "" {
			return src.Identity{}, fmt.Errorf("%w: GitHub named no viewer", src.ErrUnavailable)
		}
		if db != nil {
			if err := db.SetViewer(ctx, login); err != nil {
				return src.Identity{}, err
			}
		}
	}
	id := src.Identity{Viewer: src.Person{Key: pr.Provider + ":" + login, Name: login}}
	if db != nil {
		teams, err := collect.MyTeams(ctx, db)
		if err != nil {
			return src.Identity{}, err
		}
		id.Teams = teams
	}
	return id, nil
}

// viewer is the viewer's login as GitHub's API names it, or gh when the
// plugin has not started.
func (p *Plugin) viewer(ctx context.Context, r *running) (string, error) {
	if r != nil {
		return r.client.Viewer(ctx)
	}
	out, err := p.gh().Run(ctx, "api", "user", "--jq", ".login")
	return strings.TrimSpace(string(out)), err
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
