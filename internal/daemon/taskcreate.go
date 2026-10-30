package daemon

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/placement"
	"github.com/wloophq/wloop/internal/domain/task"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// handleTaskCreate creates a task from a TaskCreate: a title alone captures it; a brief is the task's brief
// record and each property is parsed by its tasks.toml type, both written by whoever calls. A refused request
// creates nothing.
func (s *Server) handleTaskCreate(w http.ResponseWriter, r *http.Request) {
	g, ok := s.tasks(w)
	if !ok {
		return
	}
	var req api.TaskCreate
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, tasksBodyMax)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		writeError(w, http.StatusBadRequest, "a task needs a title")
		return
	}
	if req.Unit != nil && s.refusePlugin(w, *req.Unit) {
		return
	}
	actor, ok := requestActor(w, r)
	if !ok {
		return
	}
	writer := task.Writer(actor)
	props, err := s.createProps(req.Props, writer)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	brief, err := s.createBrief(req.Brief, writer)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	switch {
	case brief != nil && s.deps.RecordWriter == nil:
		writeError(w, http.StatusServiceUnavailable, "no record store: this daemon keeps no brief")
		return
	case req.Placement != "" && s.deps.Placements == nil:
		writeError(w, http.StatusServiceUnavailable, "no placement store: this daemon keeps no placement")
		return
	case req.Placement != "" && !intentload.ValidRepo(req.Placement):
		writeError(w, http.StatusBadRequest, fmt.Sprintf("placement %q is not a repository: owner/name", req.Placement))
		return
	}
	ctx := r.Context()
	e, err := g.Capture(ctx, req.Title, "", req.Unit)
	if err != nil {
		writeTasksError(w, err)
		return
	}
	u := task.UnitOf(e.ID)
	if brief != nil {
		if err := s.deps.RecordWriter.PutRecord(ctx, u, builtin.BuildsBrief, writer, brief, s.deps.Now()); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("%s created, its brief not kept: %v", e.ID, err))
			return
		}
	}
	for _, name := range slices.Sorted(maps.Keys(props)) {
		if err := g.SetProp(ctx, e.ID, name, props[name], writer); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("%s created, its property %s not set: %v", e.ID, name, err))
			return
		}
	}
	if req.Placement != "" {
		if err := s.deps.Placements.SetOverride(ctx, u, placement.Placement{Repo: req.Placement}); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("%s created, its placement not kept: %v", e.ID, err))
			return
		}
	}
	if e, err = g.Task(ctx, e.ID); err != nil {
		writeTasksError(w, err)
		return
	}
	s.writeTask(w, ctx, g, e)
}

// createProps are the properties texts set, each parsed by its tasks.toml type in the daemon's location; a
// property tasks.toml does not declare, that w does not write, or whose text its type refuses, is an error.
func (s *Server) createProps(texts map[string]string, w task.Writer) (map[string]task.Value, error) {
	def := s.loaded().Tasks
	out := make(map[string]task.Value, len(texts))
	for _, name := range slices.Sorted(maps.Keys(texts)) {
		v, err := def.Parse(name, texts[name], s.deps.Now().Location())
		if err != nil {
			return nil, err
		}
		if p, _ := def.Prop(name); !slices.Contains(p.Writers, w) {
			return nil, fmt.Errorf("a %s does not write the property %s: its writers are %v", w, name, p.Writers)
		}
		out[name] = v
	}
	return out, nil
}

// createBrief is the brief record that markdown is, nil for none; records.toml must let w write a brief.
func (s *Server) createBrief(markdown string, w task.Writer) ([]byte, error) {
	if strings.TrimSpace(markdown) == "" {
		return nil, nil
	}
	data, err := json.Marshal(map[string]string{builtin.BriefText: markdown})
	if err != nil {
		return nil, err
	}
	kind, ok := s.loaded().Records.Kind(builtin.BuildsBrief)
	if !ok || !kind.Writes(w) {
		return nil, fmt.Errorf("a %s does not write a %s record: %s says who does", w, builtin.BuildsBrief, intentload.RecordsFile)
	}
	if kind.Schema != nil {
		if err := kind.Schema.ValidateJSON(data); err != nil {
			return nil, fmt.Errorf("the brief does not fit the %s record's schema: %w", builtin.BuildsBrief, err)
		}
	}
	return data, nil
}
