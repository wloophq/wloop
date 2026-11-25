package daemon

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// handleTurnsLive serves the turns the floating widget shows: the running ones, those that ended since the
// local midnight, how many ask the user, and each intent's usual duration. The turns of an intent whose manifest
// says it reports nothing are left out of all of them. A dismissed turn counts toward the usual duration, as the
// timings report counts it, but is not listed as done.
func (s *Server) handleTurnsLive(w http.ResponseWriter, r *http.Request) {
	var tl turns.Timeline
	if s.deps.Turns != nil {
		tl, _ = s.deps.Turns.Store.(turns.Timeline)
	}
	if tl == nil {
		writeError(w, http.StatusServiceUnavailable, "the turns are not available")
		return
	}
	now := s.deps.Now()
	byUnit, err := tl.Timeline(r.Context(), now.AddDate(0, 0, -api.LiveUsualDays))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, s.liveTurns(r.Context(), byUnit, now))
}

func (s *Server) liveTurns(ctx context.Context, byUnit map[unit.Unit][]turn.Turn, now time.Time) api.LiveTurns {
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	usualSince := now.AddDate(0, 0, -api.LiveUsualDays)
	out := api.LiveTurns{Running: []api.LiveTurn{}, Done: []api.LiveTurn{}, Usual: map[string]int64{}}
	spans := map[string][]time.Duration{}
	unitOf := map[int64]unit.Unit{}
	reg := s.loaded().Registry
	for u, ts := range byUnit {
		for _, t := range ts {
			d, ok := reg.Get(dintent.Name(t.Intent))
			if ok && !d.Reports {
				continue
			}
			label := cmp.Or(d.Label, string(t.Intent))
			unitOf[t.Seq] = u
			from := cmp.Or(t.StartedAt, t.SentAt)
			switch {
			case t.State == turn.Asked:
				out.Asked++
			case t.State == turn.Sent || t.State == turn.Started:
				out.Running = append(out.Running, liveTurnOf(u, t, from, label))
			case t.State == turn.Withdrawn || t.State == turn.Replaced || t.EndedAt.IsZero():
			default:
				if !t.EndedAt.Before(usualSince) {
					spans[string(t.Intent)] = append(spans[string(t.Intent)], t.EndedAt.Sub(from))
				}
				if t.State != turn.Dismissed && !t.EndedAt.Before(midnight) {
					out.Done = append(out.Done, liveTurnOf(u, t, from, label))
				}
			}
		}
	}
	slices.SortFunc(out.Running, func(a, b api.LiveTurn) int { return cmp.Compare(a.Seq, b.Seq) })
	slices.SortFunc(out.Done, func(a, b api.LiveTurn) int { return cmp.Or(b.Ended.Compare(a.Ended), cmp.Compare(b.Seq, a.Seq)) })
	out.Done = out.Done[:min(len(out.Done), api.LiveDoneMax)]
	for intent, ds := range spans {
		out.Usual[intent] = api.QuantilesOf(ds).P50MS
	}
	s.titleTurns(ctx, out.Running, unitOf)
	s.titleTurns(ctx, out.Done, unitOf)
	return out
}

func liveTurnOf(u unit.Unit, t turn.Turn, from time.Time, label string) api.LiveTurn {
	lt := api.LiveTurn{Seq: t.Seq, Intent: string(t.Intent), Label: label, Unit: u.String(), Started: from, Ended: t.EndedAt}
	if !t.EndedAt.IsZero() {
		lt.DurationMS = t.EndedAt.Sub(from).Milliseconds()
	}
	return lt
}

// titleTurns names each turn's task and title: the task holding its unit, an open one first, else the unit's row.
func (s *Server) titleTurns(ctx context.Context, list []api.LiveTurn, unitOf map[int64]unit.Unit) {
	units := make([]unit.Unit, len(list))
	for i, lt := range list {
		units[i] = unitOf[lt.Seq]
	}
	pres := s.presentations(units)
	var held map[unit.Unit][]task.Task
	if s.deps.Tasks != nil {
		var err error
		if held, err = s.deps.Tasks.HoldersOfUnits(ctx, slices.Compact(slices.SortedFunc(slices.Values(units), api.CompareUnits))); err != nil {
			s.logf("live turns: %v", err)
		}
	}
	for i := range list {
		u := units[i]
		list[i].Title = pres[u].Title
		if tk := held[u]; len(tk) > 0 {
			list[i].Task, list[i].Title = string(tk[0].ID), cmp.Or(tk[0].Title, list[i].Title)
		}
	}
}
