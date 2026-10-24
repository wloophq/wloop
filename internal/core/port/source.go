package port

import (
	"context"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// Stub is one native object's change marker. A unit is not always one
// object: a DM object may hold several topic units, found in Read.
type Stub struct {
	Object  string // the source's native id: "owner/repo#12" for github, a channel and thread ts for a thread
	Version string // github: updatedAt|state|draft|head|checks|failed names; thread: latest_reply
	Light   Facts  // facts the stub already carries (github: mergeable, review requests, decision); nil when none
}

// Facts is a source's typed payload; it may name units not seen before.
type Facts interface{ Units() []unit.Unit }

// Source reads one provider's objects. It never writes the store.
type Source interface {
	Provider() unit.ProviderID
	Discover(ctx context.Context, cursor string) (stubs []Stub, next string, err error)
	Stubs(ctx context.Context, objects []string) ([]Stub, error)
	Read(ctx context.Context, objects []string) (Facts, error)
	Viewer(ctx context.Context) (string, error)
}

// SourceStore is the source's own persistence, in its source tables. Only
// ingest calls it.
type SourceStore interface {
	// Watched are the objects to stub-check every run with their stored versions;
	// an object whose detail is older than its max age has Version "".
	Watched(ctx context.Context) ([]Stub, error)
	Versions(ctx context.Context, objects []string) (map[string]string, error)
	Cursor(ctx context.Context) (string, error)
	// Save stores light facts, then read facts (the read wins for one object),
	// then the cursor. It runs on every pass, so read may be nil and light empty;
	// it writes and notes the log only for what differs.
	Save(ctx context.Context, light []Facts, read Facts, cursor string, at time.Time) error
	// See stamps the objects as seen at at; notes nothing.
	See(ctx context.Context, objects []string, at time.Time) error
}
