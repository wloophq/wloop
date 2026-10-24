package port

import (
	"context"
	"time"

	"github.com/wloophq/wloop/internal/domain/link"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Linker is optional on Facts: mechanical cross-source links found in the facts.
type Linker interface{ Links() []link.Link }

type LinkStore interface {
	// ReplaceFacts replaces the Origin facts links whose From is in from; notes TopicLinks.
	ReplaceFacts(ctx context.Context, from []unit.Unit, links []link.Link, at time.Time) error
	// Put upserts one link of origin extraction or user, keyed by (From, To, Kind, Item); notes TopicLinks.
	Put(ctx context.Context, l link.Link) error
	From(ctx context.Context, u unit.Unit) ([]link.Link, error)
	To(ctx context.Context, units []unit.Unit) ([]link.Link, error) // sorted by From, then At
}
