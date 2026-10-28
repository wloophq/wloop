package source

import (
	"context"
	"time"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// Broker is the host's side of the broker of one source plugin: what the
// plugin calls back. Each broker interface lives in its own file; a method
// the host does not serve yet answers ErrUnsupported.
type Broker struct {
	// ID is the plugin's id, the provider of its units.
	ID string
	// Manifest is the plugin's [source] table.
	Manifest *manifest.Source
	Units    Units
	// Snapshots keep what the plugin reports.
	Snapshots *Snapshots
	// Config is the configuration in force.
	Config func() config.Config
	// Note records a change in the host's change log; nil records none.
	Note func(t port.Topic, units ...unit.Unit)
	// Events are the host's events; nil serves neither Recorder.RecordEvents
	// nor History.
	Events EventLog
	// Marks are the host's handled marks of units; nil serves no SetMarks.
	Marks MarkStore
	// Drafts move drafts between units; nil serves no RehomeDrafts.
	Drafts DraftHome
	// Overrides are the host's overrides of units; nil serves neither
	// SetOverride nor RoundState.
	Overrides OverrideStore
	// Paused says a unit's rounds are held by a pause wider than its own
	// hold, such as its repository's; nil holds none.
	Paused func(ctx context.Context, u unit.Unit) (bool, error)
	// Briefs keep the briefs the plugin writes of its units, and Briefed
	// hears of each unit whose brief it wrote; nil Briefs serves no
	// PutBrief.
	Briefs  BriefStore
	Briefed func(u unit.Unit)
	// Now is the host's clock; PutBrief needs it.
	Now func() time.Time
}

// BriefStore keeps body, the brief JSON a plugin wrote of u at version, as
// the brief u uses since at.
type BriefStore interface {
	PutUnitBrief(ctx context.Context, u unit.Unit, version, body string, at time.Time) error
}

// OverrideStore keeps the host's overrides of units by name
// (port.Overrides); SetOverride writes set in one transaction while ifMatch
// holds.
type OverrideStore interface {
	Override(ctx context.Context, u unit.Unit) (map[string]string, error)
	SetOverride(ctx context.Context, u unit.Unit, set, ifMatch map[string]string) (written bool, err error)
}

// EventLog is the host's event log as a plugin writes and reads it.
type EventLog interface {
	RecordSourceEvents(ctx context.Context, evs ...port.Event) error
	UnitEvents(ctx context.Context, units []unit.Unit, kinds []string, since time.Time) ([]port.Event, error)
}

// MarkStore sets a unit's handled marks; a value "" removes one.
type MarkStore interface {
	SetMarks(ctx context.Context, u unit.Unit, marks map[string]string) error
}

// DraftHome moves the drafts of each unit of moves onto the unit it maps
// to, in one transaction, and counts them.
type DraftHome interface {
	RehomeDrafts(ctx context.Context, moves map[unit.Unit]unit.Unit) (int, error)
}

// Host is b as the SDK serves it to the plugin.
func (b *Broker) Host() src.Host {
	return src.Host{Reporter: reporter{b}, Ledger: ledger{b}, Reader: reader{b}, Gates: gates{b}, Recorder: recorder{b},
		Wakes: wakes{b}, Services: services{b}, History: history{b}}
}
