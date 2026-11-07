package intent

import (
	"maps"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/internal/profile"
)

// TimelineFile words the core's event kinds.
const TimelineFile = "timeline.toml"

// TimelineDef is timeline.toml: the core's event kinds as the timeline names them.
type TimelineDef struct {
	Events map[string]TimelineEvent
}

// TimelineEvent is one core event kind's words.
type TimelineEvent struct {
	Label string
}

type mTimeline struct {
	Events map[string]mTimelineEvent `toml:"events"`
}

type mTimelineEvent struct {
	Label *string `toml:"label"`
}

// loadTimeline reads timeline.toml from bundled and every other layer, in r's locale: a higher layer's label
// replaces a lower one's, and one that fails keeps the lower one. Every core event kind needs a label.
func loadTimeline(layers []profile.Layer, r layerReader) (TimelineDef, Problems) {
	d := TimelineDef{Events: map[string]TimelineEvent{}}
	problems, read := readLayered(layers, r, TimelineFile, func(file string, src []byte) Problems {
		return mergeTimeline(&d, file, src)
	})
	if !read {
		return d, problems
	}
	for _, k := range builtin.EventKinds {
		if d.Events[k.Name].Label == "" {
			problems = append(problems, Problem{File: TimelineFile, Field: "events." + k.Name + ".label", Msg: "is missing: every core event kind has a label"})
		}
	}
	return d, problems
}

func mergeTimeline(d *TimelineDef, file string, src []byte) Problems {
	m, problems, _, complete := decode[mTimeline](file, src)
	if !complete {
		return problems
	}
	s := &sheet{file: file, at: positions(src), problems: problems}
	kinds := builtin.ValueNames(builtin.EventKinds)
	for _, kind := range slices.Sorted(maps.Keys(m.Events)) {
		if !slices.Contains(kinds, kind) {
			s.add("events."+kind, "names no core event kind%s", expr.DidYouMean(kind, kinds))
			continue
		}
		e := d.Events[kind]
		if v := m.Events[kind].Label; v != nil {
			if strings.TrimSpace(*v) == "" {
				s.add("events."+kind+".label", "is required")
			} else {
				e.Label = strings.TrimSpace(*v)
			}
		}
		d.Events[kind] = e
	}
	s.addGrammar(grammar(builtin.SurfaceTimeline, src))
	return s.problems
}
