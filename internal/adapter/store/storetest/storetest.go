// Package storetest opens stores for tests: empty, or seeded with a
// deterministic history the size of the user's live store.
package storetest

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/inventory/inventorytest"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Sizes is the shape of a seeded store.
type Sizes struct {
	PRs, OpenPRs           int // pull requests, of which open
	Events                 int // events over all pull requests, in EventMix's proportions
	TurnPRs, Turns         int // pull requests with a turn book, and turns over them
	Sessions, OpenSessions int // agent terminal sessions, of which never closed
	LinkedPRs              int // pull requests linked to a workspace
	AgentLinkedPRs         int // of those, with an agent session in the link
	BriefRuns              int
}

// Live are the sizes of a store in daily use.
var Live = Sizes{PRs: 212, OpenPRs: 70, Events: 8200, TurnPRs: 49, Turns: 88,
	Sessions: 146, OpenSessions: 138, LinkedPRs: 57, AgentLinkedPRs: 13, BriefRuns: 236}

// EventMix is the snapshot's share for each kind it lists, plus a synthetic
// tail, normalized to sum to 1. The seeder rounds and gives the remainder
// to "review"; opened, merged, closed, brief_ended and the session kinds
// follow from the seeded rows instead.
var EventMix = normalized(map[string]float64{
	"review": .18, "push": .17, "comment": .07, "check": .06, "review_requested": .06,
	"session_idle": .05, "session_active": .05, "situation": .03, "brief_ended": .029,
	"opened": .025, "followed": .024, "status": .022, "round_sent": .02, "unfollowed": .02,
	"guidance": .018, "round_pending": .017, "review_request_removed": .015, "merged": .012,
	"round_skipped": .012, "ready": .01, "nudged": .01, "round_held": .008, "muted": .007,
	"unmuted": .006, "closed": .004, "round_blocked": .003, "round_unblocked": .003, "draft": .003,
})

func normalized(m map[string]float64) map[string]float64 {
	var sum float64
	for _, v := range m {
		sum += v
	}
	out := make(map[string]float64, len(m))
	for k, v := range m {
		out[k] = v / sum
	}
	return out
}

// Fixture is an open store and what was seeded into it.
type Fixture struct {
	DB        *sqlite.DB
	Inventory *inventory.Store
	Turns     *sqlite.Store
	Rounds    *sqlite.Rounds
	Log       *change.Log
	// Now is the clock the seed used; Inventory dates its own events by it.
	Now          time.Time
	Open, Closed []pr.Ref    // sorted
	WithTurns    []unit.Unit // sorted
	Handles      []string    // session handles, open ones first
}

var seedNow = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

// Empty opens a new store under t.TempDir.
func Empty(t testing.TB) *Fixture {
	t.Helper()
	f, err := open(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.DB.Close() })
	return f
}

type image struct {
	once  sync.Once
	bytes []byte
	err   error
}

var (
	imagesMu sync.Mutex
	images   = map[Sizes]*image{}
)

// Seeded opens a copy of a store seeded with s. The seed runs once per test
// binary and Sizes, through the store's own write methods only; each call
// writes the seeded file into t.TempDir. Same Sizes, same rows, byte for
// byte.
func Seeded(t testing.TB, s Sizes) *Fixture {
	t.Helper()
	img, err := imageOf(s)
	if err != nil {
		t.Fatalf("seeding %+v: %v", s, err)
	}
	path := filepath.Join(t.TempDir(), "store.db")
	if err := os.WriteFile(path, img, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.DB.Close() })
	l := layoutOf(s)
	f.Open, f.Closed, f.WithTurns, f.Handles = l.open, l.closed, l.withTurns, l.handles
	return f
}

func imageOf(s Sizes) ([]byte, error) {
	imagesMu.Lock()
	img, ok := images[s]
	if !ok {
		img = &image{}
		images[s] = img
	}
	imagesMu.Unlock()
	img.once.Do(func() { img.bytes, img.err = build(s) })
	return img.bytes, img.err
}

func open(path string) (*Fixture, error) {
	log := change.NewLog(change.DefaultWindow, nil)
	db, err := sqlite.Open(path, log)
	if err != nil {
		return nil, err
	}
	inv := inventory.New(db)
	inv.UseClock(func() time.Time { return seedNow })
	inventorytest.Of(inv)
	return &Fixture{
		DB:        db,
		Inventory: inv,
		Turns:     sqlite.New(db),
		Rounds:    sqlite.NewRounds(db),
		Log:       log,
		Now:       seedNow,
	}, nil
}
