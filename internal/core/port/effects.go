package port

import (
	"context"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
)

// Effects publishes approved drafts of one provider. The core calls Publish
// only for a draft in state sending, once; it never retries.
type Effects interface {
	Publish(ctx context.Context, d draft.Draft) (draft.Evidence, error)
	// Verify says whether d, found in sending after a crash, was posted at
	// or after startedAt by the user, with text matching d.Text as converted.
	Verify(ctx context.Context, d draft.Draft, startedAt time.Time) (draft.Evidence, bool, error)
}

// Validator is optional: an Effects value that implements it is asked at
// proposal and before approval whether Target and Text would post.
type Validator interface {
	Validate(ctx context.Context, d draft.Draft) error
}

// Locator is optional: an Effects value that implements it names, for the user approving d, the
// conversation or person d posts to.
type Locator interface {
	Where(ctx context.Context, d draft.Draft) (string, error)
}

// Deliverer is optional: an Effects value that implements it says how it
// posts a draft whose proposer asked for delivery d, which the draft then
// carries, so its approval reads what the provider will show.
type Deliverer interface {
	Delivers(d draft.Delivery) draft.Delivery
}

// Placer is optional: an Effects value that implements it names, when a draft
// is proposed, or sent, with no target, where it posts, which the draft then
// stores as its Target; "" keeps the unit itself. An error refuses the draft.
type Placer interface {
	Place(ctx context.Context, d draft.Draft) (string, error)
}
