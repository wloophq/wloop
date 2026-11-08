package daemon

import (
	"context"
	"slices"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel/cards"
)

// CardsJob keeps the cards and derived items current with the rows.
type CardsJob interface {
	// Changed must not block: the read model's publish waits on it.
	Changed(c api.Change)
	Run(ctx context.Context, rows cards.Rows)
}

func (s *Server) fanOutRows(c api.Change) {
	if s.deps.CardsJob != nil {
		s.deps.CardsJob.Changed(c)
	}
}

// withCard adds u's card and items to d. A disabled
// plugin's unit without a row whose card is stored is found from the card
// alone.
func (s *Server) withCard(ctx context.Context, u unit.Unit, d *api.Detail, found bool) (bool, error) {
	if s.deps.Snapshot == nil {
		return s.readCard(ctx, u, d, found)
	}
	err := s.deps.Snapshot.Snapshot(ctx, func(ctx context.Context) error {
		var err error
		found, err = s.readCard(ctx, u, d, found)
		return err
	})
	return found, err
}

func (s *Server) readCard(ctx context.Context, u unit.Unit, d *api.Detail, found bool) (bool, error) {
	if s.deps.Cards != nil {
		got, err := s.deps.Cards.Get(ctx, []unit.Unit{u})
		if err != nil {
			return false, err
		}
		if snap, ok := got[u]; ok {
			d.Card = &snap
		}
	}
	if !found && (d.Card == nil || s.pluginEnabled(u.Provider)) {
		return false, nil
	}
	d.Items = []items.Item{}
	if s.deps.Items != nil {
		its, err := s.deps.Items.Items(ctx, u)
		if err != nil {
			return false, err
		}
		if its != nil {
			d.Items = its
		}
	}
	ties, err := s.itemTies(ctx, u, d.Items)
	if err != nil {
		return false, err
	}
	d.ItemTies = ties
	if !found {
		c := d.Card.Card
		d.Row = api.Row{Unit: u, Link: c.Link, Label: u.Label(), Title: c.Title, Views: []string{}, Facets: map[string][]string{}, Actions: []api.ActionState{}}
		d.Events = []api.Event{}
	}
	return true, nil
}

// itemTies are whom each of its waits on and the task that holds it, an open task before a closed one.
func (s *Server) itemTies(ctx context.Context, u unit.Unit, its []items.Item) (map[string]api.ItemTie, error) {
	out := make(map[string]api.ItemTie, len(its))
	if len(its) == 0 {
		return out, nil
	}
	var (
		me      task.Me
		holders []task.Task
	)
	if s.deps.Tasks != nil {
		me = s.deps.Tasks.Me()
		var err error
		if holders, err = s.deps.Tasks.HoldersOf(ctx, u); err != nil {
			return nil, err
		}
	}
	for _, it := range its {
		tie := api.ItemTie{WaitsOn: make([]api.ItemPerson, 0, len(it.WaitingOn))}
		for _, p := range it.WaitingOn {
			tie.WaitsOn = append(tie.WaitsOn, api.ItemPerson{Key: p.Key, Name: p.Name, Me: p.Is(me)})
		}
		for _, e := range holders {
			ref := task.ItemRef{Unit: u, Item: it.ID}
			if slices.Contains(e.Items, ref) || slices.Contains(e.Resolved, ref) {
				tie.Task, tie.TaskTitle = string(e.ID), e.Title
				break
			}
		}
		out[it.ID] = tie
	}
	return out, nil
}
