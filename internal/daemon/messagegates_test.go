package daemon

import (
	"context"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/gates/gatestest"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// serveMessageGates gives s's messages their gates in memory, worded by the bundled gates.toml and resolved
// with s's consequences, each on the task of its unit in store, or a new one tasks opens.
func serveMessageGates(s *Server, store coretasks.Reader, tasks *coretasks.Service) {
	st := gatestest.NewStore(s.deps.Now)
	svc := gates.New(gates.Deps{Store: st, Reader: st, Profile: profileGates{s.loaded().Gates}, Tasks: rigTasks{store, tasks}, Clock: fixedClock(s.deps.Now()),
		Deliverer: s, Decider: s})
	s.deps.Gates, s.deps.GateReader = svc, st
	s.deps.Messages.Gates = rigGates{svc, st}
}

type rigTasks struct {
	store coretasks.Reader
	svc   *coretasks.Service
}

func (r rigTasks) TasksOf(ctx context.Context, u unit.Unit) ([]task.Task, error) {
	return r.store.TasksOf(ctx, u)
}

func (r rigTasks) Open(ctx context.Context, req coretasks.OpenRequest) (task.Task, error) {
	return r.svc.Open(ctx, req)
}
