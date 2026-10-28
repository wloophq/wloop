package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/turn"
)

// chatBodyMax bounds a chat message's request.
const chatBodyMax = 64 << 10

// handleChat delivers what the person wrote on a task's chat to its agent: its live session is told a
// message waits, else a turn starts with it. An agent's session writes no chat.
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	g, ok := s.tasks(w)
	if !ok {
		return
	}
	actor, ok := requestActor(w, r)
	if !ok {
		return
	}
	if actor != task.ActorPerson {
		writeError(w, http.StatusForbidden, "only the person writes on a task's chat")
		return
	}
	if s.deps.Messages == nil {
		writeError(w, http.StatusServiceUnavailable, messages.ErrNoChat.Error())
		return
	}
	var req api.ChatRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, chatBodyMax)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	ctx := context.WithoutCancel(r.Context())
	e, err := g.Task(ctx, task.ID(r.PathValue("id")))
	if err != nil {
		writeTasksError(w, err)
		return
	}
	ctx = s.readAhead(ctx, task.UnitOf(e.ID), false)
	s.deps.LoopMu.Lock()
	res, err := s.deps.Messages.Chat(withLoopHeld(ctx), messages.ChatRequest{Task: e.ID, Text: req.Text, Intent: strings.TrimSpace(req.Intent)})
	s.deps.LoopMu.Unlock()
	var refused messages.ChatRefusal
	switch {
	case errors.As(err, &refused):
		writeError(w, http.StatusConflict, refused.Error())
		return
	case errors.Is(err, messages.ErrNoChat):
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	case err != nil:
		writeTasksError(w, err)
		return
	}
	line := s.notice(builtin.NoticeChatTyped, nil)
	if res.Route != messages.ChatTyped {
		line = s.notice(builtin.NoticeChatStarted, map[string]any{"Intent": res.Intent, "Resumed": res.Route == messages.ChatResumed})
	}
	now, err := g.Task(ctx, e.ID)
	if err != nil {
		now = e
	}
	view, err := s.taskViews(ctx, g, false).view(ctx, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, api.TaskResult{Task: view, Lines: []string{line}})
}

// notice is the profile's notice name over data, the bundled text when the profile's fails, else its name.
func (s *Server) notice(name string, data map[string]any) string {
	full := map[string]any{"Name": brand.Name, "Command": brand.Command}
	for k, v := range data {
		full[k] = v
	}
	srcs := []string{s.loaded().Notices[name]}
	if b, err := turn.Bundled(); err == nil {
		srcs = append(srcs, b.Notices[name])
	}
	for _, src := range srcs {
		if src == "" {
			continue
		}
		var out strings.Builder
		if err := builtin.RenderToolText(&out, name, src, full); err == nil {
			return out.String()
		}
		s.logf("the notice %s: the profile's text does not render", name)
	}
	return name
}
