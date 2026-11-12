package factdrafts_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	factdrafts "github.com/wloophq/wloop/internal/adapter/facts/drafts"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/outbox/outboxtest"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type at struct{ t time.Time }

func (c at) Now() time.Time { return c.t }

// staleHead refuses every post as the GitHub publisher does when the live head moved past the basis.
type staleHead struct{}

func (staleHead) Publish(context.Context, draft.Draft) (draft.Evidence, error) {
	return draft.Evidence{}, fmt.Errorf("%w: head c2, basis c1", outbox.ErrStale)
}

func (staleHead) Verify(context.Context, draft.Draft, time.Time) (draft.Evidence, bool, error) {
	return draft.Evidence{}, false, nil
}

func TestStaleFactFromPublisherRefusalAfterRestart(t *testing.T) {
	t.Skip("T3/T5: runs once the outbox stores a stale refusal with the prefix factdrafts.StalePrefix")
	ctx := context.Background()
	clock := at{time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)}
	path := filepath.Join(t.TempDir(), "state", "store.db")
	db, err := sqlite.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	pr7 := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	st := db.Gates(clock.Now)
	gs := outboxtest.GatesOn(st, st, clock.Now)
	svc := gs.Bind(&outbox.Service{Store: db.Drafts(), Clock: clock,
		Effects: func(unit.ProviderID) (port.Effects, error) { return staleHead{}, nil }})
	d, err := svc.Propose(ctx, outbox.Proposal{Unit: pr7, Kind: draft.Kind("reply"), Text: "the review", Origin: draft.Origin{Kind: draft.OriginTurn, TurnSeq: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if err := gs.Approve(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	svc.Send(ctx)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	facts, err := factdrafts.Provider{Drafts: reopened.Drafts()}.Facts(ctx, pr7)
	if err != nil {
		t.Fatal(err)
	}
	if facts[factdrafts.FactFailed] != true || facts[factdrafts.FactStale] != true {
		t.Fatalf("facts %v after the publisher refused the draft stale", facts)
	}
}
