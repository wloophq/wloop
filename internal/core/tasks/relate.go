package tasks

import (
	"context"

	"github.com/wloophq/wloop/internal/domain/task"
)

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
