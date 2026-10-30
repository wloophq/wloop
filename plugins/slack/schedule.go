package slack

import (
	"context"
	"time"

	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/items"
	"github.com/wloophq/wloop/plugins/slack/store"
	"github.com/wloophq/wloop/plugins/slack/unit"
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
	DB       *store.SlackStore
	Heat     HeatReader
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

func (h HeatScheduler) heat(ctx context.Context, us []unit.Unit) (map[unit.Unit]Heat, error) {
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
func poll(us []unit.Unit, heat map[unit.Unit]Heat) time.Duration {
	d := PollCold
	for _, u := range us {
		switch heat[u] {
		case HeatActive:
			return PollActive
		case HeatWarm:
			d = PollWarm
		}
	}
	return d
}

// units are a thread object's unit, or the segments of a DM's latest message.
func (h HeatScheduler) units(ctx context.Context, team string, row store.ObjectRow) ([]unit.Unit, error) {
	if row.Kind != store.SlackDM {
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

// Heat is how busy a unit's items keep it.
type Heat int

const (
	HeatCold Heat = iota
	HeatWarm
	HeatActive
)

// HeatReader reads the heat of units; a unit absent is cold.
type HeatReader interface {
	Heat(ctx context.Context, units []unit.Unit) (map[unit.Unit]Heat, error)
}

// LedgerHeat reads heat from the host's items: active while a unit has an
// open item, warm for WarmFor after its last item resolved, cold otherwise.
type LedgerHeat struct {
	Items   func(ctx context.Context, units []unit.Unit) (map[unit.Unit][]items.Item, error)
	WarmFor func() time.Duration
	Now     func() time.Time
}

func (h LedgerHeat) Heat(ctx context.Context, units []unit.Unit) (map[unit.Unit]Heat, error) {
	out := map[unit.Unit]Heat{}
	if len(units) == 0 {
		return out, nil
	}
	got, err := h.Items(ctx, units)
	if err != nil {
		return nil, err
	}
	cutoff := h.Now().Add(-h.WarmFor())
	for u, its := range got {
		for _, it := range its {
			switch {
			case it.State == items.Open:
				out[u] = HeatActive
			case it.State == items.Resolved && it.UpdatedAt.After(cutoff) && out[u] != HeatActive:
				out[u] = HeatWarm
			}
		}
	}
	return out, nil
}

// Clock is the plugin's clock.
type Clock interface{ Now() time.Time }

type clockFunc func() time.Time

func (f clockFunc) Now() time.Time { return f() }
