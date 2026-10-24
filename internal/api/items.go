package api

import (
	"time"

	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// ItemsResult is what the items ledger did with a turn record's ops.
type ItemsResult struct {
	Applied  []ItemApplied  `json:"applied"`
	Proposed []ItemProposal `json:"proposed"`
	Refused  []ItemRefused  `json:"refused"`
}

type ItemApplied struct {
	Op      items.Op   `json:"op"`
	Item    items.Item `json:"item"`
	Changed bool       `json:"changed"`
}

type ItemRefused struct {
	Index  int      `json:"index"` // position in the ops passed
	Op     items.Op `json:"op"`
	Reason string   `json:"reason"`
}

type ItemProposal struct {
	ID         int64            `json:"id"`
	Unit       unit.Unit        `json:"unit"`
	Item       string           `json:"item"`
	Op         items.Op         `json:"op"`
	Provenance items.Provenance `json:"provenance"`
	Reason     string           `json:"reason"`
	State      string           `json:"state"`
	At         time.Time        `json:"at"`
}
