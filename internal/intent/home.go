package intent

import (
	"fmt"
	"strings"

	"github.com/wloophq/wloop/internal/domain/decision"
	"github.com/wloophq/wloop/internal/i18n"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/internal/profile"
)

// HomeFile is the profile file of the cockpit's home.
const HomeFile = "home.toml"

// HomeDef is home.toml over every layer, its texts in the load's locale.
type HomeDef struct {
	Template, Join, LastJoin, End, Count, Empty string
	PerSentence                                 int
	Counters                                    []HomeCounterDef
	Sections                                    []HomeSectionDef
	CaptureHint                                 string
}

// HomeCounterDef is a counter of the briefing: a decision kind's (Kind) or a view's (View).
type HomeCounterDef struct {
	ID           string
	Kind         decision.Kind
	View         string
	Noun, Clause i18n.Forms
}

// HomeSectionDef is a section of the home: the first Limit rows of View, each showing Time.
type HomeSectionDef struct {
	ID, View, Time string
	Limit          int
}

type mHome struct {
	Template    *string        `toml:"template"`
	Join        *string        `toml:"join"`
	LastJoin    *string        `toml:"last_join"`
	End         *string        `toml:"end"`
	Count       *string        `toml:"count"`
	Empty       *string        `toml:"empty"`
	PerSentence *int           `toml:"per_sentence"`
	Counters    []mHomeCounter `toml:"counters"`
	Sections    []mHomeSection `toml:"sections"`
	Capture     *struct {
		Hint *string `toml:"hint"`
	} `toml:"capture"`
}

type mHomeCounter struct {
	ID     string `toml:"id"`
	Kind   string `toml:"kind"`
	View   string `toml:"view"`
	Noun   any    `toml:"noun"`
	Clause any    `toml:"clause"`
}

type mHomeSection struct {
	ID    string `toml:"id"`
	View  string `toml:"view"`
	Limit *int   `toml:"limit"`
	Time  string `toml:"time"`
}

type homeLoad struct {
	def HomeDef
	// viewsAt are the sheets and paths of the views the counters and sections name, checked once views load.
	viewsAt []ref
}

// loadHome reads home.toml from bundled and the user's layers: a higher layer's key replaces a lower one's, a
// list of counters or sections the whole list.
func loadHome(layers []profile.Layer, r layerReader) (*homeLoad, Problems) {
	l := &homeLoad{def: HomeDef{Template: "default", PerSentence: 3}}
	problems, _ := readLayered(layers, r, HomeFile, l.merge)
	return l, problems
}

func (l *homeLoad) merge(file string, src []byte) Problems {
	m, problems, _, complete := decode[mHome](file, src)
	if !complete {
		return problems
	}
	s := &sheet{file: file, at: positions(src), problems: problems}
	d := &l.def
	text(s, "template", m.Template, &d.Template)
	for _, w := range []struct {
		key string
		v   *string
		dst *string
	}{{"join", m.Join, &d.Join}, {"last_join", m.LastJoin, &d.LastJoin}, {"end", m.End, &d.End}} {
		if w.v != nil {
			*w.dst = *w.v
		}
	}
	text(s, "empty", m.Empty, &d.Empty)
	if m.Count != nil {
		if !strings.Contains(*m.Count, "{n}") {
			s.add("count", "needs {n}, where the count goes")
		} else {
			d.Count = *m.Count
		}
	}
	if m.PerSentence != nil {
		if *m.PerSentence < 1 {
			s.add("per_sentence", "is at least 1")
		} else {
			d.PerSentence = *m.PerSentence
		}
	}
	if m.Capture != nil {
		text(s, "capture.hint", m.Capture.Hint, &d.CaptureHint)
	}
	if m.Counters != nil {
		l.dropRefs("counters[")
		d.Counters = nil
		for i, c := range m.Counters {
			if def, ok := l.counter(s, fmt.Sprintf("counters[%d]", i), c); ok {
				d.Counters = append(d.Counters, def)
			}
		}
	}
	if m.Sections != nil {
		l.dropRefs("sections[")
		d.Sections = nil
		for i, sec := range m.Sections {
			path := fmt.Sprintf("sections[%d]", i)
			def := HomeSectionDef{ID: strings.TrimSpace(sec.ID), View: strings.TrimSpace(sec.View), Time: "moved", Limit: 3}
			if sec.Time != "" {
				n := len(s.problems)
				s.oneOf(path+".time", sec.Time, builtin.HomeTimes())
				if len(s.problems) == n {
					def.Time = sec.Time
				}
			}
			if sec.Limit != nil {
				if *sec.Limit < 1 {
					s.add(path+".limit", "is at least 1")
				} else {
					def.Limit = *sec.Limit
				}
			}
			if def.ID == "" || def.View == "" {
				continue
			}
			l.viewsAt = append(l.viewsAt, ref{s, path + ".view", def.View})
			d.Sections = append(d.Sections, def)
		}
	}
	s.addGrammar(grammar(builtin.SurfaceHome, src))
	return s.problems
}

func (l *homeLoad) counter(s *sheet, path string, c mHomeCounter) (HomeCounterDef, bool) {
	def := HomeCounterDef{ID: strings.TrimSpace(c.ID), Kind: decision.Kind(c.Kind), View: strings.TrimSpace(c.View)}
	switch {
	case c.Kind != "" && def.View != "", c.Kind == "" && def.View == "":
		s.add(path, "sets kind or view, one of them")
		return def, false
	case c.Kind != "":
		n := len(s.problems)
		s.oneOf(path+".kind", c.Kind, builtin.DecisionKinds())
		if len(s.problems) > n {
			return def, false
		}
		if def.ID == "" {
			def.ID = c.Kind
		}
	default:
		if def.ID == "" {
			s.add(path+".id", "is required for a view's counter")
			return def, false
		}
		l.viewsAt = append(l.viewsAt, ref{s, path + ".view", def.View})
	}
	for key, v := range map[string]any{"noun": c.Noun, "clause": c.Clause} {
		if v == nil {
			continue
		}
		f, ok := formsOf(s, path+"."+key, v)
		if !ok {
			return def, false
		}
		if key == "noun" {
			def.Noun = f
		} else {
			def.Clause = f
		}
	}
	if def.View != "" && (def.Noun == nil || def.Clause == nil) {
		s.add(path, "a view's counter needs its noun and its clause")
		return def, false
	}
	return def, true
}

func (l *homeLoad) dropRefs(prefix string) {
	kept := l.viewsAt[:0]
	for _, r := range l.viewsAt {
		if !strings.HasPrefix(r.path, prefix) {
			kept = append(kept, r)
		}
	}
	l.viewsAt = kept
}

// check says which counter or section names no view, and leaves it out.
func (l *homeLoad) check(views ViewsDef) Problems {
	var out Problems
	ids := views.IDs()
	bad := map[string]bool{}
	for _, r := range l.viewsAt {
		if _, ok := views.View(r.name); !ok {
			out = append(out, r.s.problem(r.path, "names no view of %s%s", ViewsFile, expr.DidYouMean(r.name, ids)))
			bad[r.name] = true
		}
	}
	keep := l.def.Counters[:0]
	for _, c := range l.def.Counters {
		if !bad[c.View] || c.View == "" {
			keep = append(keep, c)
		}
	}
	l.def.Counters = keep
	sections := l.def.Sections[:0]
	for _, s := range l.def.Sections {
		if !bad[s.View] {
			sections = append(sections, s)
		}
	}
	l.def.Sections = sections
	return out
}
