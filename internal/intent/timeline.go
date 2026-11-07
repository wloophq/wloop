package intent

import (
	"cmp"
	"maps"
	"slices"
	"strings"
	"text/template"

	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/internal/profile"
)

// TimelineFile words the core's event kinds and declares the facts a timeline folds its events into.
const TimelineFile = "timeline.toml"

// TimelineDef is timeline.toml: the core's event kinds as the timeline names them, and its facts in the order
// they are tried.
type TimelineDef struct {
	Events map[string]TimelineEvent
	Facts  []TimelineFact
}

// TimelineEvent is one core event kind's words and tone.
type TimelineEvent struct {
	Label, Tone string
}

// TimelineFact is one [facts.<name>]: the event kinds that open, close and join it, and how it reads.
type TimelineFact struct {
	Name                 string
	Start, End, Absorb   []string
	Label, Running, Tone string
	// Unfinished is how a span reads that its start kind came to again before its end.
	Unfinished string
	Order      int
	Subs       map[string]string
}

type mTimeline struct {
	Events map[string]mTimelineEvent `toml:"events"`
	Facts  map[string]mTimelineFact  `toml:"facts"`
}

type mTimelineEvent struct {
	Label *string `toml:"label"`
	Tone  *string `toml:"tone"`
}

type mTimelineFact struct {
	Start      []string          `toml:"start"`
	End        []string          `toml:"end"`
	Absorb     []string          `toml:"absorb"`
	Label      string            `toml:"label"`
	Running    string            `toml:"running"`
	Unfinished string            `toml:"unfinished"`
	Tone       string            `toml:"tone"`
	Order      int               `toml:"order"`
	Subs       map[string]string `toml:"subs"`
}

// factValues are the values a fact's label and running text read.
var factValues = map[string]string{"actor": "dana", "duration": "7m", "count": "4", "sub": "APPROVED", "event": "reviewed"}

// loadTimeline reads timeline.toml from bundled and every other layer, in r's locale: a higher layer's label
// replaces a lower one's, and one that fails keeps the lower one; a fact of a higher layer replaces the one of
// its name whole. Every core event kind needs a label, and every kind a fact names is a core kind, a registered
// plugin's or one of verbKinds, the task verbs' events.
func loadTimeline(layers []profile.Layer, r layerReader, verbKinds []string) (TimelineDef, Problems) {
	d := TimelineDef{Events: map[string]TimelineEvent{}}
	facts := map[string]TimelineFact{}
	known := timelineKinds(verbKinds)
	problems, read := readLayered(layers, r, TimelineFile, func(file string, src []byte) Problems {
		return mergeTimeline(&d, facts, known, file, src)
	})
	if !read {
		return d, problems
	}
	for _, k := range builtin.EventKinds {
		if d.Events[k.Name].Label == "" {
			problems = append(problems, Problem{File: TimelineFile, Field: "events." + k.Name + ".label", Msg: "is missing: every core event kind has a label"})
		}
	}
	d.Facts = slices.SortedFunc(maps.Values(facts), func(a, b TimelineFact) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Name, b.Name))
	})
	return d, problems
}

// timelineKinds are the event kinds a fact may name.
func timelineKinds(verbKinds []string) []string {
	out := slices.Concat(builtin.ValueNames(builtin.EventKinds), slices.Collect(maps.Keys(builtin.EventProviders)), verbKinds)
	slices.Sort(out)
	return slices.Compact(out)
}

func mergeTimeline(d *TimelineDef, facts map[string]TimelineFact, known []string, file string, src []byte) Problems {
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
		if v := m.Events[kind].Tone; v != nil && slices.Contains(builtin.ValueNames(builtin.Tones), *v) {
			e.Tone = *v
		}
		d.Events[kind] = e
	}
	for _, name := range slices.Sorted(maps.Keys(m.Facts)) {
		if f, ok := timelineFact(s, known, name, m.Facts[name]); ok {
			facts[name] = f
		}
	}
	s.addGrammar(grammar(builtin.SurfaceTimeline, src))
	return s.problems
}

// timelineFact is fact name as m declares it; false, with its problems on s, when it names a kind no one
// records, or its texts do not parse.
func timelineFact(s *sheet, known []string, name string, m mTimelineFact) (TimelineFact, bool) {
	at := "facts." + name
	before := len(s.problems)
	if len(m.Start) == 0 {
		s.add(at+".start", "is required: the event kinds that open the fact")
	}
	for _, l := range []struct {
		key  string
		list []string
	}{{"start", m.Start}, {"end", m.End}, {"absorb", m.Absorb}} {
		key := l.key
		for _, k := range l.list {
			if !slices.Contains(known, k) {
				s.add(at+"."+key, "%q names no event kind of the core, a plugin or a task verb%s", k, expr.DidYouMean(k, known))
			}
		}
	}
	for _, l := range [][2]string{{"label", m.Label}, {"running", m.Running}, {"unfinished", m.Unfinished}} {
		key, text := l[0], l[1]
		if text == "" {
			continue
		}
		t, err := template.New(key).Option("missingkey=error").Parse(text)
		if err == nil {
			values := maps.Clone(factValues)
			if key != "label" {
				delete(values, "duration")
			}
			err = t.Execute(&strings.Builder{}, values)
		}
		if err != nil {
			s.add(at+"."+key, "does not read: %v", err)
		}
	}
	if m.Tone != "" && !slices.Contains(builtin.ValueNames(builtin.Tones), m.Tone) {
		return TimelineFact{}, false
	}
	if len(s.problems) > before {
		return TimelineFact{}, false
	}
	return TimelineFact{Name: name, Start: m.Start, End: m.End, Absorb: m.Absorb, Label: strings.TrimSpace(m.Label), Running: strings.TrimSpace(m.Running),
		Unfinished: strings.TrimSpace(m.Unfinished), Tone: m.Tone, Order: m.Order, Subs: m.Subs}, true
}
