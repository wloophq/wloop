package source_test

import (
	"context"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/source"
)

func TestItemsNeedCard(t *testing.T) {
	m := complete()
	m.Items = func(api.Row) []items.Item { return nil }
	if err := m.Valid(); err == nil || !strings.Contains(err.Error(), "card") {
		t.Fatalf("items without a card: %v", err)
	}
	m.Card = func(context.Context, api.Row) (card.Card, error) { return card.Card{}, nil }
	if err := m.Valid(); err != nil {
		t.Fatalf("items with a card: %v", err)
	}
}

type checkouts struct{}

func (checkouts) Checkout(context.Context, unit.Unit) (source.Checkout, bool, error) {
	return source.Checkout{}, false, nil
}

func TestCapabilitiesListWhatAModuleOffers(t *testing.T) {
	m := source.Module{ID: "notes"}
	if got := m.Capabilities(); len(got) != 0 {
		t.Fatalf("a bare module offers %v", got)
	}
	m.Checkouts, m.Reacts = checkouts{}, true
	m.Items = func(api.Row) []items.Item { return nil }
	if got := strings.Join(m.Capabilities(), ","); got != "checkouts,reacts,items" {
		t.Fatalf("capabilities %s", got)
	}
}
