package tasks

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/domain/task"
)

// Review is the review as it stands: its steps in order, each with its count. ID is empty while nothing was
// left to review when it started, so nothing was recorded.
type Review struct {
	ID        string
	StartedAt time.Time
	Steps     []ReviewStep
	Done      bool
}

// ReviewStep is a step of the review: the view it walks, how many it lists, and whether it is done.
type ReviewStep struct {
	View  task.View
	Count int
	Done  bool
}

// ServesReview says whether the review is served: ServeProjects gave it a store for its runs.
func (s *Service) ServesReview() bool {
	s.reg.RLock()
	defer s.reg.RUnlock()
	return s.runs != nil
}

func (s *Service) reviewRuns() (ProjectStore, error) {
	s.reg.RLock()
	defer s.reg.RUnlock()
	if s.runs == nil {
		return nil, fmt.Errorf("%w: the review is not served", ErrArgs)
	}
	return s.runs, nil
}

// Review is the current review, or a new one: a step whose count is 0 at its start starts done, and a new
// review is recorded once a step is left to do.
func (s *Service) Review(ctx context.Context) (Review, error) {
	runs, err := s.reviewRuns()
	if err != nil {
		return Review{}, err
	}
	s.review.Lock()
	defer s.review.Unlock()
	steps, counts, err := s.reviewCounts(ctx)
	if err != nil {
		return Review{}, err
	}
	run, err := s.currentRun(ctx, runs, steps, counts, false)
	if err != nil {
		return Review{}, err
	}
	return reviewOf(run, steps, counts), nil
}

// ReviewStepDone marks step, the view it walks, done in the current review, recording the review once every
// step is.
func (s *Service) ReviewStepDone(ctx context.Context, step string) (Review, error) {
	runs, err := s.reviewRuns()
	if err != nil {
		return Review{}, err
	}
	s.review.Lock()
	defer s.review.Unlock()
	steps, counts, err := s.reviewCounts(ctx)
	if err != nil {
		return Review{}, err
	}
	if !slices.Contains(steps, task.View(step)) {
		return Review{}, fmt.Errorf("%w: no review step %q", ErrArgs, step)
	}
	run, err := s.currentRun(ctx, runs, steps, counts, true)
	if err != nil {
		return Review{}, err
	}
	if !slices.Contains(run.Done, step) {
		run.Done = append(run.Done, step)
	}
	if allDone(run, steps) {
		run.FinishedAt = s.now()
	}
	if err := runs.PutRun(ctx, run); err != nil {
		return Review{}, err
	}
	return reviewOf(run, steps, counts), nil
}

// reviewCounts are the review's steps and each step's count over every area.
func (s *Service) reviewCounts(ctx context.Context) ([]task.View, map[task.View]int, error) {
	steps := s.profileViews().Review
	c, err := s.Counts(ctx, "")
	if err != nil {
		return nil, nil, err
	}
	return steps, c.Views, nil
}

// currentRun is the stored run with a step still open, or a new one whose empty steps start done; a new run is
// put when one of its steps is open, or when keep.
func (s *Service) currentRun(ctx context.Context, runs ProjectStore, steps []task.View, counts map[task.View]int, keep bool) (task.ReviewRun, error) {
	run, ok, err := runs.CurrentRun(ctx)
	if err != nil || ok {
		return run, err
	}
	run = task.ReviewRun{StartedAt: s.now()}
	for _, st := range steps {
		if counts[st] == 0 {
			run.Done = append(run.Done, string(st))
		}
	}
	if allDone(run, steps) && !keep {
		return run, nil
	}
	run.ID = newRunID()
	return run, runs.PutRun(ctx, run)
}

func allDone(run task.ReviewRun, steps []task.View) bool {
	return !slices.ContainsFunc(steps, func(st task.View) bool { return !slices.Contains(run.Done, string(st)) })
}

func reviewOf(run task.ReviewRun, steps []task.View, counts map[task.View]int) Review {
	rv := Review{ID: run.ID, StartedAt: run.StartedAt, Done: true}
	for _, st := range steps {
		done := slices.Contains(run.Done, string(st))
		rv.Steps = append(rv.Steps, ReviewStep{View: st, Count: counts[st], Done: done})
		rv.Done = rv.Done && done
	}
	return rv
}

func newRunID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "r-" + hex.EncodeToString(b[:])
}
