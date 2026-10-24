package daemon

import (
	"context"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/items"
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
	if !found {
		c := d.Card.Card
		d.Row = api.Row{Unit: u, Link: c.Link, Label: u.Label(), Title: c.Title, Views: []string{}, Facets: map[string][]string{}, Actions: []api.ActionState{}}
		d.Events = []api.Event{}
	}
	return true, nil
}
