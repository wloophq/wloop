package github

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"sync"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/api"
	"github.com/wloophq/wloop/plugins/github/explore"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/store"
)

// The views the host asks for: a search of GitHub for Explore, the recent
// searches, the failed steps of a pull request's failing checks and the
// API's budget.
const (
	ViewExplore       = "explore"
	ViewExploreRecent = "explore.recent"
	ViewChecks        = "checks"
	ViewBudget        = "budget"
	// ViewCandidate reads one pull request from GitHub as Explore shows
	// it, in any state; its query is a ChecksQuery.
	ViewCandidate = "candidate"
)

// ExploreQuery is what Explore asks for.
type ExploreQuery struct {
	Query string `json:"query"`
}

// ChecksQuery names the pull request a view reads.
type ChecksQuery struct {
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	Number int    `json:"number"`
}

// Check is a failing check of a pull request, with the failed steps of
// its GitHub Actions job when they could be read.
type Check struct {
	Name    string   `json:"name"`
	URL     string   `json:"url,omitempty"`
	Summary string   `json:"summary,omitempty"`
	Steps   []string `json:"steps,omitempty"`
}

// Budget is the API's GraphQL bucket, which the searches and the sweeps
// spend; Low says it refuses background reads.
type Budget struct {
	Low       bool      `json:"low"`
	Remaining int       `json:"remaining"`
	Limit     int       `json:"limit"`
	ResetAt   time.Time `json:"reset_at,omitzero"`
}

// presenting is what the views keep across calls.
type presenting struct {
	once     sync.Once
	explorer *explore.Explorer
	steps    stepCache
}

// explorer is the plugin's Explore over its client and store.
func (r *running) explorer() *explore.Explorer {
	r.views.once.Do(func() {
		r.views.explorer = &explore.Explorer{GitHub: r.client, Store: exploreStore{r.db}, Loop: r.loop,
			Budget: func() (bool, api.Limit) {
				return r.client.Allow(api.PriorityBackground), r.client.Budget()[api.ResourceGraphQL]
			}}
	})
	return r.views.explorer
}

// exploreStore is the store as Explore reads it.
type exploreStore struct{ *store.DB }

func (s exploreStore) Refs(ctx context.Context) ([]pr.Ref, error) {
	prs, err := s.ListPRs(ctx, false)
	refs := make([]pr.Ref, len(prs))
	for i, x := range prs {
		refs[i] = x.Ref
	}
	return refs, err
}

// View serves one of the plugin's views.
func (p *Plugin) View(ctx context.Context, req src.ViewRequest) (json.RawMessage, error) {
	r, err := p.running()
	if err != nil {
		return nil, err
	}
	switch req.View {
	case ViewExplore:
		var q ExploreQuery
		if err := decodeQuery(req.Query, &q); err != nil {
			return nil, err
		}
		return marshal(r.explorer().Search(ctx, q.Query))
	case ViewExploreRecent:
		return marshal(r.explorer().Recent(ctx))
	case ViewChecks:
		var q ChecksQuery
		if err := decodeQuery(req.Query, &q); err != nil {
			return nil, err
		}
		return marshal(r.checks(ctx, pr.Ref{Owner: q.Owner, Repo: q.Repo, Number: q.Number}))
	case ViewCandidate:
		var q ChecksQuery
		if err := decodeQuery(req.Query, &q); err != nil {
			return nil, err
		}
		viewer, err := r.db.Viewer(ctx)
		if err != nil {
			return nil, err
		}
		return marshal(r.client.CandidateByRef(api.WithPriority(ctx, api.PriorityBackground), pr.Ref{Owner: q.Owner, Repo: q.Repo, Number: q.Number}, viewer))
	case ViewBudget:
		l := r.client.Budget()[api.ResourceGraphQL]
		return json.Marshal(Budget{Low: !r.client.Allow(api.PriorityBackground), Remaining: l.Remaining, Limit: l.Limit, ResetAt: l.ResetAt})
	}
	return nil, fmt.Errorf("%w: no view %s", src.ErrUnsupported, req.View)
}

