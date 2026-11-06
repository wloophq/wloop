package tasks

import (
	"context"

	"github.com/wloophq/wloop/internal/domain/task"
)

// The handlers that fold a task into another, give it back, and link two tasks: each takes the other task in
// the argument task.ArgTask.
const (
	HandlerMerge    = "merge"
	HandlerSplit    = "split"
	HandlerRelate   = "relate"
	HandlerUnrelate = "unrelate"
)

func (s *Service) serveMerges() {
	s.Register(EveryKind, HandlerMerge, s.merge)
	s.Register(EveryKind, HandlerSplit, s.split)
	s.Register(EveryKind, HandlerRelate, s.relate)
	s.Register(EveryKind, HandlerUnrelate, s.unrelate)
}

// merge folds e and the open task req.Args[task.ArgTask] names into one: the one Survives holds on alone
// stays, else the older, and the other is merged into it (merge.Fold) through the Store's Merger. It refuses a
// task that is another's parent, the same task twice, and a store with no Merger.
func (s *Service) merge(ctx context.Context, e task.Task, req Request) (Result, error) {
	panic("not implemented: T1")
}

// split gives back the task req.Args[task.ArgTask] names, one merged into e, as its merge record keeps it
// (merge.Unfold); it refuses a task e's property of role merged does not list, and one whose merge into e
// was followed by e's own merge elsewhere until that one is split.
func (s *Service) split(ctx context.Context, e task.Task, req Request) (Result, error) {
	panic("not implemented: T1")
}

// relate adds each of e and the task req.Args[task.ArgTask] names to the other's property of role related, in
// one Apply; relating two related tasks changes nothing.
func (s *Service) relate(ctx context.Context, e task.Task, req Request) (Result, error) {
	panic("not implemented: T2")
}

// unrelate takes each of e and the task req.Args[task.ArgTask] names off the other's property of role
// related, in one Apply.
func (s *Service) unrelate(ctx context.Context, e task.Task, req Request) (Result, error) {
	panic("not implemented: T2")
}
