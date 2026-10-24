package explore

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	dgh "github.com/wloophq/wloop/internal/domain/github"
	"github.com/wloophq/wloop/internal/github"
)

// Searcher is the GitHub search Explore runs.
type Searcher interface {
	SearchCandidates(ctx context.Context, q, viewer string, first int) (dgh.CandidatePage, error)
	SearchCandidatesEach(ctx context.Context, qs []string, viewer string, first int) ([]dgh.CandidatePage, []error, error)
	CandidateByRef(ctx context.Context, ref github.Ref, viewer string) (dgh.CandidatePage, error)
}

// Retry bounds each attempt of a search and pauses before its second try.
type Retry struct {
	Attempt, Pause time.Duration
}

// Search gives a search GitHub could not finish in time one more try after
// a pause: a gateway error or a timeout often passes.
func (r Retry) Search(ctx context.Context, s Searcher, q, viewer string) (dgh.CandidatePage, error) {
	var page dgh.CandidatePage
	err := r.twice(ctx, func(actx context.Context) (err error) {
		page, err = s.SearchCandidates(actx, q, viewer, MaxResults)
		return err
	})
	return page, err
}

func (r Retry) twice(ctx context.Context, search func(context.Context) error) error {
	var err error
	for try := range 2 {
		if try > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(r.Pause):
			}
		}
		actx, cancel := context.WithTimeout(ctx, r.Attempt)
		err = search(actx)
		timedOut := errors.Is(actx.Err(), context.DeadlineExceeded) && ctx.Err() == nil
		cancel()
		if err == nil {
			return nil
		}
		if !timedOut && !github.Transient(err) {
			return err
		}
	}
	return fmt.Errorf("%w; GitHub failed twice, narrow the search, for example with repo:owner/name or org:owner", err)
}

// AuthorInRepos runs the searches SearchesPerRequest to a request and
// merges them in the order sort asks for; failed holds the searches that
// failed alone, and only all of them failing fails the merge.
func (r Retry) AuthorInRepos(ctx context.Context, s Searcher, queries []string, sort, viewer string) (merged dgh.CandidatePage, failed []error, err error) {
	seen := map[github.Ref]bool{}
	for chunk := range slices.Chunk(queries, github.SearchesPerRequest) {
		var pages []dgh.CandidatePage
		var errs []error
		err := r.twice(ctx, func(actx context.Context) (err error) {
			pages, errs, err = s.SearchCandidatesEach(actx, chunk, viewer, MaxResults)
			return err
		})
		if err != nil {
			return dgh.CandidatePage{}, nil, err
		}
		for i, page := range pages {
			if i == 0 {
				merged.RateLimit, merged.RateLimit.Cost = page.RateLimit, merged.RateLimit.Cost+page.RateLimit.Cost
			}
			if errs[i] != nil {
				failed = append(failed, errs[i])
				continue
			}
			merged.Total += page.Total
			for _, c := range page.Candidates {
				if !seen[c.Ref] {
					seen[c.Ref] = true
					merged.Candidates = append(merged.Candidates, c)
				}
			}
		}
	}
	if len(failed) == len(queries) {
		return dgh.CandidatePage{}, nil, failed[0]
	}
	if cmp := Order(sort); cmp != nil {
		slices.SortStableFunc(merged.Candidates, cmp)
	}
	merged.Candidates = merged.Candidates[:min(len(merged.Candidates), MaxResults)]
	return merged, failed, nil
}
