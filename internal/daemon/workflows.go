package daemon

import (
	"net/http"
	"slices"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// handleWorkflowDisable turns a loaded workflow off in the configuration, applied at once; its held drafts
// stay in the Outbox.
func (s *Server) handleWorkflowDisable(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, ok := s.forUser(w, r, "stopping a workflow"); !ok {
		return
	}
	if !s.workflowLoaded(name) {
		writeError(w, http.StatusNotFound, s.notice(builtin.NoticeWorkflowUnknown, map[string]any{"Workflow": name}))
		return
	}
	if s.deps.ConfigSet == nil {
		writeError(w, http.StatusServiceUnavailable, "this daemon changes no configuration")
		return
	}
	if err := s.deps.ConfigSet([]string{"workflows", name, "enabled"}, false); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, api.WorkflowSwitch{Workflow: name, Enabled: false})
}

// workflowLoaded says whether the profile loads a workflow named name.
func (s *Server) workflowLoaded(name string) bool {
	return slices.ContainsFunc(s.loaded().Workflows, func(wf workflow.Workflow) bool { return wf.Name == name })
}
