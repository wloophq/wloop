package brief

import (
	"context"
	"encoding/json"
	"time"

	dinv "github.com/wloophq/wloop/internal/domain/inventory"
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

// BriefSource is what the brief queue asks a provider about its units. A
// provider without one answers "without" to the intakes of its units.
type BriefSource interface {
	// Input reads what a brief of u reads now.
	Input(ctx context.Context, u unit.Unit) (UnitInput, error)
	// Revision is what u is at now, in the provider's own format; a brief
	// whose stored revision differs was written before u changed.
	Revision(ctx context.Context, u unit.Unit) (string, error)
	// Describe is in, one line, as the prompt's header names it.
	Describe(in UnitInput) string
	// Stale says whether the brief rec holds of u is due again at now.
	Stale(rec dinv.BriefRecord, u unit.Unit, now time.Time) bool
	// AttemptKey names what an attempt at revision was made from, so a
	// failure counts against that revision only.
	AttemptKey(rec dinv.BriefRecord, revision string) string
}
