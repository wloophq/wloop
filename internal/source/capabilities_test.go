package source_test

import (
	"context"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/items"
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
