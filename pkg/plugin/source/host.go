package source

import (
	"context"
	"encoding/json"
	"time"

	"github.com/wloophq/wloop/pkg/plugin"
)

// Host is the host's broker as a plugin calls it, and what the host serves
// through [ServeHost]. A nil interface in the host's Host answers every call
// of it with [ErrUnsupported].
type Host struct {
	Reporter
	Ledger
	Reader
	Gates
	Recorder
	Wakes
	Services
}

// Reporter takes what a plugin observed. Report replaces each unit's
// snapshot; Changed tells the host that units changed in ways a snapshot
// does not carry, for its change log.
type Reporter interface {
	Report(ctx context.Context, snapshots []Snapshot) error
	Changed(ctx context.Context, units []plugin.Unit, topics []plugin.Topic) error
}

// Ledger is the host's items ledger. ApplyItems returns one result per op,
// in order.
type Ledger interface {
	ApplyItems(ctx context.Context, ops []ItemOp) ([]ItemResult, error)
	QueryItems(ctx context.Context, q ItemQuery) ([]Item, error)
}

// Reader reads the host state a plugin computes from.
type Reader interface {
	// Tasks are the tasks linked to units.
	Tasks(ctx context.Context, units []plugin.Unit) ([]Task, error)
	// Books are the turns and records of units.
	Books(ctx context.Context, units []plugin.Unit) ([]Book, error)
	RoundState(ctx context.Context, u plugin.Unit) (RoundState, error)
	// Workspaces lists the workspaces of repo, "owner/name"; "" every one.
	Workspaces(ctx context.Context, repo string) ([]Workspace, error)
	// Session is the MCP session with handle: its unit, turn and draft rules.
	Session(ctx context.Context, handle string) (Session, error)
}

// Gates proposes what waits on the user. OpenGate proposes a draft, which
// the host's approval policy holds or releases; no plugin approves one.
// RecordSent records a post a tool made at once as a sent draft.
type Gates interface {
	OpenGate(ctx context.Context, d Draft) (GateRef, error)
	RecordSent(ctx context.Context, d Draft, session string) (GateRef, error)
	// RehomeDrafts moves the open drafts of from onto to, and counts them.
	RehomeDrafts(ctx context.Context, from, to plugin.Unit) (int, error)
	// SetOverride sets the host override name of u; value "" clears it.
	SetOverride(ctx context.Context, u plugin.Unit, name, value string) error
}

// Recorder writes host data a plugin produces: events on its units, the
// marks of its rounds and the briefs of its extraction.
type Recorder interface {
	RecordEvents(ctx context.Context, events []Event) error
	// SetMarks sets marks of u by name; value "" removes one.
	SetMarks(ctx context.Context, u plugin.Unit, marks map[string]string) error
	PutBrief(ctx context.Context, b Brief) error
}

// Wakes sets and clears when a task of a unit wakes.
type Wakes interface {
	SetWake(ctx context.Context, w Wake) error
	ClearWake(ctx context.Context, u plugin.Unit, task string) error
}

// Services are the host's services a plugin spends: the agent, the meter
// and its configuration.
type Services interface {
	Ask(ctx context.Context, req AskRequest) (AskResult, error)
	Meter(ctx context.Context, s Sample) error
	// Config is the plugin's [plugins.<id>] table of the configuration.
	Config(ctx context.Context) (json.RawMessage, error)
}

// Task is a task the host keeps, as a plugin reads it.
type Task struct {
	ID       string
	Title    string
	State    string
	Category string
	Parent   string
	Props    json.RawMessage
	Units    []plugin.Unit
}

// Book is a unit's turns and their records.
type Book struct {
	Unit    plugin.Unit
	Turns   []Turn
	Records []TurnRecord
}

// Turn is one turn on a unit.
type Turn struct {
	Seq                        int64
	ID, Intent, Role, Workflow string
	State, Head, Session       string
	SentAt, StartedAt, EndedAt time.Time
}

// TurnRecord is a record a turn left; Record is its JSON.
type TurnRecord struct {
	ID, TurnSeq int64
	Head        string
	At          time.Time
	Record      json.RawMessage
}

// Workspace is a workspace the host's managers keep.
type Workspace struct {
	Manager, ID, Repo, Path    string
	Branch, PushBranch, Status string
	IsMain                     bool
	Units                      []plugin.Unit
}

// Session is an MCP session as the host knows it.
type Session struct {
	Unit    plugin.Unit
	Turn    string
	TurnSeq int64
	Intent  string
	Role    string
	// AllowedEffects are the effects a draft of the session may carry.
	AllowedEffects []string
}

// GateRef names the gate a draft opened, and the draft.
type GateRef struct {
	Gate  string
	Draft int64
}

// Event is an event on a unit, for the timelines of its tasks.
type Event struct {
	Unit   plugin.Unit
	Kind   string
	Actor  string
	At     time.Time
	Anchor Anchor
	Data   json.RawMessage
}

// Brief is a brief the plugin wrote of a task, in the brief schema's JSON.
type Brief struct {
	Task    string
	Unit    plugin.Unit
	Version string
	Body    json.RawMessage
}

// Wake is when a unit's task wakes: at Until, or once When holds over the
// task's facts.
type Wake struct {
	Unit  plugin.Unit
	Task  string
	Until time.Time
	When  string
}

// AskRequest is a prompt for the host's agent.
type AskRequest struct {
	Prompt string
	// Schema is the JSON schema the answer matches; nil asks for text.
	Schema     json.RawMessage
	Tier       string
	MaxCostUSD float64
	MeterScope string
}

// AskResult is the agent's answer and what it cost.
type AskResult struct {
	Text    string
	CostUSD float64
}

// Sample is one metered call the plugin made, under the host's meter scope.
type Sample struct {
	Scope    string
	Resource string
	Op       string
	Spawn    bool
	Duration time.Duration
	Failed   bool
	Points   int64
}
