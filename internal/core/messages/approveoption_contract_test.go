package messages_test

import (
	"context"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/task"
)

// The gates call an option that points to a draft makes, compiled and never run.
var _ = func(ctx context.Context, s *gates.Service, question, drafted gate.Gate, o record.Option) error {
	if o.Draft == nil {
		return nil
	}
	return s.ApproveOption(ctx, question, drafted, o.Draft.Rev, gate.Resolver{By: task.ActorPerson})
}
