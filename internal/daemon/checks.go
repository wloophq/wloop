package daemon

import (
	"container/list"
	"context"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/api"
)

var actionsJob = regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+)/actions/runs/\d+/job/(\d+)`)

const (
	stepJobsMax = 5
	// stepRetry is how long a job whose steps could not be read waits
	// before it is asked again: a missing scope or a deleted run stays so.
	stepRetry   = 10 * time.Minute
	stepTimeout = 10 * time.Second
	stepsMax    = 512
)

// stepsOf is what the daemon knows of one job's failed steps. A finished job
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

// handleChecks names the failed steps of the GitHub Actions jobs among the
// failing checks, asking GitHub for at most stepJobsMax jobs per request,
// in parallel.
func (s *Server) handleChecks(w http.ResponseWriter, r *http.Request) {
	u, ok := s.parsePR(r)
	if !ok {
		writeNoUnit(w, r)
		return
	}
	pr, found, err := s.deps.PRs.PR(r.Context(), u)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, u.Label()+" is not in the inventory")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), stepTimeout)
	defer cancel()
	out := make([]api.CheckDetail, len(pr.FailingChecks))
	var wg sync.WaitGroup
	asked := 0
	for i, c := range pr.FailingChecks {
		out[i] = api.CheckDetail{Name: c.Name, URL: c.URL, Summary: c.Summary}
		m := actionsJob.FindStringSubmatch(c.URL)
		if m == nil || s.deps.FailedSteps == nil {
			continue
		}
		job, err := strconv.ParseInt(m[3], 10, 64)
		if err != nil {
			continue
		}
		if known, ok := s.steps.Load(job); ok {
			if known.failedAt.IsZero() {
				out[i].Steps = known.steps
				continue
			}
			if s.deps.Now().Sub(known.failedAt) < stepRetry {
				continue
			}
		}
		if asked == stepJobsMax {
			continue
		}
		asked++
		wg.Go(func() {
			steps, err := s.deps.FailedSteps(ctx, m[1], m[2], job)
			switch {
			case err == nil:
				out[i].Steps = steps
				s.steps.Store(job, stepsOf{steps: steps})
			case r.Context().Err() == nil:
				s.logf("%s: failed steps of job %d: %v", u.Label(), job, err)
				s.steps.Store(job, stepsOf{failedAt: s.deps.Now()})
			}
		})
	}
	wg.Wait()
	writeJSON(w, out)
}
