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
)

// open is the primary a plugin's layer names to open its unit at its source.
const open = "open"

// Queue is a decision kind's items, the oldest decision first.
type Queue struct {
	Kind         decision.Kind
	Title, Empty string
	Items        []Item
}

// Item is one decision: a task's open gate (Gate), or a state of one of its units (Found.Unit, with Situation
// how the unit's situation reads).
type Item struct {
	Task      task.Task
	Found     decision.Found
	Gate      *gate.Gate
	Situation string
	Primary   Action
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
	q := Queue{Kind: k, Title: kw.Queue, Empty: kw.Empty}
	for _, e := range sets[kw.View] {
		found, err := s.Decisions.Of(ctx, e)
		if err != nil {
			return Queue{}, err
		}
		for _, f := range found {
			if f.Kind != k {
				continue
			}
			it, err := s.item(ctx, kw, e, f)
			if err != nil {
				return Queue{}, err
			}
			q.Items = append(q.Items, it)
		}
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

func (s Service) item(ctx context.Context, kw KindWords, e task.Task, f decision.Found) (Item, error) {
	it := Item{Task: e, Found: f}
	if f.Gate != 0 {
		if s.Gates != nil {
			g, err := s.Gates.Get(ctx, f.Gate)
			if err != nil {
				return Item{}, err
			}
			it.Gate = &g
			it.Primary = Action{Kind: ActionGate, ID: primaryIntent(kw.Primary, g.Intents)}
		}
		return it, nil
	}
	pw := s.Words.Plugin(f.Unit.Provider)
	it.Situation = pw.Situations[f.Situation]
	if it.Situation == "" {
		it.Situation = f.Situation
	}
	switch p := pw.Primary[f.Kind]; p {
	case "", open:
		it.Primary = Action{Kind: ActionOpen}
	default:
		it.Primary = Action{Kind: ActionAction, ID: p}
	}
	return it, nil
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
