package ghsource_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"testing"

	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

type recordingGH struct {
	calls [][]string
	err   error
}

func (g *recordingGH) Run(_ context.Context, args ...string) ([]byte, error) {
	g.calls = append(g.calls, args)
	return nil, g.err
}

type viewerOf string

func (v viewerOf) Viewer(context.Context) (string, error) { return string(v), nil }

func (v viewerOf) Basics(_ context.Context, ref pr.Ref) (*pr.PullRequest, error) {
	if ref.Number != 20 {
		return nil, fmt.Errorf("%s: %w", ref, src.ErrUnavailable)
	}
	return &pr.PullRequest{Facts: pr.Facts{Ref: ref, State: "OPEN", Author: string(v), HeadRefName: "feat/x"}}, nil
}

func TestTheLocalPluginClonesWithGhAndKnowsTheViewer(t *testing.T) {
	gh := &recordingGH{}
	l := ghsource.Local{GH: gh, API: viewerOf("ana")}
	var stderr bytes.Buffer
	exit, err := l.RunCLI(context.Background(), src.CLIRequest{Args: []string{ghsource.CommandClone, "acme/api", "/src/api"}}, &bytes.Buffer{}, &stderr)
	if err != nil || exit != 0 || len(gh.calls) != 1 || !slices.Equal(gh.calls[0], []string{"repo", "clone", "acme/api", "/src/api"}) {
		t.Fatalf("exit %d, %v, calls %q, stderr %q", exit, err, gh.calls, stderr.String())
	}
	gh.err = errors.New("repository not found")
	stderr.Reset()
	if exit, _ := l.RunCLI(context.Background(), src.CLIRequest{Args: []string{ghsource.CommandClone, "acme/gone", "/src/gone"}}, &bytes.Buffer{}, &stderr); exit == 0 || !strings.Contains(stderr.String(), "repository not found") {
		t.Fatalf("a failed clone: exit %d, stderr %q", exit, stderr.String())
	}
	if exit, _ := l.RunCLI(context.Background(), src.CLIRequest{Args: []string{"login"}}, &bytes.Buffer{}, &stderr); exit == 0 {
		t.Fatal("an undeclared command ran")
	}
	id, err := l.Identity(context.Background())
	if err != nil || id.Viewer.Key != "github:ana" || id.Viewer.Name != "ana" {
		t.Fatalf("identity %+v, %v", id, err)
	}
}

func TestTheLocalPluginReadsAPullRequestsBasics(t *testing.T) {
	l := ghsource.Local{API: viewerOf("bruno")}
	ctx := context.Background()
	raw, err := l.Action(ctx, src.ActionRequest{Unit: plugin.Unit{Provider: "github", Key: "acme/api#20"}, Action: ghsource.ActionBasics})
	var got pr.PullRequest
	if err != nil || json.Unmarshal(raw, &got) != nil || got.Ref != (pr.Ref{Owner: "acme", Repo: "api", Number: 20}) || got.Author != "bruno" || got.HeadRefName != "feat/x" {
		t.Fatalf("basics %s, %v", raw, err)
	}
	if _, err := l.Action(ctx, src.ActionRequest{Unit: plugin.Unit{Provider: "github", Key: "acme/api#21"}, Action: ghsource.ActionBasics}); !errors.Is(err, src.ErrUnavailable) {
		t.Fatalf("a failed read: %v", err)
	}
	if _, err := l.Action(ctx, src.ActionRequest{Unit: plugin.Unit{Provider: "slack", Key: "C1/1.2"}, Action: ghsource.ActionBasics}); !errors.Is(err, src.ErrNotFound) {
		t.Fatalf("a unit that is no pull request: %v", err)
	}
	if _, err := l.Action(ctx, src.ActionRequest{Action: "mute"}); !errors.Is(err, src.ErrUnsupported) {
		t.Fatalf("an action the local plugin does not run: %v", err)
	}
}

// answeringGH answers each command by its first two words.
type answeringGH struct {
	answers map[string]string
	fail    map[string]error
	calls   []string
}

