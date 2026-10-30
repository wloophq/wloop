// Package link holds the cross-source links between units.
package link

import (
	"time"

	"github.com/wloophq/wloop/plugins/slack/unit"
)

// Kind is the one link-kind enum; extraction ops use these values as link_kind.
type Kind string

const (
	Mention Kind = "mention" // shown, never resolves
	Review  Kind = "review"  // resolved by a review on To by an assignee of Item (any reviewer when none), mooted when To merges or closes
	Merge   Kind = "merge"   // resolved (done) when To merges; mooted when To closes unmerged
)

// Valid says whether k is one of the link kinds.
func (k Kind) Valid() bool {
	switch k {
	case Mention, Review, Merge:
		return true
	}
	return false
}

type Origin string

const (
	Facts      Origin = "facts"
	Extraction Origin = "extraction"
	User       Origin = "user"
)

type Link struct {
	From     unit.Unit   `json:"from"`
	To       unit.Unit   `json:"to"`
	Kind     Kind        `json:"kind"`
	Item     string      `json:"item,omitempty"` // item id on From; "" links the unit only and resolves nothing
	Evidence unit.Anchor `json:"evidence"`       // the message or event that names To
	Origin   Origin      `json:"origin"`
	At       time.Time   `json:"at"`
}

type EventKind string

const (
	EventReview EventKind = "review"
	EventMerged EventKind = "merged"
	EventClosed EventKind = "closed"
)

// Event is a provider-neutral event on a unit, read by the resolver.
type Event struct {
	Unit   unit.Unit
	Kind   EventKind
	Actor  string // person key, "github:<login>"
	At     time.Time
	Anchor unit.Anchor // Ref "review:<id>", "merged", "closed"
}
