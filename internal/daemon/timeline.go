package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/timeline"
	"github.com/wloophq/wloop/internal/domain/task"
)

// handleTimeline serves a task's timeline, newest first, paged by Seq.
func (s *Server) handleTimeline(w http.ResponseWriter, r *http.Request) {
	g, ok := s.tasks(w)
	if !ok {
		return
	}
	if s.deps.Timeline == nil {
		writeError(w, http.StatusServiceUnavailable, "the timeline is not served")
		return
	}
	ctx := r.Context()
	e, err := g.Task(ctx, task.ID(r.PathValue("id")))
	if err != nil {
		writeTasksError(w, err)
		return
	}
	f, err := timelineFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	evs, err := s.deps.Timeline.Of(ctx, e.ID, f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	own := task.UnitOf(e.ID)
	out := make([]api.TimelineEvent, len(evs))
	for i, ev := range evs {
		out[i] = api.TimelineEvent{Seq: ev.Seq, Kind: ev.Kind, Actor: ev.Actor, At: ev.At, Data: jsonData(ev.Data)}
		if ev.Kind == timeline.KindRecord {
			out[i].Sub = ev.Key
		}
		if ev.Unit != own {
			out[i].Unit = ev.Unit.String()
		}
	}
	writeJSON(w, out)
}

func timelineFilter(r *http.Request) (timeline.Filter, error) {
	var f timeline.Filter
	q := r.URL.Query()
	if k := q.Get("kinds"); k != "" {
		f.Kinds = strings.Split(k, ",")
	}
	before, err := queryCount(q.Get("before"), 64)
	if err != nil {
		return f, fmt.Errorf("before: %w", err)
	}
	limit, err := queryCount(q.Get("limit"), 32)
	if err != nil {
		return f, fmt.Errorf("limit: %w", err)
	}
	f.Before, f.Limit = before, int(limit)
	return f, nil
}

// queryCount is a query parameter's count, 0 when absent; bits is the size it must fit.
func queryCount(v string, bits int) (int64, error) {
	if v == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(v, 10, bits)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%q is not a count", v)
	}
	return n, nil
}

// jsonData is an event's data as JSON: its JSON when it holds some, else
// the text as a JSON string.
func jsonData(data string) json.RawMessage {
	if data == "" {
		return nil
	}
	if json.Valid([]byte(data)) {
		return json.RawMessage(data)
	}
	b, _ := json.Marshal(data)
	return b
}
