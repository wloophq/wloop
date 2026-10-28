package ghsource

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	dgh "github.com/wloophq/wloop/internal/domain/github"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/termfmt"
)

// Clients are what the GitHub plugin reaches GitHub through while it runs
// in the host's process: gh, and GitHub's API.
type Clients struct {
	GH  ghRunner
	API *github.Client
}

// NewClients are gh on the PATH and an API client that asks gh for its token.
func NewClients() Clients { return Clients{GH: github.CLI(), API: github.NewClient(github.Options{})} }

// Local is the plugin as the host calls it over c.
func (c Clients) Local() Local { return Local{GH: c.GH, API: c.API} }

// SetFloors sets the API's budget floors for a background rate limit
// fraction.
func (c Clients) SetFloors(fraction float64) { c.API.SetFloors(github.DefaultFloors(fraction)) }

// FailedSteps reads the names of the failed steps of a check's job, one
// request per job.
func FailedSteps(api *github.Client) func(ctx context.Context, owner, repo string, job int64) ([]string, error) {
	return func(ctx context.Context, owner, repo string, job int64) ([]string, error) {
		resp, err := api.REST(dgh.WithPriority(ctx, dgh.PriorityUser), github.Request{Path: fmt.Sprintf("repos/%s/%s/actions/jobs/%d", owner, repo, job)})
		if err != nil {
			return nil, err
		}
		var body struct {
			Steps []struct {
				Name       string `json:"name"`
				Conclusion string `json:"conclusion"`
			} `json:"steps"`
		}
		if err := json.Unmarshal(resp.Body, &body); err != nil {
			return nil, err
		}
		var steps []string
		for _, s := range body.Steps {
			if s.Conclusion == "failure" {
				steps = append(steps, termfmt.OneLine(s.Name, 120))
			}
		}
		return steps, nil
	}
}

type dirRunner interface {
	RunIn(ctx context.Context, dir string, args ...string) ([]byte, error)
}

// Locator is the pull request of a checkout's branch, as gh reads it.
type Locator struct{ GH ghRunner }

func (l Locator) Locate(ctx context.Context, dir string) (unit.Unit, bool, error) {
	out, err := l.run(ctx, dir, "pr", "view", "--json", "url", "--jq", ".url")
	if err != nil {
		return unit.Unit{}, false, err
	}
	ref, err := pr.ParseURL(strings.TrimSpace(string(out)))
	if err != nil {
		return unit.Unit{}, false, err
	}
	return ref.Unit(), true, nil
}

func (l Locator) run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if dir == "" {
		return l.GH.Run(ctx, args...)
	}
	d, ok := l.GH.(dirRunner)
	if !ok {
		return nil, fmt.Errorf("gh %s: this runner cannot run in %s", strings.Join(args[:2], " "), dir)
	}
	return d.RunIn(ctx, dir, args...)
}
