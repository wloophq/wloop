package slack

import (
	"context"

	"github.com/wloophq/wloop/plugins/slack/unit"
)

type Segments interface {
	// UnitsFor returns the segments covering ts; empty until segmented. Callers pass a
	// reply's thread_ts, so a reply follows its parent's segment.
	UnitsFor(ctx context.Context, conv, ts string) ([]unit.Unit, error)
	Range(ctx context.Context, u unit.Unit) (conv, fromTS, toTS string, err error) // toTS "" while open
}

// Span is a DM segment's range; ID "" asks for a new segment; ToTS "" is open.
type Span struct{ ID, FromTS, ToTS string }

type SegmentWriter interface {
	// Apply replaces conv's spans from the first span's FromTS on. A kept ID moves its range;
	// a new ID is minted once and never reused. It returns the units of every span.
	Apply(ctx context.Context, team, conv string, spans []Span) ([]unit.Unit, error)
}
