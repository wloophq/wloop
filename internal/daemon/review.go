package daemon

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/wloophq/wloop/internal/api"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
)

func (s *Server) handleReview(w http.ResponseWriter, r *http.Request) {
	g, ok := s.tasks(w)
	if !ok {
		return
	}
	if !g.Serves(task.ViewReview) {
		writeError(w, http.StatusNotFound, "the review is not served")
		return
	}
	rv, err := g.Review(r.Context())
	if err != nil {
		writeTasksError(w, err)
		return
	}
	writeJSON(w, s.wireReview(rv))
}

func (s *Server) handleReviewStepDone(w http.ResponseWriter, r *http.Request) {
	g, ok := s.tasks(w)
	if !ok {
		return
	}
	if !g.Serves(task.ViewReview) {
		writeError(w, http.StatusNotFound, "the review is not served")
		return
	}
	rv, err := g.ReviewStepDone(context.WithoutCancel(r.Context()), r.PathValue("step"))
	if err != nil {
		writeTasksError(w, err)
		return
	}
	writeJSON(w, s.wireReview(rv))
}

func (s *Server) wireReview(rv coretasks.Review) api.Review {
	texts, bundled := s.loaded().Lists, bundledWords().lists
	out := api.Review{ID: rv.ID, StartedAt: rv.StartedAt, Done: rv.Done, Steps: []api.ReviewStep{}}
	for _, st := range rv.Steps {
		label := cmp.Or(texts.Review[st.ID], bundled.Review[st.ID], st.ID)
		out.Steps = append(out.Steps, api.ReviewStep{ID: st.ID, Label: label, View: string(st.View),
			Query: map[string]string{"view": string(st.View)}, Count: st.Count, Done: st.Done})
	}
	return out
}

// project sets a project's next action, open actions and review on v, reading every project's status once per
// viewer. The verbs a handler takes on a project, and project on any task, are offered only where the task's
// state takes them.
func (ev *taskViewer) project(ctx context.Context, e task.Task, v *api.TaskView) error {
	for i, st := range v.Verbs {
		verb := task.Verb(st.Verb)
		if !st.Enabled || !ev.g.Handles(e.Kind, verb) {
			continue
		}
		var err error
		switch {
		case verb == task.VerbProject && e.Kind == task.KindProject:
			err = errors.New("a project cannot join a project")
		case verb == task.VerbProject, e.Kind == task.KindProject && slices.Contains([]task.Verb{task.VerbDone, task.VerbDismiss, task.VerbReference}, verb):
			err = ev.g.Accepts(e, verb)
		case e.Kind == task.KindProject && (verb == task.VerbReviewed || verb == task.VerbSequential) && !e.Open():
			err = fmt.Errorf("%s on a %s project", verb, e.State)
		}
		if err != nil {
			v.Verbs[i] = api.VerbState{Verb: st.Verb, Reason: err.Error()}
		}
	}
	if e.Kind != task.KindProject {
		return nil
	}
	if ev.status == nil {
		st, err := ev.g.ProjectsStatus(ctx)
		if err != nil {
			return err
		}
		ev.status = st
	}
	st := ev.status[e.ID]
	v.Sequential, v.NextAction, v.NextActionTitle, v.OpenActions = e.Sequential, string(st.Next), st.NextTitle, st.Open
	v.ReviewEvery, v.ReviewedAt, v.ReviewDue = e.ReviewInterval().String(), e.ReviewedAt, st.Due
	return nil
}
