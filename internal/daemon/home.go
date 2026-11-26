package daemon

import (
	"context"
	"net/http"
	"slices"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/home"
	"github.com/wloophq/wloop/internal/core/outbox"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/decision"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/i18n"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/keys"
)

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	g, ok := s.tasks(w)
	if !ok {
		return
	}
	ctx, ok := s.queuePass(r.Context(), w, g)
	if !ok {
		return
	}
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
		if sec.Queue {
			items, err := s.queueItems(withPlanReads(ctx), g, sec.Items)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			hs.Items, hs.Empty = items, s.loaded().Decisions.EmptyAll
		}
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
	return home.Service{Lists: g, Words: homeWords{l}, Decisions: &taskDecisions{s: s, m: l.Decisions.Map}, Serves: queueServes{s}}
}

// queueServes keeps the queue items whose gate's subject reads (servable).
type queueServes struct{ s *Server }

func (q queueServes) Servable(ctx context.Context, its []home.Item) ([]home.Item, error) {
	return q.s.servable(ctx, its)
}

// waiting is what the gates and the drafts make the tasks wait on; in a pass, read once.
func (s *Server) waiting(ctx context.Context) (outbox.Waiting, error) {
	var r outbox.WaitingReads
	if rd := s.deps.GateReader; rd != nil {
		r.Where = rd.Where
		if svc := s.deps.Gates; svc != nil {
			r.Pinned = svc.Pinned
		}
		if o := s.deps.Outbox; o != nil {
			r.Drafts = o
		}
	}
	return outbox.ReadWaiting(ctx, r)
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
		PerSentence: d.PerSentence, CaptureHint: d.CaptureHint, Order: w.l.Decisions.Order, Sort: w.l.Decisions.Sort}
	for _, c := range d.Counters {
		v, _ := w.l.Views.View(c.View)
		p.Counters = append(p.Counters, home.Counter{ID: c.ID, Kind: c.Kind, View: task.View(c.View), Noun: home.Forms(c.Noun), Clause: home.Forms(c.Clause),
			Queue: c.Kind == "" && v.Layout == builtin.ViewLayoutQueue})
	}
	for _, sec := range d.Sections {
		v, _ := w.l.Views.View(sec.View)
		p.Sections = append(p.Sections, home.Section{ID: sec.ID, View: task.View(sec.View), Label: v.Label, Time: sec.Time, Limit: sec.Limit,
			Queue: v.Layout == builtin.ViewLayoutQueue})
	}
	return p
}

func (w homeWords) Kind(k decision.Kind) home.KindWords {
	t := w.l.Decisions.Kinds[k]
	return home.KindWords{View: task.View(t.View), Noun: home.Forms(t.Noun), Clause: home.Forms(t.Clause), Queue: t.Queue, Empty: t.Empty, Primary: t.Primary, Tone: t.Tone}
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

// taskDecisions are the decisions a task waits on: what its gates and drafts make it wait on (outbox.Waiting),
// read once per pass, and, while it is open, its units' situations and signals as the read model holds them.
type taskDecisions struct {
	s *Server
	m decision.Map
}

func (d *taskDecisions) Of(ctx context.Context, e task.Task) ([]decision.Found, error) {
	w, err := d.s.waiting(ctx)
	if err != nil {
		return nil, err
	}
	var sources []decision.Source
	var units []unit.Unit
	if e.Open() {
		units = e.LinkedUnits()
	}
	for _, u := range units {
		signals, situation, moved, ok, err := d.s.LinkState(ctx, u)
		if err != nil {
			return nil, err
		}
		if ok {
			sources = append(sources, decision.Source{Unit: u, Situation: situation, Signals: signals, Since: moved})
		}
	}
	return w.Of(d.m, e.ID, sources), nil
}
