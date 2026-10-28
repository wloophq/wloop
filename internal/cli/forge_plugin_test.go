package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/github"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/pkg/plugin/source/sourcetest"
)

// forgeStub is a source plugin serving the forge's identity and commands:
// clone runs as the clone command, and every command and identity asked is
// kept.
type forgeStub struct {
	*sourcetest.Stub
	login string
	clone func(repo, path string) error

	mu         sync.Mutex
	commands   [][]string
	identities int
}

func (s *forgeStub) Action(context.Context, src.ActionRequest) (json.RawMessage, error) {
	return nil, src.ErrUnsupported
}

func (s *forgeStub) View(context.Context, src.ViewRequest) (json.RawMessage, error) {
	return nil, src.ErrUnsupported
}

func (s *forgeStub) Identity(context.Context) (src.Identity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.identities++
	return src.Identity{Viewer: src.Person{Key: "github:" + s.login, Name: s.login}}, nil
}

func (s *forgeStub) RunCLI(_ context.Context, req src.CLIRequest, _, stderr io.Writer) (int, error) {
	s.mu.Lock()
	s.commands = append(s.commands, req.Args)
	s.mu.Unlock()
	if len(req.Args) == 3 && req.Args[0] == "clone" {
		if err := s.clone(req.Args[1], req.Args[2]); err != nil {
			fmt.Fprintln(stderr, err)
			return 1, nil
		}
		return 0, nil
	}
	fmt.Fprintf(stderr, "unknown command %q\n", req.Args)
	return 2, nil
}

func (s *forgeStub) asked() ([][]string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.commands), s.identities
}

// roundCommandFiles are the round and turn commands, which reach GitHub
// only through its plugin.
var roundCommandFiles = []string{"review.go", "address.go", "nudge.go", "round.go", "rounds.go", "turn.go", "unitturn.go", "stepstart.go", "builtins.go"}

func TestRoundCommandsReachGitHubThroughThePlugin(t *testing.T) {
	t.Run("the commands import no GitHub client", func(t *testing.T) {
		fset := token.NewFileSet()
		for _, file := range roundCommandFiles {
			f, err := parser.ParseFile(fset, file, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range f.Imports {
				p, _ := strconv.Unquote(imp.Path.Value)
				if strings.HasSuffix(p, "/internal/github") || strings.HasSuffix(p, "/internal/ghcli") {
					t.Errorf("%s imports %s", file, p)
				}
			}
		}
	})

	t.Run("a round's clone and the viewer go through the plugin", func(t *testing.T) {
		te, m, projects, _ := startEnv(t)
		var clones []string
		stub := &forgeStub{Stub: sourcetest.NewStub(), login: "ana", clone: cloneAs(t, "acme/api", &clones)}
		te.Env.forge = sourcetest.Launch(t, "github", stub, src.Host{})
		ctx := context.Background()
		e, err := te.Env.tasks.Capture(ctx, "add retries", "", nil)
		if err != nil {
			t.Fatal(err)
		}
		c := daemon.Call{Inputs: map[string]any{"repo": "acme/api"}, Out: io.Discard}
		if err := (envActions{te.Env, nil}).Intent(ctx, "start", task.UnitOf(e.ID), c); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(projects, "api")
		commands, _ := stub.asked()
		if !slices.EqualFunc(commands, [][]string{{"clone", "acme/api", path}}, slices.Equal) || len(clones) != 1 || len(m.Prompts) != 1 {
			t.Fatalf("plugin commands %q, clones %q, prompts %d", commands, clones, len(m.Prompts))
		}

		login, err := te.Env.viewerLogin(ctx)
		if _, identities := stub.asked(); err != nil || login != "ana" || identities != 1 {
			t.Fatalf("viewer %q, %v; identities asked %d", login, err, identities)
		}
		for _, call := range te.gh.calls {
			if strings.HasPrefix(call, "repo clone") || strings.Contains(call, "viewer") {
				t.Errorf("gh reached outside the plugin: %s", call)
			}
		}
	})

	t.Run("a failed clone is the command's error", func(t *testing.T) {
		te, _, _, _ := startEnv(t)
		stub := &forgeStub{Stub: sourcetest.NewStub(), clone: func(string, string) error { return fmt.Errorf("repository not found") }}
		te.Env.forge = sourcetest.Launch(t, "github", stub, src.Host{})
		_, err := te.Env.repoCheckout(context.Background(), te.cfg, "acme/api")
		if err == nil || !strings.Contains(err.Error(), "repository not found") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestAnUnavailableForgeIsTriedAgain(t *testing.T) {
	for _, err := range []error{fmt.Errorf("reading: %w", src.ErrUnavailable), github.ErrRateLimited, &github.RateLimitedError{Resource: "graphql"}} {
		if _, ok := startError(err).(automation.Transient); !ok {
			t.Errorf("startError(%v) = %#v, want transient", err, startError(err))
		}
	}
}
