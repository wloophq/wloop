package cli

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/round"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/workspace/fake"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/pkg/plugin/source/sourcetest"
)

// inventoryCommandFiles are the inventory commands, which reach GitHub only
// through its plugin.
var inventoryCommandFiles = []string{"adopt.go", "ahead.go", "hand.go", "intake.go", "pause.go", "remove.go", "trust.go", "try.go", "actions.go"}

// githubMarks are the marks GitHub's rounds write, by their name in the
// round package.
var githubMarks = map[string]string{
	"Reviewed":          round.Reviewed,
	"AddressedReviews":  round.AddressedReviews,
	"AddressedChecks":   round.AddressedChecks,
	"AddressedConflict": round.AddressedConflict,
	"FirstSkipped":      round.FirstSkipped,
}

// pullStub is a forge plugin that also serves its pull requests' basics as
// an action, keeping each action asked.
type pullStub struct {
	*forgeStub
	pull *pr.PullRequest
	err  error

	mu      sync.Mutex
	actions []src.ActionRequest
}

func (s *pullStub) Action(_ context.Context, req src.ActionRequest) (json.RawMessage, error) {
	s.mu.Lock()
	s.actions = append(s.actions, req)
	s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	return json.Marshal(s.pull)
}

func (s *pullStub) asked() []src.ActionRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.actions)
}

func theirPull(author string) *pr.PullRequest {
	return &pr.PullRequest{Facts: pr.Facts{Ref: pr.Ref{Owner: "acme", Repo: "api", Number: 20}, State: "OPEN", Title: "Theirs",
		HeadRefName: "feat/theirs", BaseRefName: "main", HeadRefOid: "c9", Author: author}}
}

func TestInventoryCommandsReachGitHubThroughThePlugin(t *testing.T) {
	t.Run("the commands import no GitHub client and name no GitHub mark", func(t *testing.T) {
		marks := map[string]bool{}
		for _, v := range githubMarks {
			marks[v] = true
		}
		fset := token.NewFileSet()
		for _, file := range inventoryCommandFiles {
			f, err := parser.ParseFile(fset, file, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range f.Imports {
				p, _ := strconv.Unquote(imp.Path.Value)
				if strings.HasSuffix(p, "/internal/github") || strings.HasSuffix(p, "/internal/ghcli") {
					t.Errorf("%s imports %s", file, p)
				}
			}
			ast.Inspect(f, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.SelectorExpr:
					if x, ok := n.X.(*ast.Ident); ok && x.Name == "round" && githubMarks[n.Sel.Name] != "" {
						t.Errorf("%s: %s names the GitHub mark round.%s", file, fset.Position(n.Pos()), n.Sel.Name)
					}
				case *ast.BasicLit:
					if s, err := strconv.Unquote(n.Value); n.Kind == token.STRING && err == nil && marks[s] {
						t.Errorf("%s: %s names the GitHub mark %q", file, fset.Position(n.Pos()), s)
					}
				}
				return true
			})
		}
	})

	takeOver := func(t *testing.T, author string, m *fake.Manager, checkout string) (*testEnv, *pullStub, int) {
		m.RepoList = []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}
		te := newTestEnv(t, m)
		te.git.branch[checkout] = "feat/theirs"
		stub := &pullStub{forgeStub: &forgeStub{Stub: sourcetest.NewStub(), login: "ana"}, pull: theirPull(author)}
		te.Env.forge = sourcetest.Launch(t, "github", stub, src.Host{})
		code := te.run("take-over", "https://github.com/acme/api/pull/20")
		for _, call := range te.gh.calls {
			if strings.Contains(call, "pr: repository") || strings.Contains(call, "viewer") {
				t.Errorf("gh reached outside the plugin: %s", call)
			}
		}
		asked := stub.asked()
		if len(asked) != 1 || asked[0].Unit.Provider != "github" || asked[0].Unit.Key != "acme/api#20" {
			t.Fatalf("plugin actions %+v", asked)
		}
		return te, stub, code
	}

	t.Run("a take-over reads the pull request and the viewer through the plugin", func(t *testing.T) {
		m := &fake.Manager{WorkspaceList: []wsdomain.Workspace{{ID: "main-api", RepoID: "r1", Path: "/src/api", DisplayName: "api", Branch: "feat/theirs", HostID: "local", IsMain: true}}}
		te, stub, code := takeOver(t, "ana", m, "/src/api")
		if code != 0 {
			t.Fatalf("exit %d, the main checkout on ana's branch is not the plugin's viewer ana's: %s", code, te.err)
		}
		if _, identities := stub.forgeStub.asked(); identities != 1 || !strings.Contains(te.out.String(), "already an author workspace: api") {
			t.Fatalf("identities asked %d: %s", identities, te.out)
		}
	})

	t.Run("someone else's pull request is taken over as the plugin read it", func(t *testing.T) {
		m := &fake.Manager{}
		te, _, code := takeOver(t, "bruno", m, "/worktrees/feat/theirs")
		if code != 0 {
			t.Fatalf("exit %d: %s", code, te.err)
		}
		if got := strings.Join(m.Writes, "\n"); got != "create feat/theirs\nsession ws-1\nlink ws-1 20" {
			t.Fatalf("writes:\n%s", got)
		}
	})

	t.Run("whether a pull request is mine asks the plugin's viewer", func(t *testing.T) {
		te := newTestEnv(t, &fake.Manager{})
		stub := &forgeStub{Stub: sourcetest.NewStub(), login: "ana"}
		te.Env.forge = sourcetest.Launch(t, "github", stub, src.Host{})
		mine, err := te.Env.prOf(context.Background(), theirPull("ana"))()
		if _, identities := stub.asked(); err != nil || !mine || identities != 1 {
			t.Fatalf("mine %v, %v; identities asked %d", mine, err, identities)
		}
	})

	t.Run("an unavailable plugin is the source unavailable", func(t *testing.T) {
		te := newTestEnv(t, &fake.Manager{})
		stub := &pullStub{forgeStub: &forgeStub{Stub: sourcetest.NewStub(), login: "ana"}, err: src.ErrUnavailable}
		te.Env.forge = sourcetest.Launch(t, "github", stub, src.Host{})
		if _, err := te.Env.basics(context.Background(), pr.Ref{Owner: "acme", Repo: "api", Number: 20}); !errors.Is(err, src.ErrUnavailable) {
			t.Fatalf("err = %v, want the source unavailable", err)
		}
	})
}
