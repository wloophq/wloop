package ghsource_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"
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
