package home

import (
	"context"
	"slices"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/decision"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// The kinds of a queue item's primary action.
const (
	ActionGate   = "gate"   // resolve the item's gate with the intent ID
	ActionAction = "action" // run the catalog action ID on the item's unit
	ActionOpen   = "open"   // open the item's unit at its source
	ActionRetry  = "retry"  // retry the delivery of the draft of the item's gate, which a person approved already
)

// Queue is a decision kind's items, the oldest decision first, and View, the view its items are drawn as rows of.
type Queue struct {
	Kind         decision.Kind
	View         task.View
	Title, Empty string
	Items        []Item
}

// Item is one decision: a task's open gate (Found.Gate), or a state of one of its units (Found.Unit, with
// Situation how the unit's situation reads). Word is what one such decision is, as the counters word it, and
// Tone its kind's.
type Item struct {
	Task      task.Task
	Found     decision.Found
	Situation string
	Primary   Action
	Word      string
	Tone      string
}

// Action is an item's primary action.
type Action struct {
	Kind, ID string
}

// Queue is kind k's items over the tasks of its view that keep takes (every one when nil).
func (s Service) Queue(ctx context.Context, k decision.Kind, keep coretasks.Keep) (Queue, error) {
	kw := s.Words.Kind(k)
	sets, err := s.Lists.Sets(ctx, []task.View{kw.View}, keep)
	if err != nil {
		return Queue{}, err
	}
	q := Queue{Kind: k, View: kw.View, Title: kw.Queue, Empty: kw.Empty}
	ds, err := s.decisions(ctx, k, sets[kw.View])
	if err != nil {
		return Queue{}, err
	}
	for _, d := range ds {
		q.Items = append(q.Items, s.item(kw, d.task, d.found))
	}
	slices.SortStableFunc(q.Items, func(a, b Item) int { return a.Found.Since.Compare(b.Found.Since) })
	if p, ok := onePlugin(q.Items); ok {
		pk := s.Words.Plugin(p).Kinds[k]
		if pk.Queue != "" {
			q.Title = pk.Queue
		}
	}
	return q, nil
}

// QueueAll is the items of every kind that keep takes (every one when nil): the kinds in order, a kind order
// leaves out after the listed ones, each kind's items as its Queue lists them.
func (s Service) QueueAll(ctx context.Context, order []decision.Kind, sort decision.Sort, keep coretasks.Keep) ([]Item, error) {
	var out []Item
	for _, k := range decision.Ordered(order) {
		q, err := s.Queue(ctx, k, keep)
		if err != nil {
			return nil, err
		}
		out = append(out, q.Items...)
	}
	if sort == decision.SortNewest {
		slices.SortStableFunc(out, func(a, b Item) int { return b.Found.Since.Compare(a.Found.Since) })
	}
	return out, nil
}

type taskDecision struct {
	task  task.Task
	found decision.Found
}

// decisions are the decisions of kind k ts wait on, in ts's order: what a queue lists and its home counter counts.
func (s Service) decisions(ctx context.Context, k decision.Kind, ts []task.Task) ([]taskDecision, error) {
	var out []taskDecision
	for _, e := range ts {
		found, err := s.Decisions.Of(ctx, e)
		if err != nil {
			return nil, err
		}
		for _, f := range found {
			if f.Kind == k {
				out = append(out, taskDecision{e, f})
			}
		}
	}
	return out, nil
}

func (s Service) item(kw KindWords, e task.Task, f decision.Found) Item {
	it := Item{Task: e, Found: f, Word: s.noun(f.Unit.Provider, kw.Noun).For(s.Words.Form(1)), Tone: kw.Tone}
	if f.Gate != nil && f.Gate.Status != gate.Open {
		it.Primary = Action{Kind: ActionRetry}
		return it
	}
	if f.Gate != nil {
		it.Primary = Action{Kind: ActionGate, ID: primaryIntent(kw.Primary, f.Gate.Intents)}
		return it
	}
	pw := s.Words.Plugin(f.Unit.Provider)
	it.Situation = pw.Situations[f.Situation]
	if it.Situation == "" {
		it.Situation = f.Situation
	}
	switch p := pw.Primary[f.Kind]; p {
	case "", decision.Open:
		it.Primary = Action{Kind: ActionOpen}
	default:
		it.Primary = Action{Kind: ActionAction, ID: p}
	}
	return it
}

// primaryIntent is want when the gate offers it, else the gate's first intent.
func primaryIntent(want string, intents []string) string {
	if want != "" && slices.Contains(intents, want) {
		return want
	}
	if len(intents) == 0 {
		return ""
	}
	return intents[0]
}

// onePlugin is the plugin every item's unit is of; false when the items are of none or of several.
func onePlugin(items []Item) (unit.ProviderID, bool) {
	var p unit.ProviderID
	for i, it := range items {
		q := it.Found.Unit.Provider
		if q == "" || (i > 0 && q != p) {
			return "", false
		}
		p = q
	}
	return p, p != ""
}
