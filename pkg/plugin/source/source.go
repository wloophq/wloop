// Package source is the SDK of the source kind: a plugin that reports units
// of work from a service (pull requests, threads) and acts on them.
//
// A source plugin implements [Source] and the capability interfaces it has
// ([Ingester], [Presenter], [Publisher], [Lander], [Briefer], [Planner],
// [Tooler], [CLIRunner]), passes it to [New] and serves the result with
// [plugin.Serve]. The host reaches it through [Client], and serves the
// plugin's calls back through [ServeHost], split the same way into
// [Reporter], [Ledger], [Reader], [Gates], [Recorder], [Wakes] and
// [Services].
//
// Every value the host's context carries across the boundary is a field: the
// call's priority and meter scope travel as [Call] ([WithCall] on the host,
// [CallOf] in the plugin), the MCP session and turn in [ToolCall], the cost
// cap and meter scope in [AskRequest], and the session's draft rules come
// from [Reader.Session].
//
// Time is the host's: a plugin reports temporal [Value]s, which the host
// resolves with its clock whenever it reads them.
package source

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/wloophq/wloop/pkg/plugin"
)

// Kind is the source kind's name in plugin.toml and go-plugin.
const Kind plugin.Kind = "source"

// Source is what every source plugin implements. Start receives the host's
// broker; the plugin keeps it to report snapshots and call the host.
//
// In CLI mode the host runs the plugin binary for one [CLIRunner.RunCLI]
// without calling Start: the plugin has no broker and opens no store, which
// the daemon's process of the plugin owns.
type Source interface {
	Health(ctx context.Context) (plugin.Health, error)
	Start(ctx context.Context, req StartRequest) error
	Stop(ctx context.Context) error
	Notify(ctx context.Context, units []plugin.Unit, topics []plugin.Topic) error
}

// StartRequest is what a source plugin receives when the host starts it.
type StartRequest struct {
	// StateDir is the plugin's own state directory, which only it reads.
	StateDir string
	// Host is the host's broker. A broker interface the host does not serve
	// answers every call with [ErrUnsupported].
	Host Host
}

// Ingester reads the source: the host runs the manifest's jobs through
// Ingest, during which the plugin reports what changed; it tracks and
// untracks units a person follows, and resolves what a person or a link
// names.
type Ingester interface {
	Ingest(ctx context.Context, req IngestRequest) (IngestResult, error)
	// Track reads a unit, returning it as its source names it, and follows
	// it when req.Follow is set: the host reads, admits, then follows. A
	// read not followed reports no snapshot. ErrNotFound or ErrUnavailable
	// say why a read failed when the plugin can tell.
	Track(ctx context.Context, req TrackRequest) (Tracked, error)
	Untrack(ctx context.Context, u plugin.Unit) error
	// Resolve is the unit an address a person gives lands in; false for an
	// address that is not the plugin's.
	Resolve(ctx context.Context, address string) (plugin.Unit, bool, error)
	// Locate is the unit a local checkout's directory holds; false for none.
	Locate(ctx context.Context, dir string) (plugin.Unit, bool, error)
}

// Presenter serves what clients show of the plugin: its actions, its views
// and who the viewer is in the source.
type Presenter interface {
	Action(ctx context.Context, req ActionRequest) (json.RawMessage, error)
	View(ctx context.Context, req ViewRequest) (json.RawMessage, error)
	Identity(ctx context.Context) (Identity, error)
}

// Publisher posts approved drafts. The host calls Publish once, for a draft
// being sent, and never retries; Verify says whether a draft found sending
// after a crash was posted at or after startedAt.
type Publisher interface {
	Validate(ctx context.Context, d Draft) error
	Where(ctx context.Context, d Draft) (string, error)
	Place(ctx context.Context, d Draft) (string, error)
	Publish(ctx context.Context, d Draft) (Evidence, error)
	Verify(ctx context.Context, d Draft, startedAt time.Time) (Evidence, bool, error)
}

// Lander says where the plugin's work lands: the unit a sent draft's post
// landed in, the repository a unit's work checks out, and a task's move
// written back to the source.
type Lander interface {
	Landed(ctx context.Context, d Draft) (plugin.Unit, bool, error)
	Checkout(ctx context.Context, u plugin.Unit) (Checkout, bool, error)
	WriteState(ctx context.Context, w StateWrite) (Evidence, error)
}

// Briefer is how the host's brief queue reads a unit. BriefWant is a first
// turn asking for a unit's brief now: true when one is coming, which the
// plugin writes through Recorder.PutBrief, false when nothing of the unit is
// left to read.
type Briefer interface {
	BriefInput(ctx context.Context, u plugin.Unit) (BriefInput, error)
	BriefRevision(ctx context.Context, u plugin.Unit) (BriefRevision, error)
	BriefDescribe(ctx context.Context, in BriefInput) (string, error)
	BriefStale(ctx context.Context, req BriefStaleRequest) (bool, error)
	BriefWant(ctx context.Context, u plugin.Unit) (bool, error)
}

