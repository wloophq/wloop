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

type ProjectStatus struct {
	// Next is the project's next action: the open action with the lowest Position that is ready; of a sequential
	// project, its first open action when that one is ready. "" none.
	Next      ID
	NextTitle string
	Open      int // its open actions
}

// ProjectsOf is the status of each project in all over the actions in all.
func ProjectsOf(all []Task) map[ID]ProjectStatus {
	out := map[ID]ProjectStatus{}
	seq := map[ID]bool{}
	for _, e := range all {
		if e.Kind == KindProject {
			out[e.ID] = ProjectStatus{}
			seq[e.ID] = e.Sequential
		}
	}
	acts := map[ID][]Task{}
	for _, e := range all {
		if _, ok := out[e.Parent]; ok && e.Open() {
			acts[e.Parent] = append(acts[e.Parent], e)
		}
	}
	for p, es := range acts {
		sortByPosition(es)
		st := ProjectStatus{Open: len(es)}
		for _, e := range es {
			if e.Category == CategoryReady {
				st.Next, st.NextTitle = e.ID, e.Title
				break
			}
			if seq[p] {
				break
			}
		}
		out[p] = st
	}
	return out
}

// NeedsReview says whether the Review lists project p: open, and due or, unless in Someday, without a next action.
func NeedsReview(p Task, st ProjectStatus, now time.Time) bool {
	if p.Kind != KindProject || !p.Open() {
		return false
	}
	return p.ReviewDue(now) || (st.Next == "" && !inSomeday(p))
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
