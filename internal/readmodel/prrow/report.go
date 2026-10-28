package prrow

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/plugins/source/value"
	"github.com/wloophq/wloop/internal/readmodel/overlay"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// Report is what the GitHub plugin last reported of a pull request, as
// its row reads it: the pull request, its situation from the source alone,
// and the source part of its payload, views and facets.
type Report struct {
	PR        dinv.PR
	Situation src.Situation
	Payload   Payload
	Views     []string
	Facets    map[string][]string
	Card      *src.Card
	Items     []src.Item
	// Facts are the workflow facts that read the pull request alone.
	Facts []src.Fact
}

// reportData is the part of the plugin's snapshot data a row reads.
type reportData struct {
	PR  dinv.PR  `json:"pr"`
	Row *Payload `json:"row"`
}

// ReportOf is the report snapshot s carries; false for a snapshot with no
// pull request.
func ReportOf(s src.Snapshot) (Report, bool, error) {
	if s.Gone || len(s.Data) == 0 {
		return Report{}, false, nil
	}
	var d reportData
	if err := json.Unmarshal(s.Data, &d); err != nil {
		return Report{}, false, fmt.Errorf("the snapshot of %s: %w", s.Unit.Key, err)
	}
	if d.Row == nil || s.Situation == nil {
		return Report{}, false, nil
	}
	out := Report{PR: d.PR, Situation: *s.Situation, Payload: *d.Row, Views: slices.Clone(s.Views), Facets: map[string][]string{},
		Card: s.Card, Items: s.Items, Facts: s.Facts}
	for id, v := range s.Facets {
		out.Facets[id] = []string{v}
	}
	for id, vs := range s.FacetValues {
		out.Facets[id] = slices.Clone(vs)
	}
	return out, true, nil
}

// source is the report's situation at now.
func (r Report) source(now time.Time) overlay.Situation {
	s := r.Situation
	stalled, _ := value.Resolve(s.Stalled, now).(bool)
	return overlay.Situation{Kind: s.Kind, WaitingOn: s.WaitingOn, Since: s.Since, Reason: overlay.Cut(s.Reason), Stalled: stalled}
}

// SituationOf is the situation of the report at now under the host's
// overlays: the run that drives its row, then the user's mute o, whose
// time is its newest muted event among evs; unmuted is it without the
// mute.
func SituationOf(r Report, f *TurnFact, o dinv.Override, evs []dinv.Event, now time.Time) (res, unmuted overlay.Situation) {
	sit := r.source(now)
	if f != nil && sit.Kind != overlay.KindDone {
		waiting := f.Waiting
		if f.Unread && o.MuteUntilStateChange {
			// An unread session cannot show that the muted state changed.
			waiting = o.MutedKind == overlay.KindLoopWaiting
		}
		if s, ok := f.Facts.Situation(waiting); ok {
			sit = s
		}
	}
	unmuted = sit
	m := overlay.Mute{Until: o.MutedUntil, UntilStateChange: o.MuteUntilStateChange, Kind: o.MutedKind, WaitingOn: o.MutedWaitingOn}
	for _, e := range evs {
		if e.Kind == dinv.KindMuted && !e.At.Before(m.At) {
			m.At = e.At
		}
	}
	if (!m.Until.IsZero() || m.UntilStateChange) && m.Holds(sit, now) {
		return m.Muted(), unmuted
	}
	return sit, unmuted
}