func decodeQuery(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("%w: invalid query: %v", src.ErrUnfit, err)
	}
	return nil
}

func marshal[T any](v T, err error) (json.RawMessage, error) {
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

var actionsJob = regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+)/actions/runs/\d+/job/(\d+)`)

const (
	stepJobsMax = 5
	// stepRetry is how long a job whose steps could not be read waits
	// before it is asked again: a missing scope or a deleted run stays so.
	stepRetry   = 10 * time.Minute
	stepTimeout = 10 * time.Second
	stepsMax    = 512
)

// checks names the failed steps of the GitHub Actions jobs among the
// failing checks of a pull request the store holds, asking GitHub for at
// most stepJobsMax jobs per call, in parallel.
func (r *running) checks(ctx context.Context, ref pr.Ref) ([]Check, error) {
	pull, found, err := r.db.PR(ctx, ref)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%w: %s is not in the inventory", src.ErrNotFound, ref)
	}
	sctx, cancel := context.WithTimeout(ctx, stepTimeout)
	defer cancel()
	out := make([]Check, len(pull.FailingChecks))
	var wg sync.WaitGroup
	asked := 0
	for i, c := range pull.FailingChecks {
		out[i] = Check{Name: c.Name, URL: c.URL, Summary: c.Summary}
		m := actionsJob.FindStringSubmatch(c.URL)
		if m == nil {
			continue
		}
		job, err := strconv.ParseInt(m[3], 10, 64)
		if err != nil {
			continue
		}
		if known, ok := r.views.steps.Load(job); ok {
			if known.failedAt.IsZero() {
				out[i].Steps = known.steps
				continue
			}
			if r.now().Sub(known.failedAt) < stepRetry {
				continue
			}
		}
		if asked == stepJobsMax {
			continue
		}
		asked++
		wg.Go(func() {
			steps, err := r.failedSteps(sctx, m[1], m[2], job)
			switch {
			case err == nil:
				out[i].Steps = steps
				r.views.steps.Store(job, stepsOf{steps: steps})
			case ctx.Err() == nil:
				r.views.steps.Store(job, stepsOf{failedAt: r.now()})
			}
		})
	}
	wg.Wait()
	return out, nil
}

// failedSteps reads the names of the failed steps of a check's job.
func (r *running) failedSteps(ctx context.Context, owner, repo string, job int64) ([]string, error) {
	resp, err := r.client.REST(api.WithPriority(ctx, api.PriorityUser), api.Request{Path: fmt.Sprintf("repos/%s/%s/actions/jobs/%d", owner, repo, job)})
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
		return nil, errors.Join(fmt.Errorf("job %d", job), err)
	}
	var steps []string
	for _, s := range body.Steps {
		if s.Conclusion == "failure" {
			steps = append(steps, pr.OneLine(s.Name, 120))
		}
	}
	return steps, nil
}

// stepsOf is what the plugin knows of one job's failed steps. A finished job
// does not change, so a read is kept until it is evicted.
type stepsOf struct {
	steps    []string
	failedAt time.Time
}

// stepCache holds the stepsOf of the stepsMax most recently used jobs.
type stepCache struct {
	mu    sync.Mutex
	order *list.List
	byJob map[int64]*list.Element
}

type stepEntry struct {
	job int64
	val stepsOf
}

func (c *stepCache) Load(job int64) (stepsOf, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.byJob[job]
	if !ok {
		return stepsOf{}, false
	}
	c.order.MoveToFront(el)
	return el.Value.(*stepEntry).val, true
}

func (c *stepCache) Store(job int64, v stepsOf) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.byJob == nil {
		c.order = list.New()
		c.byJob = map[int64]*list.Element{}
	}
	if el, ok := c.byJob[job]; ok {
		el.Value.(*stepEntry).val = v
		c.order.MoveToFront(el)
		return
	}
	c.byJob[job] = c.order.PushFront(&stepEntry{job: job, val: v})
	if c.order.Len() > stepsMax {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.byJob, oldest.Value.(*stepEntry).job)
	}
}

func (c *stepCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.byJob)
}
