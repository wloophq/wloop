package slack

import (
	"context"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/port"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Poll intervals by heat; polling never stops.
const (
	PollActive = 5 * time.Minute
	PollWarm   = 30 * time.Minute
	PollCold   = 60 * time.Minute
	// RecentFor keeps an object active after its last activity.
	RecentFor = 24 * time.Hour
)

// HeatScheduler is the dslack.Scheduler that checks an object again by the
// heat of its units and its last activity: active or active in the last
// RecentFor every PollActive, warm every PollWarm, cold every PollCold. An
// object the store lacks yet is active, and so is every object when the
// heat cannot be read.
type HeatScheduler struct {
	DB       *sqlite.SlackStore
	Heat     port.HeatReader
	Segments dslack.Segments
	Log      func(format string, args ...any)
}

var _ dslack.Scheduler = HeatScheduler{}

func (h HeatScheduler) Next(ctx context.Context, objects []string, now time.Time) (map[string]time.Time, error) {
	out := make(map[string]time.Time, len(objects))
	team, err := h.DB.Cursor(ctx, CursorViewerTeam)
	if err != nil {
		return nil, err
	}
	unitsOf := make(map[string][]unit.Unit, len(objects))
	var all []unit.Unit
	for _, o := range objects {
		row, ok, err := h.DB.Object(ctx, o)
		if err != nil {
			return nil, err
		}
		if !ok || now.Sub(row.LastActivityAt) < RecentFor {
			out[o] = now.Add(PollActive)
			continue
		}
		us, err := h.units(ctx, team, row)
		if err != nil {
			return nil, err
		}
		unitsOf[o] = us
		all = append(all, us...)
	}
	if len(unitsOf) == 0 {
		return out, nil
	}
	heat, err := h.heat(ctx, all)
	for o, us := range unitsOf {
		if err != nil {
			out[o] = now.Add(PollActive)
			continue
		}
		out[o] = now.Add(poll(us, heat))
	}
	return out, nil
}

func (h HeatScheduler) heat(ctx context.Context, us []unit.Unit) (map[unit.Unit]port.Heat, error) {
	if h.Heat == nil || len(us) == 0 {
		return nil, nil
	}
	heat, err := h.Heat.Heat(ctx, us)
	if err != nil && h.Log != nil {
		h.Log("slack schedule: heat of %d units: %v; checking them as active", len(us), err)
	}
	return heat, err
}

// poll is the interval of the hottest unit; no unit is cold.
func poll(us []unit.Unit, heat map[unit.Unit]port.Heat) time.Duration {
	d := PollCold
	for _, u := range us {
		switch heat[u] {
		case port.HeatActive:
			return PollActive
		case port.HeatWarm:
			d = PollWarm
		}
	}
	return d
}

// units are a thread object's unit, or the segments of a DM's latest message.
func (h HeatScheduler) units(ctx context.Context, team string, row sqlite.ObjectRow) ([]unit.Unit, error) {
	if row.Kind != sqlite.SlackDM {
		if team == "" {
			return nil, nil
		}
		return []unit.Unit{dslack.ThreadUnit(team, row.Conversation, row.TS)}, nil
	}
	if h.Segments == nil || row.Version == "" {
		return nil, nil
	}
	return h.Segments.UnitsFor(ctx, row.Conversation, row.Version)
}

type clockFunc func() time.Time

func (f clockFunc) Now() time.Time { return f() }
