package readmodel

import (
	"context"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/intake"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
)

// RowDeps are what the daemon supplies to any provider's rows. Books,
// Sessions, Intakes and Intake may be nil: no turns, no sessions, no intakes.
type RowDeps struct {
	Books    Books
	Sessions Sessions
	// Intakes are the open intakes, the oldest first.
	Intakes func(ctx context.Context) ([]intake.Intake, error)
	// Intake is a row's view of an open intake, from its unit's brief record
	// (found false: none); attempt is the brief attempt the intake waits on.
	Intake   func(i intake.Intake, rec dinv.BriefRecord, found bool, attempt func() (int, error)) api.Intake
	Settings func() RowSettings
	Logf     func(format string, args ...any)
}

// RowSettings is what every row reads of the configuration, the profile and
// the daemon, read once per load.
type RowSettings struct {
	Cfg    config.Config
	Loaded *intentload.Loaded
	GateOn bool
	// Forceable is the last remove refusal of a unit that force gets past,
	// "" for none.
	Forceable func(unit.Unit) string
}

// Books reads turn books.
type Books interface {
	Books(ctx context.Context, units ...unit.Unit) (map[unit.Unit]turn.Book, error)
}

// Sessions reads the managed sessions.
type Sessions interface {
	// Sessions lists the sessions not closed.
	Sessions(ctx context.Context) ([]turn.Session, error)
	Session(ctx context.Context, handle string) (turn.Session, bool, error)
}
