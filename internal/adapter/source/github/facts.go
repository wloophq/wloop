package ghsource

import (
	factsgithub "github.com/wloophq/wloop/internal/adapter/facts/github"
	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/sweep"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/gitx"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/workspace"
)

// FactsDeps are what GitHub's workflow facts read in the host's process.
type FactsDeps struct {
	Reports readmodel.Reports
	Store   *inventory.Store
	WS      *workspace.Set
	Git     gitx.Git
	Cfg     config.Config
	Clock   port.Clock
	Intents func() *dintent.Registry
	Changes port.Changes
	Rounds  sweep.Rounds
	Books   sweep.Books
}

// FactsOf are GitHub's workflow facts over d: the plugin's reports under
// the host's overlays.
func FactsOf(d FactsDeps) *factsgithub.Provider {
	cfg := d.Cfg
	fd := factsgithub.Deps{
		Inputs:   ReportedInputs{Reports: d.Reports, Store: d.Store},
		Store:    d.Store,
		Snapshot: factsgithub.SnapshotOf(d.WS),
		Git:      factsgithub.LocalGit(d.Git),
		Cfg:      func() config.Config { return cfg },
		Clock:    d.Clock,
		Intents:  d.Intents,
		Reports:  d.Reports,
		Changes:  d.Changes,
		Rounds:   d.Rounds,
		Books:    d.Books,
	}
	return factsgithub.New(fd)
}
