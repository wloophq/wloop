package daemon

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
)

// handleOutbox serves the Outbox's drafts, read as GET Drafts reads them, grouped by why they were drafted,
// each with the asks of its task and the items it is about.
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
	ev := s.taskViews(r.Context(), s.deps.Tasks, false)
	ctx := whereBy(r.Context(), time.Now().Add(s.whereBudget()))
	out := make([]api.OutboxDraft, 0, len(ps))
	for _, p := range ps {
		d := s.draftDetail(ctx, p.Draft, p.Gate, ev.unitItems())
		c, err := ev.draftContext(ctx, d)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, api.OutboxDraft{Draft: d, Context: c})
	}
	writeJSON(w, outboxPage(s.loaded(), out))
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

// outboxGroupKey is what a draft's group is: its workflow's, else, for one a person did not write, its purpose's,
// else the group of the drafts written by hand.
type outboxGroupKey struct{ workflow, purpose string }

func groupOf(d api.Draft) outboxGroupKey {
	switch {
	case d.Origin.Workflow != "":
		return outboxGroupKey{workflow: d.Origin.Workflow}
	case d.Origin.Kind == string(draft.OriginUser):
		return outboxGroupKey{}
	}
	return outboxGroupKey{purpose: d.Purpose}
}

// outboxPage groups ds, keeping their order inside each group, and heads each group with l's words: a workflow's
// [outbox], a purpose's word of gates.toml, or gates.toml's words for the drafts written by hand. The largest
// group comes first, then by title.
func outboxPage(l *intentload.Loaded, ds []api.OutboxDraft) api.OutboxPage {
	workflows := map[string]workflow.Workflow{}
	for _, w := range l.Workflows {
		workflows[w.Name] = w
	}
	words := l.Gates.Drafts
	byKey := map[outboxGroupKey]int{}
	page := api.OutboxPage{Groups: []api.OutboxGroup{}}
	for _, d := range ds {
		k := groupOf(d.Draft)
		i, ok := byKey[k]
		if !ok {
			g := api.OutboxGroup{Workflow: k.workflow, Purpose: k.purpose}
			switch w := workflows[k.workflow]; {
			case k.workflow != "":
				g.Title, g.About = cmp.Or(w.Outbox.Title, w.Summary, k.workflow), w.Outbox.About
			case k.purpose != "":
				g.Title = cmp.Or(words.Purposes[k.purpose], k.purpose)
			default:
				g.Title, g.About = words.ByHand, words.ByHandAbout
			}
			i = len(page.Groups)
			byKey[k] = i
			page.Groups = append(page.Groups, g)
		}
		page.Groups[i].Drafts = append(page.Groups[i].Drafts, d)
	}
	slices.SortStableFunc(page.Groups, func(a, b api.OutboxGroup) int {
		return cmp.Or(len(b.Drafts)-len(a.Drafts), strings.Compare(a.Title, b.Title), strings.Compare(a.Workflow, b.Workflow), strings.Compare(a.Purpose, b.Purpose))
	})
	return page
}
