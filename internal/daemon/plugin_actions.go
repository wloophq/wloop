package daemon

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/task"
)

func (s *Server) handlePluginAction(w http.ResponseWriter, r *http.Request) {
	if s.deps.PluginAction == nil {
		writeError(w, http.StatusNotFound, "no plugin runs actions in this daemon")
		return
	}
	var req api.PluginActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "plugin action: "+err.Error())
		return
	}
	actor, ok := requestActor(w, r)
	if !ok {
		return
	}
	req.Action, req.Actor = r.PathValue("action"), string(actor)
	out, err := s.deps.PluginAction(r.Context(), r.PathValue("id"), req)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
}

// handlePluginRun streams a running plugin's command line to the person
// who runs it; an agent's session runs none.
func (s *Server) handlePluginRun(w http.ResponseWriter, r *http.Request) {
	if s.deps.PluginRun == nil {
		writeError(w, http.StatusNotFound, "no plugin runs command lines in this daemon")
		return
	}
	actor, ok := requestActor(w, r)
	if !ok {
		return
	}
	if actor != task.ActorPerson {
		writeError(w, http.StatusForbidden, "a plugin's command line is the person's; an agent's session runs none")
		return
	}
	var req api.PluginRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "plugin run: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	st := &chunkStream{w: w, enc: json.NewEncoder(w)}
	exit, err := s.deps.PluginRun(r.Context(), r.PathValue("id"), req.Args,
		chunkWriter(func(b []byte) api.PluginRunChunk { return api.PluginRunChunk{Stdout: string(b)} }, st),
		chunkWriter(func(b []byte) api.PluginRunChunk { return api.PluginRunChunk{Stderr: string(b)} }, st))
	end := api.PluginRunChunk{Exit: &exit}
	if err != nil {
		end = api.PluginRunChunk{Error: err.Error()}
	}
	_ = st.send(end)
}

// chunkStream writes a command's chunks to its response, each flushed as
// it is written.
type chunkStream struct {
	mu  sync.Mutex
	w   http.ResponseWriter
	enc *json.Encoder
}

func (c *chunkStream) send(ch api.PluginRunChunk) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.enc.Encode(ch); err != nil {
		return err
	}
	if f, ok := c.w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}

type chunkWriterFunc func([]byte) (int, error)

func (f chunkWriterFunc) Write(b []byte) (int, error) { return f(b) }

func chunkWriter(of func([]byte) api.PluginRunChunk, st *chunkStream) chunkWriterFunc {
	return func(b []byte) (int, error) {
		if err := st.send(of(b)); err != nil {
			return 0, err
		}
		return len(b), nil
	}
}
