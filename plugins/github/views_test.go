package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/api/apitest"
	"github.com/wloophq/wloop/plugins/github/pr"
)

// jobsGH answers the REST reads of GitHub Actions jobs with steps, and
// counts them; job 1 fails.
type jobsGH struct {
	mu    sync.Mutex
	asked []string
}

func (g *jobsGH) Run(_ context.Context, args ...string) ([]byte, error) {
	path := args[len(args)-1]
	for _, a := range args {
		if strings.HasPrefix(a, "repos/") {
			path = a
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.asked = append(g.asked, path)
	if strings.HasSuffix(path, "/jobs/1") {
		return nil, errors.New("gh: HTTP 403")
	}
	return []byte(`{"steps":[{"name":"Lint PR title","conclusion":"failure"},{"name":"Build","conclusion":"success"},{"name":"Run\ttests\u001b[2J","conclusion":"failure"}]}`), nil
}

func (g *jobsGH) calls() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := slices.Clone(g.asked)
	slices.Sort(out)
	g.asked = nil
	return out
}

// testClock is a clock a test moves.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// checksPlugin is the plugin started over gh at clock, with the pull
// request acme/api#7 failing checks.
func checksPlugin(t *testing.T, gh *jobsGH, clock *testClock, checks ...pr.FailingCheck) *Plugin {
	t.Helper()
	p := &Plugin{GH: &fakeGH{answers: map[string]string{}}, API: apitest.Options(gh), Git: noCheckouts{}, Now: clock.Now}
	if err := p.Start(context.Background(), src.StartRequest{StateDir: t.TempDir(), Host: (&fakeHost{}).host()}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Stop(context.Background()) })
	p.loading.Wait()
	if err := p.run.db.UpsertPR(context.Background(), pr.PR{Ref: ref7, State: "OPEN", ChecksState: "FAILURE", FailingChecks: checks}); err != nil {
		t.Fatal(err)
	}
	gh.calls()
	return p
}

func checksOf(t *testing.T, p *Plugin, ref pr.Ref) ([]Check, error) {
	t.Helper()
	q, _ := json.Marshal(ChecksQuery{Owner: ref.Owner, Repo: ref.Repo, Number: ref.Number})
	raw, err := p.View(context.Background(), src.ViewRequest{View: ViewChecks, Query: q})
	if err != nil {
		return nil, err
	}
	var out []Check
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

func TestTheChecksViewNamesTheFailedStepsOnce(t *testing.T) {
	t.Parallel()
	gh, clock := &jobsGH{}, &testClock{now: testAt}
	p := checksPlugin(t, gh, clock, pr.FailingCheck{Name: "Build and test", URL: "https://github.com/acme/api/actions/runs/38071193224/job/114268776435"},
		pr.FailingCheck{Name: "Preview site", Summary: "Build ou deploy do preview falhou"})
	var got []Check
	for range 2 {
		var err error
		if got, err = checksOf(t, p, ref7); err != nil {
			t.Fatal(err)
		}
	}
	if c := gh.calls(); !slices.Equal(c, []string{"repos/acme/api/actions/jobs/114268776435"}) {
		t.Fatalf("asked %v", c)
	}
	if len(got) != 2 || !slices.Equal(got[0].Steps, []string{"Lint PR title", "Run tests [2J"}) || got[1].Summary == "" || got[1].Steps != nil {
		t.Fatalf("got %+v", got)
	}
	if _, err := checksOf(t, p, pr.Ref{Owner: "acme", Repo: "api", Number: 99}); !errors.Is(err, src.ErrNotFound) {
		t.Fatalf("unknown pull request: %v", err)
	}
}

func TestTheChecksViewAsksAtMostFiveJobsAndRemembersAFailureForAWhile(t *testing.T) {
	t.Parallel()
	gh, clock := &jobsGH{}, &testClock{now: testAt}
	var checks []pr.FailingCheck
	for job := 1; job <= 6; job++ {
		checks = append(checks, pr.FailingCheck{Name: fmt.Sprint("job ", job), URL: fmt.Sprintf("https://github.com/acme/api-fork/actions/runs/9/job/%d", job)})
	}
	checks = append(checks, pr.FailingCheck{Name: "huge", URL: "https://github.com/acme/api/actions/runs/9/job/99999999999999999999"})
	p := checksPlugin(t, gh, clock, checks...)
	if got, err := checksOf(t, p, ref7); err != nil || len(got) != 7 {
		t.Fatalf("%+v %v", got, err)
	}
	if c := gh.calls(); len(c) != 5 || c[0] != "repos/acme/api-fork/actions/jobs/1" {
		t.Fatalf("first load asked %v", c)
	}
	checksOf(t, p, ref7)
	if c := gh.calls(); !slices.Equal(c, []string{"repos/acme/api-fork/actions/jobs/6"}) {
		t.Fatalf("second load asked %v: a failure or a success was asked again", c)
	}
	clock.Add(stepRetry + time.Second)
	checksOf(t, p, ref7)
	if c := gh.calls(); !slices.Equal(c, []string{"repos/acme/api-fork/actions/jobs/1"}) {
		t.Fatalf("after the retry delay asked %v", c)
	}
}

func TestTheBudgetViewIsTheGraphQLBucket(t *testing.T) {
	t.Parallel()
	p := started(t, &githubGraphQL{}, &fakeHost{})
	raw, err := p.View(context.Background(), src.ViewRequest{View: ViewBudget})
	if err != nil {
		t.Fatal(err)
	}
	var b Budget
	if err := json.Unmarshal(raw, &b); err != nil || b.Low {
		t.Fatalf("%+v %v", b, err)
	}
}
