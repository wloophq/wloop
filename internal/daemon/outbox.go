package daemon

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/domain/draft"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// handleOutbox serves the Outbox's drafts, read as GET Drafts reads them, each with the asks of its task, the
// items it is about, its draft facets' values and its search texts, in the order the sort of the Outbox view
// the query names (else the profile's first) gives them, and the draft facets with their heads.
func (s *Server) handleOutbox(w http.ResponseWriter, r *http.Request) {
	o := s.deps.Outbox
	if o == nil {
		writeActionError(w, errOutboxUnavailable)
		return
	}
	ps, err := o.Outbox(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	l := s.loaded()
	ev := s.taskViews(r.Context(), s.deps.Tasks, false)
	ctx := whereBy(r.Context(), time.Now().Add(s.whereBudget()))
	pins := s.openPins(r.Context())
	units := make([]unit.Unit, 0, len(ps))
	for _, p := range ps {
		units = append(units, p.Draft.Unit)
	}
	rows := ev.rowsOf(r.Context(), units)
	listed := make([]faced, 0, len(ps))
	for _, p := range ps {
		if p.Draft.State == draft.Held && pins[int64(p.Draft.ID)] != 0 {
			continue
		}
		d := s.draftDetail(ctx, p.Draft, p.Gate, ev.unitItems())
		c, err := ev.draftContext(ctx, d)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		where := rowWhere(l.Views.DraftFacets, rows[p.Draft.Unit].Row.Facets)
		people := mentioned(p.Draft.Text, d.Names)
		facts := draftViewFacts(p.Draft, s.publisherOf(p.Draft), where, people)
		facets := catalog.FactFacets(l.Views.DraftFacets, facts)
		if len(facets) == 0 {
			facets = nil
		}
		listed = append(listed, faced{api.OutboxDraft{Draft: d, Context: c, Facets: facets, Texts: draftTexts(append([]string{d.Text, d.Label, d.Title, d.Where, where}, people...)...)}, facts})
	}
	_ = catalog.SortBy(l.Views, outboxView(l.Views, r.URL.Query().Get("view")), listed, func(f faced) (workflow.Facts, error) { return f.facts, nil })
	out := make([]api.OutboxDraft, 0, len(listed))
	for _, f := range listed {
		out = append(out, f.draft)
	}
	facets, heads := outboxWords(l, listed)
	writeJSON(w, api.OutboxPage{Drafts: out, Facets: facets, Heads: heads})
}

// faced is a draft the Outbox serves with the facts its draft facets read.
type faced struct {
	draft api.OutboxDraft
	facts workflow.Facts
}

// outboxView is id when it is a view def draws in the outbox layout, else the first such view; "" for none.
func outboxView(def intentload.ViewsDef, id string) string {
	first := ""
	for _, v := range def.Views {
		if v.Layout != builtin.ViewLayoutOutbox {
			continue
		}
		if v.ID == id {
			return id
		}
		if first == "" {
			first = v.ID
		}
	}
	return first
}

// draftTexts are texts without the empty ones, each once.
func draftTexts(texts ...string) []string {
	var out []string
	for _, t := range texts {
		if t != "" && !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}

// draftContext is what d answers: the asks of its task, with the quotes its brief keeps, and its items.
func (ev *taskViewer) draftContext(ctx context.Context, d api.Draft) (api.DraftContext, error) {
	c := api.DraftContext{Asks: []api.AskView{}, Items: make([]api.OutboxItem, 0, len(d.ItemDetails))}
	for _, it := range d.ItemDetails {
		c.Items = append(c.Items, api.OutboxItem{ID: it.ID, Summary: it.Summary, State: it.State})
	}
	if d.Task == "" || ev.g == nil {
		return c, nil
	}
	e, ok, err := ev.taskOf(ctx, task.ID(d.Task))
	if err != nil || !ok {
		return c, err
	}
	var v api.TaskView
	if err := ev.brief(ctx, e, &v); err != nil {
		return c, err
	}
	if err := ev.asks(ctx, e, &v); err != nil {
		return c, err
	}
	if v.Asks != nil {
		c.Asks = v.Asks
	}
	return c, nil
}

// outboxWords are the draft facets, each with l's words for the values ds hold of those that read who proposed
// a draft, its workflow and its kind among its labels, and the heads of those values that say more, by facet id,
// then by value. A value the facet's values table words keeps that word. A kind takes the word of the plugin
// that posts the first draft whose plugin words it.
func outboxWords(l *intentload.Loaded, ds []faced) ([]api.FacetSpec, map[string]map[string]api.OutboxHead) {
	specs := catalog.DraftFacets(l.Views.DraftFacets)
	heads := map[string]map[string]api.OutboxHead{}
	for i, f := range l.Views.DraftFacets {
		var word func(value string, d faced) (string, api.OutboxHead)
		switch f.Fact {
		case "draft.proposer":
			word = func(v string, _ faced) (string, api.OutboxHead) { return proposerWords(l, v) }
		case "draft.workflow":
			word = func(v string, _ faced) (string, api.OutboxHead) { return workflowWords(l, v) }
		case "draft.kind":
			word = func(v string, d faced) (string, api.OutboxHead) {
				publisher, _ := d.facts["draft.provider"].(string)
				return l.UI[publisher].Drafts[v].Label, api.OutboxHead{}
			}
		default:
			continue
		}
		for _, d := range ds {
			for _, v := range d.draft.Facets[f.ID] {
				if _, ok := heads[f.ID][v]; ok {
					continue
				}
				title, h := word(v, d)
				if _, ok := specs[i].Labels[v]; !ok && title != "" {
					if specs[i].Labels == nil {
						specs[i].Labels = map[string]string{}
					}
					specs[i].Labels[v] = title
				}
				if h != (api.OutboxHead{}) {
					if heads[f.ID] == nil {
						heads[f.ID] = map[string]api.OutboxHead{}
					}
					heads[f.ID][v] = h
				}
			}
		}
	}
	if len(heads) == 0 {
		heads = nil
	}
	return specs, heads
}

// proposerWords are the title and head of v, a draft.proposer value: an intent's [outbox] words, else its
// label; a workflow's; a rule's word of gates.toml; or gates.toml's words for the drafts written by hand or by
// a session outside any intent's turn.
func proposerWords(l *intentload.Loaded, v string) (string, api.OutboxHead) {
	by, name, _ := strings.Cut(v, ":")
	switch by {
	case "intent":
		def, ok := l.Registry.Get(dintent.Name(name))
		if !ok {
			return "", api.OutboxHead{}
		}
		return cmp.Or(def.Outbox.Title, def.Label), api.OutboxHead{About: def.Outbox.About}
	case "workflow":
		return workflowWords(l, name)
	case "rule":
		return l.Gates.Drafts.Rules[name], api.OutboxHead{}
	case "user":
		return l.Gates.Drafts.ByHand, api.OutboxHead{About: l.Gates.Drafts.ByHandAbout}
	case "session":
		return l.Gates.Drafts.BySession, api.OutboxHead{About: l.Gates.Drafts.BySessionAbout}
	}
	return "", api.OutboxHead{}
}

// workflowWords are the title and head of the drafts of the workflow name: its [outbox] words, else its summary.
func workflowWords(l *intentload.Loaded, name string) (string, api.OutboxHead) {
	i := slices.IndexFunc(l.Workflows, func(w workflow.Workflow) bool { return w.Name == name })
	if i < 0 {
		return "", api.OutboxHead{}
	}
	w := l.Workflows[i]
	return cmp.Or(w.Outbox.Title, w.Summary), api.OutboxHead{About: w.Outbox.About, Workflow: name}
}
