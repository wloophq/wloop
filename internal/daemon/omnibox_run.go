package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/omnibox"
	"github.com/wloophq/wloop/internal/domain/task"
)

// omniboxSource is the source the omnibox's operations reach their handlers as.
const omniboxSource = "cockpit"

// startIntent is the intent a start operation runs on the task it captures.
const startIntent = "start"

// handleOmniboxRun runs confirmed operations in order, each as one request to the handler a click would
// reach, with the caller's headers, so each is refused or gated exactly as that click. An operation no verb of
// the omnibox targets is refused by itself; the others run.
func (s *Server) handleOmniboxRun(w http.ResponseWriter, r *http.Request) {
	var in api.OmniboxRun
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(in.Operations) == 0 || len(in.Operations) > omnibox.MaxOperations {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("send 1 to %d operations", omnibox.MaxOperations))
		return
	}
	voc := s.omniboxVocabulary()
	out := api.OmniboxRan{Results: make([]api.OmniboxResult, len(in.Operations))}
	for i, op := range in.Operations {
		out.Results[i] = s.runOperation(r, voc, op)
	}
	writeJSON(w, out)
}

func (s *Server) runOperation(r *http.Request, voc omnibox.Vocabulary, op api.OmniboxOperation) api.OmniboxResult {
	if why, params := voc.Allowed(omnibox.AskOp(op)); why != "" {
		return api.OmniboxResult{Status: http.StatusForbidden, Error: why, Params: params}
	}
	incomplete := api.OmniboxResult{Status: http.StatusBadRequest, Error: api.OmniboxDropIncompleteOperation, Params: map[string]string{"op": op.Op}}
	switch op.Op {
	case omnibox.OpAction:
		if op.Unit == nil || op.Action == "" {
			return incomplete
		}
		return s.dispatch(r, api.UnitPath(*op.Unit, op.Action), api.ActionRequest{Guidance: op.Guidance, Source: omniboxSource})
	case omnibox.OpVerb:
		if op.Task == "" || op.Verb == "" {
			return incomplete
		}
		req := api.TaskRequest{Source: omniboxSource}
		if op.Guidance != "" {
			req.Args = map[string]string{"guidance": op.Guidance}
		}
		return s.dispatch(r, api.TaskPath(op.Task, op.Verb), req)
	case omnibox.OpCapture:
		if strings.TrimSpace(op.Title) == "" {
			return incomplete
		}
		return s.dispatch(r, api.TasksPath, api.TaskCreate{Title: op.Title, Brief: op.Brief})
	case omnibox.OpStart:
		if strings.TrimSpace(op.Brief) == "" {
			return incomplete
		}
		return s.start(r, op)
	}
	return api.OmniboxResult{Status: http.StatusBadRequest, Error: api.OmniboxDropUnknownOperation, Params: map[string]string{"op": op.Op}}
}

// start captures a task briefed with op's brief, titled as the start intent titles a capture, then starts the
// start intent on it in op's repository; the start is not tried when the capture fails.
func (s *Server) start(r *http.Request, op api.OmniboxOperation) api.OmniboxResult {
	title, err := s.loaded().TargetTitle(startIntent, op.Brief)
	if err != nil || strings.TrimSpace(title) == "" {
		title, _, _ = strings.Cut(strings.TrimSpace(op.Brief), "\n")
	}
	captured := s.dispatch(r, api.TasksPath, api.TaskCreate{Title: title, Brief: op.Brief})
	if !captured.OK {
		return captured
	}
	req := api.ActionRequest{Source: omniboxSource}
	if op.Repo != "" {
		req.Inputs = map[string]any{"repo": op.Repo}
	}
	started := s.dispatch(r, api.UnitPath(task.UnitOf(task.ID(captured.Task)), startIntent), req)
	started.Task = captured.Task
	return started
}

// dispatch posts body to path on the daemon's own handler with r's headers, and reads its answer.
func (s *Server) dispatch(r *http.Request, path string, body any) api.OmniboxResult {
	b, err := json.Marshal(body)
	if err != nil {
		return api.OmniboxResult{Status: http.StatusInternalServerError, Error: api.OmniboxRunFailed, Params: map[string]string{"error": err.Error()}}
	}
	sub, err := http.NewRequestWithContext(r.Context(), http.MethodPost, path, bytes.NewReader(b))
	if err != nil {
		return api.OmniboxResult{Status: http.StatusInternalServerError, Error: api.OmniboxRunFailed, Params: map[string]string{"error": err.Error()}}
	}
	sub.Header = r.Header.Clone()
	sub.Header.Set("Content-Type", "application/json")
	sub.Host, sub.RemoteAddr = r.Host, r.RemoteAddr
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, sub)
	var answer struct {
		Error   string   `json:"error"`
		Refusal string   `json:"refusal"`
		Output  []string `json:"output"`
		Lines   []string `json:"lines"`
		ID      string   `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &answer)
	out := api.OmniboxResult{Status: rec.Code, OK: rec.Code < 300, Output: answer.Output, Task: answer.ID}
	if out.Output == nil {
		out.Output = answer.Lines
	}
	if !out.OK {
		out.Error, out.Params, out.Refusal = api.OmniboxRunFailed, map[string]string{"error": answer.Error}, answer.Refusal
		out.Output, out.Task = nil, ""
	}
	return out
}
