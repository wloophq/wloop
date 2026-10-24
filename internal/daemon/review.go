package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/wloophq/wloop/internal/api"
	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/domain/gtd"
)

func (s *Server) handleReview(w http.ResponseWriter, r *http.Request) {
	g, ok := s.gtd(w)
	if !ok {
		return
	}
	if !g.Serves(gtd.ViewReview) {
		writeError(w, http.StatusNotFound, "the review is not served")
		return
	}
	rv, err := g.Review(r.Context())
	if err != nil {
		writeGTDError(w, err)
		return
	}
	writeJSON(w, wireReview(rv))
}

func (s *Server) handleReviewStepDone(w http.ResponseWriter, r *http.Request) {
	g, ok := s.gtd(w)
	if !ok {
		return
	}
	if !g.Serves(gtd.ViewReview) {
		writeError(w, http.StatusNotFound, "the review is not served")
		return
	}
	rv, err := g.ReviewStepDone(context.WithoutCancel(r.Context()), r.PathValue("step"))
	if err != nil {
		writeGTDError(w, err)
		return
	}
	writeJSON(w, wireReview(rv))
}

func wireReview(rv coregtd.Review) api.Review {
	out := api.Review{ID: rv.ID, StartedAt: rv.StartedAt, Done: rv.Done, Steps: []api.ReviewStep{}}
	for _, st := range rv.Steps {
		out.Steps = append(out.Steps, api.ReviewStep{ID: st.ID, Label: st.Label, View: string(st.View),
			Query: map[string]string{"view": string(st.View)}, Count: st.Count, Done: st.Done})
	}
	return out
}

// project sets a project's next action, open actions and review on v, reading every project's status once per
// viewer. The verbs a handler takes on a project, and project on any entry, are offered only where the entry's
// state takes them.
func (ev *entryViewer) project(ctx context.Context, e gtd.Entry, v *api.EntryView) error {
	for i, st := range v.Verbs {
		verb := gtd.Verb(st.Verb)
		if !st.Enabled || !ev.g.Handles(e.Kind, verb) {
			continue
		}
		var err error
		switch {
		case verb == gtd.VerbProject && e.Kind == gtd.KindProject:
			err = errors.New("a project cannot join a project")
		case verb == gtd.VerbProject, e.Kind == gtd.KindProject && slices.Contains([]gtd.Verb{gtd.VerbDone, gtd.VerbDismiss, gtd.VerbReference}, verb):
			err = coregtd.Accepts(e, verb)
		case e.Kind == gtd.KindProject && (verb == gtd.VerbReviewed || verb == gtd.VerbSequential) && !e.Live():
			err = fmt.Errorf("%s on a %s project", verb, e.State)
		}
		if err != nil {
			v.Verbs[i] = api.VerbState{Verb: st.Verb, Reason: err.Error()}
		}
	}
	if e.Kind != gtd.KindProject {
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
