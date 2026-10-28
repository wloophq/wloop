package cli

import (
	"context"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/timeline"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// chatLog is the tasks' chat over the store: the event log, the marks of what the agents read, and the
// tasks' briefs.
type chatLog struct{ db *sqlite.DB }

var _ messages.TaskLog = chatLog{}

func (c chatLog) RecordEvents(ctx context.Context, evs ...port.Event) error {
	return sqlite.New(c.db).RecordEvents(ctx, evs...)
}

func (c chatLog) Of(ctx context.Context, id task.ID, f timeline.Filter) ([]timeline.Event, error) {
	return timeline.Merged{Log: c.db, Holdings: c.db}.Of(ctx, id, f)
}

func (c chatLog) Marks(ctx context.Context, u unit.Unit) (map[string]string, error) {
	return c.db.Marks(ctx, u)
}

func (c chatLog) SetMarks(ctx context.Context, u unit.Unit, marks map[string]string) error {
	return c.db.SetMarks(ctx, u, marks)
}

func (c chatLog) Brief(ctx context.Context, e task.Task) (string, bool, error) {
	rec, ok, err := timeline.RecordOf(ctx, c.db, builtin.BuildsBrief, e.ID, e.Units)
	return rec.Data, ok, err
}

// chatIntent is the loaded profile's chat intent.
func (e *Env) chatIntent() string {
	if l := e.loadedIntents(); l != nil {
		return l.Tasks.ChatIntent
	}
	return ""
}
