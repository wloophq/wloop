package ghsource_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/github/githubtest"
)

type ghFunc func(args ...string) ([]byte, error)

func (f ghFunc) Run(_ context.Context, args ...string) ([]byte, error) { return f(args...) }

func TestFailedStepsReadsTheJobOnce(t *testing.T) {
	t.Parallel()
	var asked [][]string
	steps := ghsource.FailedSteps(githubtest.Runner(ghFunc(func(args ...string) ([]byte, error) {
		asked = append(asked, args)
		return []byte(`{"steps":[{"name":"Lint PR title","conclusion":"failure"},{"name":"Build","conclusion":"success"},{"name":"Run\ttests\u001b[2J","conclusion":"failure"}]}`), nil
	})))
	got, err := steps(context.Background(), "acme", "api", 114268776435)
	if err != nil || !slices.Equal(got, []string{"Lint PR title", "Run tests [2J"}) {
		t.Fatalf("%q %v", got, err)
	}
	if len(asked) != 1 || !slices.Equal(asked[0], []string{"api", "repos/acme/api/actions/jobs/114268776435"}) {
		t.Fatalf("gh %q", asked)
	}
	steps = ghsource.FailedSteps(githubtest.Runner(ghFunc(func(...string) ([]byte, error) { return nil, errors.New("HTTP 404") })))
	if _, err := steps(context.Background(), "acme", "api", 1); err == nil {
		t.Fatal("no error from a failed call")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFailedStepsOneRequestPerJob(t *testing.T) {
	t.Parallel()
	var paths []string
	gh := githubtest.Client(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"steps":[{"name":"Build","conclusion":"failure"}]}`)),
			Request:    r,
		}, nil
	}))
	gh.SetFloors(github.DefaultFloors(0.5))
	gh.Restore([]dgh.Limit{{Resource: dgh.ResourceCore, Limit: 5000, Remaining: 1000, ResetAt: time.Now().Add(time.Hour)}})
	background := dgh.WithPriority(context.Background(), dgh.PriorityBackground)
	if _, err := gh.REST(background, github.Request{Path: "repos/acme/api"}); !errors.Is(err, dgh.ErrBudget) {
		t.Fatalf("a background read at 20 %% was admitted: %v", err)
	}
	steps := ghsource.FailedSteps(gh)
	for _, job := range []int64{11, 12} {
		got, err := steps(background, "acme", "api", job)
		if err != nil || !slices.Equal(got, []string{"Build"}) {
			t.Fatalf("job %d: %q %v", job, got, err)
		}
	}
	if len(paths) != 2 || !strings.HasSuffix(paths[0], "/repos/acme/api/actions/jobs/11") || !strings.HasSuffix(paths[1], "/repos/acme/api/actions/jobs/12") {
		t.Fatalf("requests %q", paths)
	}
}
