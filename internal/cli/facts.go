package cli

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// hostAnswers reads the user's answers on a unit from the host's events of
// the kinds its plugin's manifest marks answers_asked_turn, by the viewer
// the plugin stored; before it knows the viewer, it finds none. An event's
// data starts with the answer's state, which the manifest's verdicts turn
// into the record's verdict, and each answer points to its unit.
type hostAnswers struct {
	events interface {
		UnitEvents(ctx context.Context, units []unit.Unit, kinds []string, since time.Time) ([]port.Event, error)
	}
	viewer interface {
		Viewer(ctx context.Context, p unit.ProviderID) (string, error)
	}
}

var _ turns.Answers = hostAnswers{}

func (a hostAnswers) Answers(ctx context.Context, u unit.Unit, since time.Time) ([]turns.SourceAnswer, error) {
	answering := builtin.AnsweringEvents[u.Provider]
	if len(answering) == 0 {
		return nil, nil
	}
	viewer, err := a.viewer.Viewer(ctx, u.Provider)
	if err != nil || viewer == "" {
		return nil, err
	}
	evs, err := a.events.UnitEvents(ctx, []unit.Unit{u}, slices.Sorted(maps.Keys(answering)), since)
	if err != nil {
		return nil, err
	}
	var out []turns.SourceAnswer
	for _, ev := range evs {
		if !strings.EqualFold(ev.Actor, viewer) || ev.At.Before(since) {
			continue
		}
		state, _, _ := strings.Cut(ev.Data, ":")
		verdict := answering[ev.Kind][state]
		if verdict == "" {
			continue
		}
		out = append(out, turns.SourceAnswer{ID: ev.Key, State: state, Verdict: verdict, URL: u.Address(), At: ev.At})
	}
	return out, nil
}
