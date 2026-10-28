package tasks

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/domain/task"
)

// Review is the weekly Review as it stands: its steps in order, each with its count. ID is empty while
// nothing was left to review when it started, so nothing was recorded.
type Review struct {
	ID        string
	StartedAt time.Time
	Steps     []ReviewStep
	Done      bool
}

type ReviewStep struct {
	task.ReviewStep
	Count int
	Done  bool
}

// reviewSource serves the review view, the projects the Review's projects step lists, and keeps the runs.
type reviewSource struct {
	s     *Service
	store ProjectStore
	mu    sync.Mutex
}

func (r *reviewSource) Entries(ctx context.Context, q task.Query) ([]task.Entry, error) {
	open, err := r.s.d.Store.Entries(ctx, task.Query{View: task.ViewOpen})
	if err != nil {
		return nil, err
	}
	st, now := task.ProjectsOf(open), r.s.now()
	var out []task.Entry
	for _, e := range open {
		if task.NeedsReview(e, st[e.ID], now) && q.Matches(e) {
			out = append(out, e)
		}
	}
	task.Sort(out, task.ViewProjects)
	return out, nil
}

func (r *reviewSource) Entry(ctx context.Context, id task.ID) (task.Entry, bool, error) {
	return task.Entry{}, false, nil
}

func (r *reviewSource) Count(ctx context.Context, area string) (int, error) {
	es, err := r.Entries(ctx, task.Query{View: task.ViewReview, Area: area})
	return len(es), err
}

func (s *Service) review() (*reviewSource, error) {
	src, _ := s.view(task.ViewReview)
	r, ok := src.(*reviewSource)
	if !ok {
		return nil, fmt.Errorf("%w: the review is not served", ErrArgs)
	}
	return r, nil
}

// Review is the current Review, or a new one: a step whose count is 0 at its start starts done, and a new
// Review is recorded once a step is left to do.
func (s *Service) Review(ctx context.Context) (Review, error) {
	r, err := s.review()
	if err != nil {
		return Review{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	counts, err := r.counts(ctx)
	if err != nil {
		return Review{}, err
	}
	run, err := r.current(ctx, counts, false)
	if err != nil {
		return Review{}, err
	}
	return reviewOf(run, counts), nil
}

// ReviewStepDone marks step done in the current Review, recording the Review once every step is.
func (s *Service) ReviewStepDone(ctx context.Context, step string) (Review, error) {
	r, err := s.review()
	if err != nil {
		return Review{}, err
	}
	if !slices.ContainsFunc(task.ReviewSteps, func(x task.ReviewStep) bool { return x.ID == step }) {
		return Review{}, fmt.Errorf("%w: no review step %q", ErrArgs, step)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	counts, err := r.counts(ctx)
	if err != nil {
		return Review{}, err
	}
	run, err := r.current(ctx, counts, true)
	if err != nil {
		return Review{}, err
	}
	if !slices.Contains(run.Done, step) {
		run.Done = append(run.Done, step)
	}
	if len(run.Done) == len(task.ReviewSteps) {
		run.FinishedAt = s.now()
	}
	if err := r.store.PutRun(ctx, run); err != nil {
		return Review{}, err
	}
	return reviewOf(run, counts), nil
}

// current is the stored run with a step open, or a new one whose empty steps start done; a new run is put
// when one of its steps is open, or when keep.
func (r *reviewSource) current(ctx context.Context, counts map[string]int, keep bool) (task.ReviewRun, error) {
	run, ok, err := r.store.CurrentRun(ctx)
	if err != nil || ok {
		return run, err
	}
	run = task.ReviewRun{StartedAt: r.s.now()}
	for _, st := range task.ReviewSteps {
		if counts[st.ID] == 0 {
			run.Done = append(run.Done, st.ID)
		}
	}
	if len(run.Done) == len(task.ReviewSteps) && !keep {
		return run, nil
	}
	run.ID = newRunID()
	return run, r.store.PutRun(ctx, run)
}

// counts are each step's count over every area.
func (r *reviewSource) counts(ctx context.Context) (map[string]int, error) {
	c, err := r.s.Counts(ctx, "")
	if err != nil {
		return nil, err
	}
	projects, err := r.Count(ctx, "")
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	c.Views[task.ViewReview] = projects
	for _, st := range task.ReviewSteps {
		out[st.ID] = c.Views[st.View]
	}
	return out, nil
}

func reviewOf(run task.ReviewRun, counts map[string]int) Review {
	rv := Review{ID: run.ID, StartedAt: run.StartedAt, Done: true}
	for _, st := range task.ReviewSteps {
		done := slices.Contains(run.Done, st.ID)
		rv.Steps = append(rv.Steps, ReviewStep{ReviewStep: st, Count: counts[st.ID], Done: done})
		rv.Done = rv.Done && done
	}
	return rv
}

func newRunID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "r-" + hex.EncodeToString(b[:])
}
