package views

import (
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/view"
)

// NewestOwnsTheCard keeps, of the units that share a card, only the newest
// unit's items; ties go to the greater unit. Items whose unit has no card
// stay.
func NewestOwnsTheCard(items []view.Item, cardOf func(unit.Unit) (string, bool)) []view.Item {
	owner := map[string]view.Item{}
	for _, it := range items {
		card, ok := cardOf(it.Unit)
		if !ok {
			continue
		}
		if cur, seen := owner[card]; !seen || it.At.After(cur.At) || (it.At.Equal(cur.At) && cur.Unit.Less(it.Unit)) {
			owner[card] = it
		}
	}
	out := make([]view.Item, 0, len(items))
	for _, it := range items {
		if card, ok := cardOf(it.Unit); ok && owner[card].Unit != it.Unit {
			continue
		}
		out = append(out, it)
	}
	return out
}
