package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/lens"
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
	as := r.URL.Query().Get("as")
	if as != "" && as != "nodes" {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("as: %q is not nodes", as))
		return
	}
	evs, err := s.deps.Timeline.Of(ctx, e.ID, f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	shown := s.timelineEvent(e.ID)
	if as == "nodes" {
		writeJSON(w, s.timelineNodes(evs, shown))
		return
	}
	out := make([]api.TimelineEvent, len(evs))
	for i, ev := range evs {
		out[i] = shown(ev)
	}
	writeJSON(w, out)
}

// timelineEvent shows an event of task id's timeline as the API serves it.
func (s *Server) timelineEvent(id task.ID) func(timeline.Event) api.TimelineEvent {
	own := task.UnitOf(id)
	via, viaWorkflow := s.loaded().Tasks.ViaSession, s.loaded().Tasks.ViaWorkflow
	return func(ev timeline.Event) api.TimelineEvent {
		out := api.TimelineEvent{Seq: ev.Seq, Kind: ev.Kind, Actor: ev.Actor, Session: ev.Session, At: ev.At, Data: jsonData(ev.Data)}
		kind, name, _ := strings.Cut(ev.Actor, ":")
		switch {
		case ev.Session != "" && via != "":
			out.By = strings.ReplaceAll(via, "{session}", ev.Session)
		case kind == string(task.ActorWorkflow) && name != "" && viaWorkflow != "":
			out.By = strings.ReplaceAll(viaWorkflow, "{workflow}", name)
		}
		if ev.Kind == timeline.KindRecord {
			out.Sub = ev.Key
		}
		if ev.Unit != own {
			out.Unit = ev.Unit.String()
		}
		return out
	}
}

// timelineNodes folds evs, newest first, into the facts of the profile's timeline.toml, newest first.
func (s *Server) timelineNodes(evs []timeline.Event, shown func(timeline.Event) api.TimelineEvent) []api.TimelineNode {
	l := s.loaded()
	rules := make([]timeline.Rule, len(l.Timeline.Facts))
	for i, f := range l.Timeline.Facts {
		rules[i] = timeline.Rule{Name: f.Name, Start: f.Start, End: f.End, Absorb: f.Absorb, Label: f.Label, Running: f.Running, Unfinished: f.Unfinished, Tone: f.Tone, Subs: f.Subs}
	}
	oldest := slices.Clone(evs)
	slices.Reverse(oldest)
	nodes := timeline.Group(oldest, rules, s.timelineWords())
	out := make([]api.TimelineNode, len(nodes))
	for i, n := range nodes {
		events := make([]api.TimelineEvent, len(n.Events))
		for j, ev := range n.Events {
			events[j] = shown(ev)
		}
		out[i] = api.TimelineNode{Rule: n.Rule, Label: n.Label, Tone: n.Tone, At: n.At, End: n.End, Running: n.Running, Actor: n.Actor, Events: events}
	}
	return out
}

// timelineWords are the words timeline.Group reads: the catalog's event kinds, the task verbs' events by
// their verbs' labels, durations in the row words, and each event's sub as its kind's data shows.
func (s *Server) timelineWords() timeline.Words {
	cat, views := s.rowsCatalog(), s.viewsCatalog()
	kinds := make(map[string]timeline.Word, len(cat.Events)+len(views.Verbs))
	data := map[string]string{}
	for _, v := range views.Verbs {
		kinds[task.VerbEventKind(task.Verb(v.Verb))] = timeline.Word{Label: v.Label}
	}
	for _, e := range cat.Events {
		kinds[e.Kind] = timeline.Word{Label: e.Label, Tone: e.Tone}
		data[e.Kind] = e.Data
	}
	return timeline.Words{
		Kinds:    kinds,
		Duration: func(d time.Duration) string { return lens.Age(views.Lens.RowWords, d) },
		Sub:      func(ev timeline.Event) string { return eventSub(ev, data[ev.Kind]) },
	}
}

// eventSub is ev's short value as its kind's data shows: a record's kind, else its data cut as show says.
func eventSub(ev timeline.Event, show string) string {
	if ev.Kind == timeline.KindRecord {
		return ev.Key
	}
	if json.Valid([]byte(ev.Data)) && strings.HasPrefix(strings.TrimSpace(ev.Data), "{") {
		return ""
	}
	switch show {
	case "none":
		return ""
	case "first_field":
		first, _, _ := strings.Cut(ev.Data, ":")
		return first
	case "short_sha":
		return ev.Data[:min(7, len(ev.Data))]
	}
	return ev.Data
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
