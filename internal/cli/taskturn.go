package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/turn"
)

var errTasksUnavailable = errors.New("the tasks are read in the daemon alone")

// taskShortID is a task's id cut to the first six hex digits after its prefix.
func taskShortID(id task.ID) string {
	s := strings.TrimPrefix(string(id), "t-")
	return s[:min(6, len(s))]
}

// nameFacts are the name facts of u's task, none for a unit of another
// provider or a task the daemon cannot read.
func (e *Env) nameFacts(ctx context.Context, u unit.Unit) map[string]any {
	id, ok := task.IDOf(u)
	if !ok || e.tasks == nil {
		return map[string]any{}
	}
	tk, err := e.tasks.Task(ctx, id)
	if err != nil {
		return map[string]any{}
	}
	return map[string]any{"task.title": tk.Title, "task.short_id": taskShortID(id)}
}

// taskPayload is a task as its turn reads it: the user's title and
// notes, the brief its record holds.
type taskPayload struct {
	task  task.Task
	brief string
}

func (p taskPayload) Unit() unit.Unit { return task.UnitOf(p.task.ID) }

func (p taskPayload) Arg() string { return p.Unit().String() }

func (p taskPayload) Wording() turn.Wording {
	return turn.Wording{Noun: "task", This: "this task", Ref: p.Unit().String(), Short: taskShortID(p.task.ID)}
}

func (p taskPayload) Fact(string) (any, bool) { return nil, false }

func (p taskPayload) Contents() string {
	out := p.task.Title
	for _, part := range []string{p.task.Notes, p.brief} {
		if strings.TrimSpace(part) != "" && part != p.task.Title {
			out += "\n\n" + part
		}
	}
	return out
}

// taskPayloads reads a task's turn payload from the daemon's tasks.
type taskPayloads struct{ e *Env }

func (r taskPayloads) Payload(ctx context.Context, req turn.Request) (turn.Payload, error) {
	id, _ := task.IDOf(req.Unit)
	if r.e.tasks == nil {
		return nil, errTasksUnavailable
	}
	tk, err := r.e.tasks.Task(ctx, id)
	if err != nil {
		return nil, err
	}
	brief, err := r.e.taskBrief(ctx, tk.ID)
	if err != nil {
		return nil, err
	}
	return taskPayload{task: tk, brief: brief}, nil
}

// taskBrief is the markdown brief task id's own brief record holds; "" for none, or outside a store.
func (e *Env) taskBrief(ctx context.Context, id task.ID) (string, error) {
	if e.Store == nil {
		return "", nil
	}
	u := task.UnitOf(id)
	recs, err := e.Store.DB().Records(ctx, builtin.BuildsBrief, u)
	if err != nil {
		return "", err
	}
	rec, ok := recs[u]
	if !ok {
		return "", nil
	}
	var data map[string]any
	if json.Unmarshal([]byte(rec.Data), &data) != nil {
		return "", nil
	}
	text, _ := data[builtin.BriefText].(string)
	return text, nil
}

// PrepareAhead readies, before the round takes the loop mutex, the
// repository an intent's turn runs in when no manager knows it: cloned
// and, when its project's trust_clones says so, trusted, as the round
// would otherwise do under the mutex.
func (a envActions) PrepareAhead(ctx context.Context, name string, u unit.Unit, c daemon.Call) error {
	e := a.call(c)
	d, err := e.intentDef(name)
	if err != nil || d.WorkspaceRepo == nil {
		return err
	}
	cfg, err := e.loadConfig()
	if err != nil {
		return err
	}
	if cfg.AgentHome == "" {
		cfg.AgentHome = c.AgentHome
	}
	inputs, err := e.keptRepo(ctx, d, u, c.Inputs)
	if err != nil {
		return err
	}
	run := unitRun{def: d, cfg: cfg, r: turn.Request{Inputs: inputs}, manager: c.Manager}
	repo, err := runRepo(run)
	if err != nil || repo == "" {
		return err
	}
	cfg = scoped(run)
	if _, err := e.chooseManager(ctx, cfg, intentOptions(d, roundOptions{unit: u})); err != nil {
		return err
	}
	_, err = e.repoCheckout(ctx, cfg, repo)
	return err
}
