package daemon

import (
	"context"
	"net/http"
	"slices"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/home"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/decision"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/i18n"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/keys"
)

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	g, ok := s.tasks(w)
	if !ok {
		return
	}
	ctx := r.Context()
	h, err := s.home(g).Home(ctx)
	if err != nil {
		writeTasksError(w, err)
		return
	}
	out := api.Home{Briefing: api.Briefing{Template: h.Template, Sentences: [][]api.BriefingPart{}, Counters: []api.HomeCounter{}, Empty: h.Empty},
		Sections: []api.HomeSection{}, Capture: api.HomeCapture{Hint: h.CaptureHint, Key: s.captureKey()}}
	for _, sentence := range h.Sentences {
		parts := make([]api.BriefingPart, len(sentence))
		for i, p := range sentence {
			if p.Counter >= 0 {
				parts[i] = api.BriefingPart{Counter: &p.Counter}
			} else {
				parts[i] = api.BriefingPart{Text: p.Text}
			}
		}
		out.Briefing.Sentences = append(out.Briefing.Sentences, parts)
	}
	for _, c := range h.Counts {
		hc := api.HomeCounter{ID: c.ID, Kind: string(c.Kind), View: string(c.View), Count: c.N, Label: c.Label}
		for _, n := range c.Nouns {
			hc.Nouns = append(hc.Nouns, api.PluginNoun{Plugin: string(n.Plugin), Count: n.N, Noun: n.Noun})
		}
		out.Briefing.Counters = append(out.Briefing.Counters, hc)
	}
	var shown []task.Task
	for _, sec := range h.Sections {
		shown = append(shown, sec.Tasks...)
	}
	vs := s.taskViews(ctx, g, false)
	vs.readRows(ctx, shown)
	for _, sec := range h.Sections {
		hs := api.HomeSection{ID: sec.ID, View: string(sec.View), Label: sec.Label, Time: sec.Time, Rows: []api.TaskView{}, Total: sec.Total}
		for _, e := range sec.Tasks {
			v, err := vs.view(ctx, e)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			hs.Rows = append(hs.Rows, v)
		}
		out.Sections = append(out.Sections, hs)
	}
	writeJSON(w, out)
}

// home is the home service over g and the loaded profile.
func (s *Server) home(g *coretasks.Service) home.Service {
	l := s.loaded()
	d := &taskDecisions{s: s, m: l.Decisions.Map}
	if s.deps.GateReader != nil {
		d.gates = s.deps.GateReader
	}
	return home.Service{Lists: g, Words: homeWords{l}, Decisions: d}
}

// captureKey is the key the cockpit's capture action runs on; "" when none.
func (s *Server) captureKey() string {
	for _, b := range keys.Core {
		if b.Action == "capture" && slices.Contains(b.Surfaces, keys.Web) {
			return b.Key
		}
	}
	return ""
}

// homeWords are the loaded profile's words of the home, in its locale.
type homeWords struct{ l *intentload.Loaded }

func (w homeWords) Profile() home.Profile {
	d := w.l.Home
	p := home.Profile{Template: d.Template, Join: d.Join, LastJoin: d.LastJoin, End: d.End, Count: d.Count, Empty: d.Empty,
		PerSentence: d.PerSentence, CaptureHint: d.CaptureHint}
	for _, c := range d.Counters {
		p.Counters = append(p.Counters, home.Counter{ID: c.ID, Kind: c.Kind, View: task.View(c.View), Noun: home.Forms(c.Noun), Clause: home.Forms(c.Clause)})
	}
	for _, sec := range d.Sections {
		v, _ := w.l.Views.View(sec.View)
		p.Sections = append(p.Sections, home.Section{ID: sec.ID, View: task.View(sec.View), Label: v.Label, Time: sec.Time, Limit: sec.Limit})
	}
	return p
}

func (w homeWords) Kind(k decision.Kind) home.KindWords {
	t := w.l.Decisions.Kinds[k]
	return home.KindWords{View: task.View(t.View), Noun: home.Forms(t.Noun), Clause: home.Forms(t.Clause), Queue: t.Queue, Empty: t.Empty, Primary: t.Primary}
}

func (w homeWords) Plugin(p unit.ProviderID) home.PluginWords {
	ui := w.l.UI[string(p)]
	out := home.PluginWords{Noun: home.Forms(ui.Noun), Kinds: map[decision.Kind]home.PluginKind{}, Primary: w.l.Decisions.Primary[p], Situations: ui.Situations}
	for k, d := range ui.Decisions {
		out.Kinds[decision.Kind(k)] = home.PluginKind{Clause: home.Forms(d.Clause), Queue: d.Queue, Empty: d.Empty}
	}
	return out
}

func (w homeWords) Form(n int) string { return i18n.Form(w.l.Locale, float64(n)) }

// taskDecisions are the decisions a task waits on: its open gates, read once for every task, and its units'
// situations and signals as the read model holds them.
type taskDecisions struct {
	s     *Server
	m     decision.Map
	gates gates.Reader
	open  map[task.ID][]gate.Gate
}

func (d *taskDecisions) Of(ctx context.Context, e task.Task) ([]decision.Found, error) {
	if d.open == nil {
		d.open = map[task.ID][]gate.Gate{}
		if d.gates != nil {
			gs, err := d.gates.Where(ctx, gates.Query{Statuses: []gate.Status{gate.Open}})
			if err != nil {
				return nil, err
			}
			for _, g := range gs {
				d.open[g.Task] = append(d.open[g.Task], g)
			}
		}
	}
	var sources []decision.Source
	for _, u := range e.LinkedUnits() {
		signals, situation, moved, ok, err := d.s.LinkState(ctx, u)
		if err != nil {
			return nil, err
		}
		if ok {
			sources = append(sources, decision.Source{Unit: u, Situation: situation, Signals: signals, Since: moved})
		}
	}
	return d.m.Of(d.open[e.ID], sources), nil
}