func (g *answeringGH) Run(_ context.Context, args ...string) ([]byte, error) {
	g.calls = append(g.calls, strings.Join(args, " "))
	key := strings.Join(args[:min(2, len(args))], " ")
	return []byte(g.answers[key]), g.fail[key]
}

func (g *answeringGH) RunIn(ctx context.Context, dir string, args ...string) ([]byte, error) {
	g.calls = append(g.calls, "in "+dir)
	return g.Run(ctx, args...)
}

func run(t *testing.T, l ghsource.Local, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	exit, err := l.RunCLI(context.Background(), src.CLIRequest{Args: args}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("%q: %v", args, err)
	}
	return exit, stdout.String(), stderr.String()
}

func TestTheLocalPluginChecksGhAndReadsTheScopeSuggestions(t *testing.T) {
	gh := &answeringGH{answers: map[string]string{"search prs": "acme/api\nacme/web\n", "api user/teams": "acme/backend\n"}, fail: map[string]error{}}
	l := ghsource.Local{GH: gh}
	if exit, _, stderr := run(t, l, ghsource.CommandAuth); exit != 0 || stderr != "" {
		t.Fatalf("auth: exit %d, stderr %q", exit, stderr)
	}
	gh.fail["auth status"] = errors.New("gh auth status: not logged in")
	if exit, _, stderr := run(t, l, ghsource.CommandAuth); exit != 1 || !strings.Contains(stderr, "not logged in") {
		t.Fatalf("not logged in: exit %d, stderr %q", exit, stderr)
	}
	gh.fail["auth status"] = fmt.Errorf("gh auth status: %w", exec.ErrNotFound)
	if exit, _, _ := run(t, l, ghsource.CommandAuth); exit != ghsource.ExitNoGH {
		t.Fatalf("no gh: exit %d", exit)
	}
	exit, out, _ := run(t, l, ghsource.CommandReviewedRepos, "2026-09-28")
	if exit != 0 || out != "acme/api\nacme/web\n" || !strings.Contains(gh.calls[len(gh.calls)-1], "--reviewed-by=@me --updated=>=2026-09-28") {
		t.Fatalf("reviewed repos: exit %d, %q, calls %q", exit, out, gh.calls)
	}
	if exit, out, _ := run(t, l, ghsource.CommandTeams); exit != 0 || out != "acme/backend\n" {
		t.Fatalf("teams: exit %d, %q", exit, out)
	}
	gh.fail["api user/teams"] = errors.New("HTTP 403")
	if exit, _, stderr := run(t, l, ghsource.CommandTeams); exit != 1 || !strings.Contains(stderr, "HTTP 403") {
		t.Fatalf("a failed read: exit %d, %q", exit, stderr)
	}
	if exit, _, _ := run(t, l, ghsource.CommandReviewedRepos); exit != 2 {
		t.Fatalf("reviewed repos without a date: exit %d", exit)
	}
}

func TestTheLocatorReadsTheCheckoutsPullRequest(t *testing.T) {
	gh := &answeringGH{answers: map[string]string{"pr view": "https://github.com/acme/api/pull/7\n"}, fail: map[string]error{}}
	u, ok, err := ghsource.Locator{GH: gh}.Locate(context.Background(), "/src/api")
	if err != nil || !ok || u != (pr.Ref{Owner: "acme", Repo: "api", Number: 7}).Unit() || !slices.Contains(gh.calls, "in /src/api") {
		t.Fatalf("%v %v %v, calls %q", u, ok, err, gh.calls)
	}
	gh.fail["pr view"] = errors.New("no pull requests found for branch")
	if _, _, err := (ghsource.Locator{GH: gh}).Locate(context.Background(), "/src/api"); err == nil {
		t.Fatal("no error for a branch with no pull request")
	}
	if _, _, err := (ghsource.Locator{GH: &recordingGH{}}).Locate(context.Background(), "/src/api"); err == nil {
		t.Fatal("a runner that cannot run in a directory located")
	}
}
