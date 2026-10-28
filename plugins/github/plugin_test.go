package github

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"

	sdk "github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/store"
)

// fakeGH answers each gh call by its first two words.
type fakeGH struct {
	mu      sync.Mutex
	answers map[string]string
	fail    map[string]error
	calls   []string
}

func (g *fakeGH) Run(_ context.Context, args ...string) ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, strings.Join(args, " "))
	key := strings.Join(args[:min(2, len(args))], " ")
	if err := g.fail[key]; err != nil {
		return nil, err
	}
	return []byte(g.answers[key]), nil
}

func run(t *testing.T, p *Plugin, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	exit, err := p.RunCLI(context.Background(), src.CLIRequest{Args: args}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("%q: %v", args, err)
	}
	return exit, stdout.String(), stderr.String()
}

func TestThePluginServesItsCommandsWithGh(t *testing.T) {
	t.Parallel()
	gh := &fakeGH{answers: map[string]string{"search prs": "acme/api\nacme/web\n", "api user/teams": "acme/backend\n", "repo clone": "Cloning\n"}, fail: map[string]error{}}
	p := &Plugin{GH: gh}
	if exit, out, stderr := run(t, p, CommandClone, "acme/api", "/projects/api"); exit != 0 || out != "" || stderr != "" {
		t.Fatalf("clone: exit %d, %q, %q", exit, out, stderr)
	}
	if got := gh.calls[len(gh.calls)-1]; got != "repo clone acme/api /projects/api" {
		t.Fatalf("clone ran %q", got)
	}
	gh.fail["repo clone"] = errors.New("gh repo clone: already exists")
	if exit, _, stderr := run(t, p, CommandClone, "acme/api", "/projects/api"); exit != 1 || !strings.Contains(stderr, "already exists") {
		t.Fatalf("a failed clone: exit %d, %q", exit, stderr)
	}
	if exit, _, stderr := run(t, p, CommandAuth); exit != 0 || stderr != "" {
		t.Fatalf("auth: exit %d, stderr %q", exit, stderr)
	}
	gh.fail["auth status"] = errors.New("gh auth status: not logged in")
	if exit, _, stderr := run(t, p, CommandAuth); exit != 1 || !strings.Contains(stderr, "not logged in") {
		t.Fatalf("not logged in: exit %d, stderr %q", exit, stderr)
	}
	gh.fail["auth status"] = fmt.Errorf("gh auth status: %w", exec.ErrNotFound)
	if exit, _, _ := run(t, p, CommandAuth); exit != ExitNoGH {
		t.Fatalf("no gh: exit %d", exit)
	}
	exit, out, _ := run(t, p, CommandReviewedRepos, "2026-09-28")
	if exit != 0 || out != "acme/api\nacme/web\n" || !strings.Contains(gh.calls[len(gh.calls)-1], "--reviewed-by=@me --updated=>=2026-09-28") {
		t.Fatalf("reviewed repos: exit %d, %q, calls %q", exit, out, gh.calls)
	}
	if exit, out, _ := run(t, p, CommandTeams); exit != 0 || out != "acme/backend\n" {
		t.Fatalf("teams: exit %d, %q", exit, out)
	}
	if exit, _, _ := run(t, p, CommandReviewedRepos); exit != 2 {
		t.Fatalf("reviewed repos without a date: exit %d", exit)
	}
	if exit, _, stderr := run(t, p, "merge", "acme/api#1"); exit != 2 || !strings.Contains(stderr, "unknown command") {
		t.Fatalf("an unknown command: exit %d, %q", exit, stderr)
	}
}

func TestTheManifestDeclaresTheCommandsThePluginServes(t *testing.T) {
	t.Parallel()
	m, err := manifest.Load(".")
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Source()
	if err != nil {
		t.Fatal(err)
	}
	var declared []string
	for _, c := range s.Commands {
		declared = append(declared, c.Name)
	}
	slices.Sort(declared)
	if served := slices.Sorted(slices.Values(Commands)); !slices.Equal(declared, served) {
		t.Fatalf("the manifest declares %v, the plugin serves %v", declared, served)
	}
}

func TestIdentityIsTheViewerGhNamesAndTheStoreKeepsIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	gh := &fakeGH{answers: map[string]string{"api user": "ana\n"}, fail: map[string]error{}}
	p := &Plugin{GH: gh}
	id, err := p.Identity(ctx)
	if err != nil || id.Viewer.Key != "github:ana" || id.Viewer.Name != "ana" {
		t.Fatalf("identity in CLI mode %+v %v", id, err)
	}
	dir := t.TempDir()
	if err := p.Start(ctx, src.StartRequest{StateDir: dir}); err != nil {
		t.Fatal(err)
	}
	if h, err := p.Health(ctx); err != nil || h.Status != sdk.StatusServing {
		t.Fatalf("health %+v %v", h, err)
	}
	if _, err := p.Identity(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if v, err := db.Viewer(ctx); err != nil || v != "ana" {
		t.Fatalf("stored viewer %q %v", v, err)
	}

	gh.fail["api user"] = errors.New("gh api user: HTTP 502")
	if _, err := p.Identity(ctx); !errors.Is(err, src.ErrUnavailable) {
		t.Fatalf("a failed read: %v", err)
	}
}

func TestHealthIsLoadingUntilStarted(t *testing.T) {
	t.Parallel()
	p := &Plugin{GH: &fakeGH{}}
	if h, err := p.Health(context.Background()); err != nil || h.Status != sdk.StatusLoading {
		t.Fatalf("health %+v %v", h, err)
	}
	if err := p.Stop(context.Background()); err != nil {
		t.Fatalf("a stop before a start: %v", err)
	}
}
