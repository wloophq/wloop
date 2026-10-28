package ghsource_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
