package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/workspace/fake"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/pkg/plugin/source/sourcetest"
)

// viewingStub is a GitHub plugin that counts the views it serves.
type viewingStub struct {
	*forgeStub
	views []string
}

func (s *viewingStub) View(_ context.Context, req src.ViewRequest) (json.RawMessage, error) {
	s.views = append(s.views, req.View)
	return json.RawMessage(`{"query":"acme/api","candidates":[]}`), nil
}

// TestServeHandsGitHubsAPIClientToNoInProcessModule: under serve the host
// holds no GitHub client and no credential; Explore and the budget are the
// GitHub plugin's views, and with no plugin running nothing in the host
// reaches GitHub.
func TestServeHandsGitHubsAPIClientToNoInProcessModule(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	stub := &viewingStub{forgeStub: &forgeStub{Stub: sourcetest.NewStub(), login: "ana"}}
	te.Env.forge = sourcetest.Launch(t, "github", stub, src.Host{})
	deps := te.githubPresenter(te.Store, src.Host{}, nil)
	if _, err := deps.View(context.Background(), src.ViewRequest{View: "explore"}); err != nil {
		t.Fatal(err)
	}
	if len(stub.views) != 1 || stub.views[0] != "explore" {
		t.Fatalf("the plugin served %v", stub.views)
	}

	idle := newTestEnv(t, &fake.Manager{})
	idle.Env.forge = nil
	f := idle.Env.forgeCalls()
	if _, ok := f.(binaryForge); !ok {
		t.Fatalf("with no plugin running the forge is %T", f)
	}
	if _, err := f.Identity(context.Background()); !errors.Is(err, src.ErrUnavailable) {
		t.Fatalf("the viewer with no plugin running: %v", err)
	}
	if _, err := f.View(context.Background(), src.ViewRequest{View: "explore"}); !errors.Is(err, src.ErrUnavailable) {
		t.Fatalf("Explore with no plugin running: %v", err)
	}

	for _, gone := range []string{"../ghcli", "../explore", "../scope"} {
		if _, err := os.Stat(gone); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s is still in the host", gone)
		}
	}
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, banned := range []string{`"github.forward"`, `Run(ctx, "auth", "token"`, "ghToken"} {
			if strings.Contains(string(raw), banned) {
				t.Errorf("%s holds %s: the host reaches GitHub itself", path, banned)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
