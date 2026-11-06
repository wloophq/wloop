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

// merge folds e and the open task req.Args[task.ArgTask] names into one, under the service's lock: the
// one Survives holds on alone stays, else the older, and the other is merged into it (merge.Fold) through
// the Store's Merger, after the gates acting for it (GateSweeper.ActingFor) are superseded. Both ids are
// compared as they read, aliases followed: a task merged into e already is e. It refuses e itself, a closed
// other task, a task that is another's parent, and a store with no Merger or gates with no GateSweeper.
func (s *Service) merge(ctx context.Context, e task.Task, req Request) (Result, error) {
	panic("not implemented: T1")
}

// split gives back, under the service's lock, the task req.Args[task.ArgTask] names, one whose merge
// record (Merger.MergeOf) says it was merged into e, as merge.Unfold says, supersedes the open merge and
// relate proposals naming both, then syncs its units. It refuses a task whose record names another, which
// covers a task merged into one merged elsewhere since: that one is split first.
func (s *Service) split(ctx context.Context, e task.Task, req Request) (Result, error) {
	panic("not implemented: T1")
}
