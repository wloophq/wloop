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
//
// No broker method takes the host's loop lock: the host may hold it while
// it calls the plugin, whose call back would then wait forever. A plugin's
// reads across calls may see different commits; Notify converges them.
type Host struct {
	Reporter
	Ledger
	Reader
	Gates
	Recorder
	Wakes
	Services
	History
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
	Workspaces(ctx context.Context, repo string) (Listing, error)
	// Session is the MCP session with handle: its unit, turn and draft rules.
	Session(ctx context.Context, handle string) (Session, error)
}

// Gates proposes what waits on the user.
type Gates interface {
	// OpenGate proposes a draft, which the host's approval policy holds or
	// releases; no plugin approves one. With a session, the host sets the
	// draft's origin, task and default items from the session's book and
	// refuses an effect the session's intent does not allow; without, the
	// origin is the plugin's rule, checked against the manifest. The host
	// derives the delivery from the draft's kind through the manifest.
	OpenGate(ctx context.Context, req GateRequest) (GateRef, error)
	// RecordSent records a post a tool made at once as a sent draft. The
	// host refuses it for a session that is not live, or for a tool the
	// manifest does not mark posts = "now".
	RecordSent(ctx context.Context, d Draft, session, tool string) (GateRef, error)
	// RehomeDrafts moves the open drafts of each move's From onto its To in
	// one host transaction, and counts them; applying it twice changes
	// nothing, so a plugin retries it after a crash.
	RehomeDrafts(ctx context.Context, moves []UnitMove) (int, error)
	// SetOverride sets the host overrides of u in one transaction, only
	// while each of ifMatch holds its value ("" for unset); a set value ""
	// clears one. Written is false when ifMatch did not hold.
	SetOverride(ctx context.Context, u plugin.Unit, set, ifMatch map[string]string) (written bool, err error)
}

// GateRequest proposes a draft, from the MCP session with handle Session
// when one proposes it.
type GateRequest struct {
	Draft   Draft
	Session string
}

// UnitMove moves drafts from one unit to another.
type UnitMove struct {
	From, To plugin.Unit
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

// Services are the host's services a plugin spends: the agent, the meter,
// its configuration and its profile layer.
type Services interface {
	// Ask fails with ErrUnfit when the answer does not fit the schema.
	Ask(ctx context.Context, req AskRequest) (AskResult, error)
	Meter(ctx context.Context, s Sample) error
	Config(ctx context.Context) (Config, error)
	// Profile reads path in the plugin's profile layer, the user's
	// plugins/<id>/ over the plugin's own.
	Profile(ctx context.Context, path string) ([]byte, error)
}

// History reads the events the host keeps: the plugin's own recorded
// events and the loop's.
type History interface {
	Events(ctx context.Context, q EventQuery) ([]Event, error)
}

// EventQuery selects events of Units since Since; empty Kinds reads every
// kind.
type EventQuery struct {
	Units []plugin.Unit
	Kinds []string
	Since time.Time
}

// Config is the configuration a plugin reads: its [plugins.<id>] table, and
// the host keys its manifest's config_reads names.
type Config struct {
	Plugin json.RawMessage
	Host   json.RawMessage
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

// Turn is one turn on a unit. At is when it reached its state.
type Turn struct {
	Seq                                 int64
	ID, Intent, Role, Workflow          string
	State, Head, Session, Summary, Task string
	SentAt, StartedAt, EndedAt          time.Time
	AnsweredAt, At                      time.Time
	Items                               []string
}

// TurnRecord is a record a turn left; Record is its JSON.
type TurnRecord struct {
	ID, TurnSeq int64
	Head        string
	At          time.Time
	Record      json.RawMessage
}

// Listing is the host's workspaces; Partial says a manager failed to list.
type Listing struct {
	Partial    bool
	Repos      []Repo
	Workspaces []Workspace
}

// Repo is a repository a workspace manager knows.
type Repo struct {
	ID, Remote, Path, DefaultBranch string
}

// Workspace is a workspace the host's managers keep. Local says it is on
// this host.
type Workspace struct {
	Manager, ID, Repo, RepoID, Path string
	DisplayName, Comment            string
	Branch, PushBranch, Status      string
	IsMain, Local                   bool
	AgentStates                     []string
	Units                           []plugin.Unit
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
	// Key dedupes the event: a second event of a unit with its kind and key
	// is not recorded.
	Key string
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

// AskResult is the agent's answer and what it cost; CostUSD is nil when
// the agent reported none.
type AskResult struct {
	Text     string
	Model    string
	CostUSD  *float64
	Duration time.Duration
	Tokens   int64
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