// Planner plans the automatic rounds on the plugin's units from the host's
// round state, builds a turn's payload, applies the plugin's effects,
// proposes drafts of its own rules and places a unit's work.
//
// The host calls Plan when it launches or skips a round; workflow facts
// come from the snapshot, which the plugin reports again after
// Notify(rounds).
type Planner interface {
	Plan(ctx context.Context, u plugin.Unit, st RoundState) (Plan, error)
	Payload(ctx context.Context, req PayloadRequest) (PayloadResult, error)
	ApplyEffect(ctx context.Context, req EffectRequest) error
	Proposals(ctx context.Context, now time.Time) ([]Draft, error)
	Placement(ctx context.Context, u plugin.Unit, item string) (Placements, error)
}

// Tooler serves the MCP tools the manifest declares. A result names one of
// the tool's manifest answers; the host words it from the layered
// tools.toml.
type Tooler interface {
	CallTool(ctx context.Context, call ToolCall) (ToolResult, error)
}

// CLIRunner runs the commands the manifest declares, which need no daemon.
// The command writes to stdout and stderr, which stream to the host as it
// writes them, and returns its exit code; the plugin process never writes
// its own stdout.
type CLIRunner interface {
	RunCLI(ctx context.Context, req CLIRequest, stdout, stderr io.Writer) (exit int, err error)
}

// Capability names a capability interface a plugin implements.
type Capability string

const (
	CapabilityIngester  Capability = "ingester"
	CapabilityPresenter Capability = "presenter"
	CapabilityPublisher Capability = "publisher"
	CapabilityLander    Capability = "lander"
	CapabilityBriefer   Capability = "briefer"
	CapabilityPlanner   Capability = "planner"
	CapabilityTooler    Capability = "tooler"
	CapabilityCLIRunner Capability = "cli"
)

// Capabilities are the capabilities s implements.
func Capabilities(s Source) []Capability {
	var out []Capability
	for _, c := range []struct {
		name Capability
		has  bool
	}{
		{CapabilityIngester, is[Ingester](s)},
		{CapabilityPresenter, is[Presenter](s)},
		{CapabilityPublisher, is[Publisher](s)},
		{CapabilityLander, is[Lander](s)},
		{CapabilityBriefer, is[Briefer](s)},
		{CapabilityPlanner, is[Planner](s)},
		{CapabilityTooler, is[Tooler](s)},
		{CapabilityCLIRunner, is[CLIRunner](s)},
	} {
		if c.has {
			out = append(out, c.name)
		}
	}
	return out
}

func is[T any](v any) bool {
	_, ok := v.(T)
	return ok
}

var (
	// ErrNotFound: the source has no such unit, or will not say.
	ErrNotFound = errors.New("not found")
	// ErrUnavailable: the read failed on the source's side or on the way.
	ErrUnavailable = errors.New("source unavailable")
	// ErrUnsupported: the other side does not implement the call.
	ErrUnsupported = errors.New("not supported")
	// ErrUnfit: the agent's answer to an Ask does not fit what was asked.
	ErrUnfit = errors.New("answer does not fit")
	// ErrMayHavePosted: a Publish failed after its post may have reached
	// the source; the host asks a person to confirm before a retry.
	ErrMayHavePosted = errors.New("may have been posted")
	// ErrNoAnswer: the call was canceled, timed out, lost its connection or
	// failed in transport before the other side answered, which may have
	// acted on it.
	ErrNoAnswer = errors.New("no answer")
)

// Priority orders who may spend a rate-limited budget when it runs low.
type Priority int32

const (
	// PriorityUser is a user action, which keeps the reserve; the zero Priority.
	PriorityUser Priority = iota
	// PriorityBackground is a job, a brief or an exploration.
	PriorityBackground
)

// Call is what the host's caller carried in its context, which reaches the
// plugin as fields of every request.
type Call struct {
	Priority Priority
	// MeterScope is the full name of the host's meter scope the call runs
	// under; the plugin's samples go back to it through [Services.Meter].
	MeterScope string
}

type callKey struct{}

// WithCall makes c the Call of every request the host sends under ctx.
func WithCall(ctx context.Context, c Call) context.Context {
	return context.WithValue(ctx, callKey{}, c)
}

// CallOf is the Call of the request being served, in the plugin, or the one
// WithCall set, in the host; the zero Call when there is none.
func CallOf(ctx context.Context) Call {
	c, _ := ctx.Value(callKey{}).(Call)
	return c
}
