// Package plugin is the SDK every plugin binary and the host share: the
// handshake, the protocol versions, the lifecycle every kind answers (health,
// start, stop, notify), the logger and the process rules.
//
// A plugin binary serves one or more kinds. Each kind has its own SDK package
// (pkg/plugin/source for sources) that adapts the plugin's Go interfaces to an
// [Impl]; the plugin's main calls [Serve] with them and never touches gRPC.
//
// A plugin writes nothing to stdout: go-plugin reads the handshake from it. It
// logs through [Logger], which writes to stderr, where the host reads it.
//
// This package and every package under it import nothing under internal/.
package plugin

import (
	"context"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
)

// Protocol is the plugin protocol version this SDK speaks. A breaking change
// to any kind's protocol is a new version, served beside the old one in
// [VersionedPlugins] until no supported host or plugin speaks the old one.
const Protocol = 1

// Handshake is the go-plugin handshake the host and every plugin check. Its
// ProtocolVersion is unused: the version is negotiated from [VersionedPlugins].
var Handshake = goplugin.HandshakeConfig{
	ProtocolVersion:  Protocol,
	MagicCookieKey:   "LOOP_PLUGIN_COOKIE",
	MagicCookieValue: "3f1c9a0e-units-and-tasks",
}

// BrandEnv is the environment variable through which the host passes its
// brand name to a plugin process; a plugin cannot import the host's brand.
const BrandEnv = "LOOP_PLUGIN_BRAND"

// ParentEnv is the environment variable through which the host passes its
// pid, so a plugin whose host died before Serve ran still exits.
const ParentEnv = "LOOP_PLUGIN_PARENT_PID"

// Kind names a capability a plugin serves, such as "source". It is the
// go-plugin plugin name, and the name of the kind's table in plugin.toml.
type Kind string

// Unit names a unit of work by its provider and the provider's key, as the
// host's unit does.
type Unit struct {
	Provider string `json:"provider,omitempty"`
	Key      string `json:"key,omitempty"`
}

// Topic names host state a kind may read, of which Notify says what changed.
// The names are the wire's, stable across plugins built apart from the host.
type Topic string

const (
	TopicRounds     Topic = "rounds"     // round state, its marks, the pending rounds and the pause
	TopicGates      Topic = "gates"      // gates: drafts, plans, questions, proposals
	TopicMarks      Topic = "marks"      // unit marks
	TopicTasks      Topic = "tasks"      // tasks and their properties
	TopicTurns      Topic = "turns"      // turns and their records
	TopicSessions   Topic = "sessions"   // sessions and the workspaces they run in
	TopicOverrides  Topic = "overrides"  // per-unit overrides
	TopicViewer     Topic = "viewer"     // the viewer's login and teams
	TopicConfig     Topic = "config"     // the loaded profile and configuration
	TopicEvents     Topic = "events"     // events on units, the loop's and the plugins'
	TopicWorkspaces Topic = "workspaces" // the workspace managers' listing
	TopicAgents     Topic = "agents"     // the agents and their activity
)

// Topics are every Topic, which a manifest's notify list names from.
var Topics = []Topic{TopicRounds, TopicGates, TopicMarks, TopicTasks, TopicTurns, TopicSessions, TopicOverrides,
	TopicViewer, TopicConfig, TopicEvents, TopicWorkspaces, TopicAgents}

// Status is what a kind's health reports.
type Status int32

const (
	// StatusUnknown is the zero Status; a kind never reports it.
	StatusUnknown Status = iota
	// StatusLoading: the kind has not yet imported its data or reported every unit.
	StatusLoading
	// StatusServing: the kind is up to date.
	StatusServing
	// StatusDegraded: the kind serves, but a source it reads is failing.
	StatusDegraded
)

// Health is a kind's health. Reason says why a kind is loading or degraded,
// and is empty while it serves.
type Health struct {
	Status Status
	Reason string
}

// StartRequest is what the host gives a kind when it starts it.
type StartRequest struct {
	// StateDir is the plugin's own state directory; the host never reads it.
	StateDir string
	// HostBroker is the go-plugin broker id on which the host serves the
	// kind's host services, zero when it serves none. The kind's SDK package
	// dials it; a plugin never sees it.
	HostBroker uint32
}

// Lifecycle is what every kind answers. The host calls Start once per
// process, before any call but Health; Stop before it kills the process; and
// Notify when host state the kind reads changed for some units, after which
// a kind that computes from that state reports those units again.
type Lifecycle interface {
	Health(ctx context.Context) (Health, error)
	Start(ctx context.Context, req StartRequest) error
	Stop(ctx context.Context) error
	Notify(ctx context.Context, units []Unit, topics []Topic) error
}

// Impl is one kind a plugin binary serves: its lifecycle, and the kind's own
// gRPC services, which Register adds to the plugin's server. A kind's SDK
// package builds the Impl; a plugin implements only that package's
// interfaces.
type Impl interface {
	Lifecycle
	Register(s *grpc.Server, broker *goplugin.GRPCBroker) error
}
