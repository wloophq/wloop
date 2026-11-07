package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/omnibox"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// OmniboxAsker runs the omnibox's read-only agent call over a snapshot of the read model.
type OmniboxAsker interface {
	Ask(ctx context.Context, in omnibox.AskRequest, snap omnibox.Snapshot) (omnibox.AskReply, error)
}

// handleOmniboxAsk runs the read-only agent call on the typed text and serves its answer or proposal; it
// stores nothing but the call's cost. A client that goes away cancels the call.
func (s *Server) handleOmniboxAsk(w http.ResponseWriter, r *http.Request) {
	if s.deps.OmniboxAsk == nil {
		writeError(w, http.StatusServiceUnavailable, s.word(api.OmniboxAskUnwired, nil))
		return
	}
	var in api.OmniboxText
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	g, ok := s.tasks(w)
	if !ok {
		return
	}
	snap, views, err := s.omniboxSnapshot(r.Context(), g)
	if err != nil {
		writeTasksError(w, err)
		return
	}
	req := omnibox.AskRequest{Text: in.Text}
	for _, f := range in.Follow {
		req.Follow = append(req.Follow, omnibox.AskFollowUp{Question: f.Question, Answer: f.Answer})
	}
	got, err := s.deps.OmniboxAsk.Ask(r.Context(), req, snap)
	switch {
	case errors.Is(err, omnibox.ErrNoText):
		writeError(w, http.StatusBadRequest, s.word(api.OmniboxAskEmpty, nil))
	case errors.Is(err, turns.ErrOverCap):
		writeError(w, http.StatusTooManyRequests, s.word(api.OmniboxAskOverCap, nil))
	case errors.Is(err, turns.ErrNoRestricted), errors.Is(err, turns.ErrNoAgent):
		writeError(w, http.StatusServiceUnavailable, s.word(api.OmniboxAskNoAgent, map[string]any{"error": err.Error()}))
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, s.word(api.OmniboxAskTimedOut, nil))
	case err != nil:
		writeError(w, http.StatusBadGateway, s.word(api.OmniboxAskFailed, map[string]any{"error": err.Error()}))
	default:
		writeJSON(w, omniboxAnswer(got, views))
	}
}

// omniboxSnapshot is what the omnibox call reads, the open tasks without their verbs and actions and the
// views, and those tasks as an answer lists them, by id.
func (s *Server) omniboxSnapshot(ctx context.Context, g *coretasks.Service) (omnibox.Snapshot, map[string]api.TaskView, error) {
	es, err := g.Match(ctx, "", "")
	if err != nil {
		return omnibox.Snapshot{}, nil, err
	}
	vs := s.taskViews(ctx, g, false)
	vs.readRows(ctx, es)
	props := s.loaded().Tasks
	snap := omnibox.Snapshot{Tasks: make([]omnibox.SnapshotTask, 0, len(es))}
	views := make(map[string]api.TaskView, len(es))
	for _, e := range es {
		units := append([]unit.Unit{}, e.Units...)
		t := omnibox.SnapshotTask{ID: string(e.ID), State: string(e.State), Category: string(e.Category), Title: e.Title,
			Notes: e.Notes, Parent: string(e.Parent), Props: propsView(props, e.Props), Units: units,
			UnitLabels: vs.unitLabels(ctx, units), Created: e.Created, Updated: e.Updated}
		snap.Tasks = append(snap.Tasks, t)
		views[t.ID] = api.TaskView{ID: t.ID, State: t.State, Category: t.Category, Title: t.Title, Notes: t.Notes, Parent: t.Parent,
			Props: t.Props, Units: t.Units, UnitLabels: t.UnitLabels, Created: t.Created, Updated: t.Updated}
	}
	for _, v := range s.viewsCatalog().Views {
		snap.Views = append(snap.Views, omnibox.SnapshotView{ID: v.ID, Label: v.Label, When: v.When})
	}
	return snap, views, nil
}

func omniboxAnswer(got omnibox.AskReply, views map[string]api.TaskView) api.OmniboxAnswer {
	out := api.OmniboxAnswer{CostUSD: got.CostUSD}
	if got.List != nil {
		out.Answer = &api.OmniboxList{Text: got.List.Text, Tasks: make([]api.TaskView, 0, len(got.List.Tasks))}
		for _, id := range got.List.Tasks {
			out.Answer.Tasks = append(out.Answer.Tasks, views[id])
		}
	}
	if p := got.Proposal; p != nil {
		out.Proposal = &api.OmniboxProposal{Summary: p.Summary, Question: p.Question, Operations: make([]api.OmniboxOperation, 0, len(p.Operations))}
		for _, op := range p.Operations {
			out.Proposal.Operations = append(out.Proposal.Operations, omniboxOperation(op))
		}
	}
	for _, d := range got.Dropped {
		out.Dropped = append(out.Dropped, api.OmniboxDropped{Operation: omniboxOperation(d.Op), Why: d.Why, Params: d.Params})
	}
	return out
}

func omniboxOperation(op omnibox.AskOp) api.OmniboxOperation {
	return api.OmniboxOperation{Op: op.Op, Unit: op.Unit, Task: op.Task, Action: op.Action, Verb: op.Verb, Title: op.Title,
		Brief: op.Brief, Repo: op.Repo, Guidance: op.Guidance, Reason: op.Reason}
}
