package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/ingest"
	"github.com/wloophq/wloop/internal/core/links"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/card"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/placement"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/gitx"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/turn"
	"github.com/wloophq/wloop/internal/workspace"
)

// Module is one source as the daemon runs it.
type pluginModule struct {
	ID     unit.ProviderID
	Codec  unit.Codec
	Ingest ingest.Job
	// Rows builds the source's read model provider with the daemon's row deps.
	Rows func(d readmodel.RowDeps) readmodel.Provider
	// Facts are the source's workflow facts; nil until the source offers any.
	Facts automation.Units
	// Track reads a unit about to be tracked, wrapping a failed read in
	// port.ErrNotFound or port.ErrGitHub when it can tell which; nil tracks
	// the source's units unread.
	Track func(ctx context.Context, u unit.Unit) (port.TrackRead, error)
	// Untrack stops following a unit Track followed; set, the source keeps
	// its own follows and the daemon sends it every track and untrack of
	// its units.
	Untrack func(ctx context.Context, u unit.Unit) error
	// Slow is the source's slow job, which rebuilds what the ingestion job
	// keeps cheap; nil is none.
	Slow func(ctx context.Context) (summary, skipped string, err error)

	// The optional capabilities below are nil when absent.

	// Items are the derived items of a row: deterministic, stable ids, no I/O; the viewer comes from the row's payload.
	Items func(ctx context.Context, row api.Row) ([]items.Item, error)
	// Card is required when Items is set or the plugin's units carry items.
	Card func(ctx context.Context, row api.Row) (card.Card, error)
	// Identity is the user as this plugin knows them.
	Identity   func(ctx context.Context) (items.Person, error)
	UnitEvents links.EventReader
	CLI        *pluginCLI
	// Run is a long-running stream; the daemon runs it for an enabled plugin until ctx ends, restarting after 30 s on error.
	Run    func(ctx context.Context) error
	Health func(ctx context.Context) (api.SourceHealth, error)
	// Jobs are extra daemon jobs of the plugin.
	Jobs      []pluginJob
	Publisher port.Effects
	// Reacts says Publisher posts drafts of kind react.
	Reacts bool
	// Proposals is called by the proposals job; a provider with Proposals gets no generic stalled nudge.
	Proposals         func(ctx context.Context, now time.Time) ([]outbox.Proposal, error)
	Payload           turn.PayloadReader
	PlacementDefaults placement.Defaults
	PlacementHints    placement.Hints
	// OnPosted are the draft effects the plugin applies once a draft of its
	// units is sent, by effect name (builtin.Effects), such as unit.closed.
	OnPosted map[string]outbox.Effect
	// Checkouts says which repository a unit's work checks out.
	Checkouts unitCheckouts
	// Briefs is how the brief queue reads the plugin's units; nil when the
	// plugin's units are briefed by no queue.
	Briefs brief.BriefSource
	// Links resolves the units of other plugins the plugin's units link to.
	Links linkResolver
	// Detail says the cockpit shows a detail of the plugin's units.
	Detail bool
	// Resolve is the unit an address a person gives lands in, such as a
	// Slack message ref to its thread or direct message segment; ok is false
	// for an address that is not one of the plugin's.
	Resolve func(ctx context.Context, address string) (u unit.Unit, ok bool, err error)
}

// Checkout is the repository a unit's work checks out.
type unitCheckout struct {
	Repo string // owner/name
	// Branch is the unit's own branch; "" for the repository's default.
	Branch string
	// Head is the commit the unit's work is on; "" when unknown.
	Head string
}

// Checkouts is an optional capability of a plugin: the repository of a unit,
// which a worktree checks out; ok is false for a unit with none.
type unitCheckouts interface {
	Checkout(ctx context.Context, u unit.Unit) (c unitCheckout, ok bool, err error)
}

// LinkResolver is an optional capability of a plugin: the units of other
// plugins a link of its units names, such as a pull request a thread links.
type linkResolver interface {
	Resolve(ctx context.Context, url string) (u unit.Unit, ok bool, err error)
}

// Capabilities are what a plugin can do, as clients read them.
func (m pluginModule) Capabilities() []string {
	var out []string
	for _, c := range []struct {
		name string
		has  bool
	}{
		{api.CapabilityCheckouts, m.Checkouts != nil},
		{api.CapabilityBriefs, m.Briefs != nil},
		{api.CapabilityPublishes, m.Publisher != nil},
		{api.CapabilityReacts, m.Reacts},
		{api.CapabilityItems, m.Items != nil},
		{api.CapabilityDetail, m.Detail},
	} {
		if c.has {
			out = append(out, c.name)
		}
	}
	return out
}

// Plugin is a compiled-in plugin: its codec is registered even when it is disabled.
type compiledPlugin struct {
	ID unit.ProviderID
	// Name is how a person reads the plugin's service, such as "GitHub".
	Name  string
	Codec unit.Codec
	Build moduleBuild
}

type pluginJob struct {
	Name    string
	Every   time.Duration
	Timeout time.Duration // a run's limit; 0 is the daemon's default
	Run     func(ctx context.Context) (summary, skipped string, err error)
}

// CLI is a plugin's own command, run as <command> <Name> ... when Name is not a core command.
type pluginCLI struct {
	Name, Usage string
	Run         func(ctx context.Context, args []string, out, errw io.Writer) error
}

// Deps are the shared handles a source builds from. Briefs is nil when the
// brief is off, WS when no workspace manager is configured.
type moduleDeps struct {
	DB        *sqlite.DB
	Inventory *inventory.Store
	WS        *workspace.Set
	// Sink is the view sink whose columns WS reports as workspace statuses.
	Sink    string
	Turns   *turns.Service
	Briefs  *brief.Queue
	Changes *change.Log
	Clock   port.Clock
	Log     func(format string, args ...any)
	// Git reads the local checkouts; Agents are the agents by name and
	// Intents the loaded intents' registry, nil before a profile loads.
	Git     gitx.Git
	Agents  map[string]turns.Agent
	Intents func() *dintent.Registry
	// Units is the daemon's registry, built after the modules; Links the link store.
	Units func() *unit.Registry
	Links port.LinkStore
	// Host is the broker the host serves the module of provider id: the
	// host's events, marks and drafts the module writes and reads; nil
	// reaches them in DB.
	Host func(id unit.ProviderID) src.Host
	// Reports are what the source plugins last reported of their units;
	// nil reports none.
	Reports readmodel.Reports
	// Catalog is a plugin's views, facets and situations; nil serves the
	// bundled ones.
	Catalog func(id unit.ProviderID) readmodel.Presenter
}

// Build makes a source's module from the config and the shared handles.
type moduleBuild func(cfg config.Config, d moduleDeps) (pluginModule, error)

// Valid says why m cannot be registered: a codec, an ingestion job and rows
// of its own provider, and a card when it derives items.
func (m pluginModule) Valid() error {
	switch {
	case m.ID == "":
		return fmt.Errorf("source: a module needs a provider")
	case m.Codec == nil || m.Codec.Provider() != m.ID:
		return fmt.Errorf("source %s: a module needs the codec of its provider", m.ID)
	case m.Ingest.Prepare == nil || m.Ingest.Every <= 0:
		return fmt.Errorf("source %s: a module needs an ingestion job with an interval", m.ID)
	case m.Rows == nil:
		return fmt.Errorf("source %s: a module needs its rows", m.ID)
	case m.Items != nil && m.Card == nil:
		return fmt.Errorf("source %s: a module with items needs a card", m.ID)
	}
	return nil
}
