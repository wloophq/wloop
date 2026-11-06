package task

import (
	"cmp"
	"slices"
	"time"
)

// The project verbs; position is an argument of VerbProject.
const (
	VerbReviewed   Verb = "reviewed"
	VerbSequential Verb = "sequential"
)

// DefaultReviewEvery is the review interval of a project that sets none.
const DefaultReviewEvery = 7 * 24 * time.Hour

func (e Task) ReviewInterval() time.Duration {
	if e.ReviewEvery > 0 {
		return e.ReviewEvery
	}
	return DefaultReviewEvery
}

// ReviewDue says whether project e's interval passed at now since its last review, or since it was created when
// it was never reviewed.
func (e Task) ReviewDue(now time.Time) bool {
	since := e.ReviewedAt
	if since.IsZero() {
		since = e.Created
	}
	return !since.Add(e.ReviewInterval()).After(now)
}

// Place puts e among its project's other actions at position pos (1 first; past the end, last) and numbers
// them all from 1 in that order.
func Place(others []Task, e Task, pos int) []Task {
	out := slices.DeleteFunc(slices.Clone(others), func(o Task) bool { return o.ID == e.ID })
	sortByPosition(out)
	at := min(max(pos-1, 0), len(out))
	out = slices.Insert(out, at, e)
	for i := range out {
		out[i].Position = i + 1
	}
	return out
}

func sortByPosition(es []Task) {
	slices.SortStableFunc(es, func(a, b Task) int { return cmp.Or(cmp.Compare(a.Position, b.Position), cmp.Compare(a.ID, b.ID)) })
}
