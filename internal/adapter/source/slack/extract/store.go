package extract

import (
	"cmp"
	"context"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Store is the Reader over the store's extraction tables, plus the open segments of a DM.
type Store struct {
	DB       *sqlite.ExtractionStore
	Segments *sqlite.SegmentStore
}

var _ Reader = Store{}

// scanKey holds the job's change cursor; no unit has an empty key.
var scanKey = unit.Unit{Provider: slack.Provider}

func (s Store) States(ctx context.Context, units []unit.Unit) (map[unit.Unit]UnitState, error) {
	rows, err := s.DB.States(ctx, units)
	if err != nil {
		return nil, err
	}
	out := make(map[unit.Unit]UnitState, len(rows))
	for u, r := range rows {
		out[u] = UnitState{Brief: r.Brief, Verdict: r.Verdict, VerdictWhy: r.VerdictWhy, ViewerRole: r.ViewerRole, Topic: r.Topic,
			StartedByViewer: r.StartedByViewer, ClosurePosted: r.ClosurePosted, At: r.At}
	}
	return out, nil
}

func (s Store) Hints(ctx context.Context, u unit.Unit) (map[string]Hint, error) {
	rows, err := s.DB.Hints(ctx, u)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Hint, len(rows))
	for id, h := range rows {
		out[id] = Hint(h)
	}
	return out, nil
}

func (s Store) state(ctx context.Context, u unit.Unit) (sqlite.ExtractionState, error) {
	rows, err := s.DB.States(ctx, []unit.Unit{u})
	return rows[u], err
}

// spans are conv's open segments, ascending, with their units.
func (s Store) spans(ctx context.Context, team, conv string) ([]slack.Span, []unit.Unit, error) {
	rows, err := s.Segments.Of(ctx, slack.Provider, conv)
	if err != nil {
		return nil, nil, err
	}
	spans := make([]slack.Span, len(rows))
	units := make([]unit.Unit, len(rows))
	for i, r := range rows {
		spans[i] = slack.Span{ID: r.ID, FromTS: r.FromTS, ToTS: r.ToTS}
		units[i] = slack.Key{Team: cmp.Or(r.Team, team), Conversation: conv, Segment: r.ID}.Unit()
	}
	return spans, units, nil
}
