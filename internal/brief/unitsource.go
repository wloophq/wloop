package brief

import (
	"context"
	"encoding/json"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// UnitInput is what a brief of a unit reads: the text the prompt carries and
// the provider's own parts, which brief_runs.input stores.
type UnitInput struct {
	// Text is the unit's own text the prompt's data block holds.
	Text string
	// Bytes is the whole input's size, 0 when unknown; Cut says Text is cut.
	Bytes int
	Cut   bool
	// Parts are the provider's parts as a JSON object, such as GitHub's
	// changed files.
	Parts json.RawMessage
}

// At is what a unit is at: its revision in the provider's own format, as
// brief_runs.revision stores it, and the parts of its input a staleness
// rule reads, as brief_runs.input stores them.
type At struct {
	Revision string
	Parts    json.RawMessage
}

// Written is what a stored brief was made from and how its last attempt
// went, as brief_runs and unit_briefs keep it.
type Written struct {
	// At is what the unit was at when the brief in use was made.
	At      At
	Version string
	// Error is the last attempt's error, empty when it went ok.
	Error       string
	Failures    int
	AttemptedAt time.Time
	// AttemptOf is AttemptKey of the last attempt.
	AttemptOf string
}

// BriefSource is what the brief queue asks a provider about its units. A
// provider without one answers "without" to the intakes of its units.
type BriefSource interface {
	// Input reads what a brief of u reads now.
	Input(ctx context.Context, u unit.Unit) (UnitInput, error)
	// Revision is what u is at now, read without the input.
	Revision(ctx context.Context, u unit.Unit) (At, error)
	// Describe is in, one line, as the prompt's header names it.
	Describe(in UnitInput) string
	// Stale says whether the brief w holds of a unit now at cur is due
	// again at now, version being the brief's current version.
	Stale(w Written, cur At, version string, now time.Time) bool
	// AttemptKey names what an attempt at version on a unit at cur is made
	// from, so a failure counts against that attempt only.
	AttemptKey(version string, cur At) string
}
