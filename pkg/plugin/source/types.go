package source

import (
	"encoding/json"
	"time"

	"github.com/wloophq/wloop/pkg/plugin"
)

// Snapshot is everything a plugin reports of one unit; a later snapshot of
// the unit replaces it whole. Fields holding JSON hold an object whose shape
// the plugin and its profile layer own.
type Snapshot struct {
	Unit plugin.Unit
	// Rev grows with every change of the unit the plugin reports; the host
	// compares it with the rev its facts came from.
	Rev uint64
	At  time.Time
	// Gone says the unit no longer exists in its source.
	Gone bool
	// Data is the unit's data, which its templates and rows read.
	Data json.RawMessage
	// Situation holds the inputs the host's situation derivation reads.
	Situation json.RawMessage
	Card      *Card
	Items     []Item
	Facts     []Fact
	Signals   []Signal
	// Owes are who the unit waits on.
	Owes   []Person
	Offers []Offer
	// Extraction is the health of the unit's extraction; nil for a unit
	// the plugin extracts nothing from.
	Extraction *ExtractionHealth
	// Contexts and Due are the task fields the plugin writes.
	Contexts []string
	Due      time.Time
}

// Signal is a link signal the manifest declares, on one unit.
type Signal struct {
	Name string
	On   bool
	// Since is when it turned on; zero when unknown or off.
	Since time.Time
}

// Offer is an action the plugin offers on a unit.
type Offer struct {
	Action string
	Args   json.RawMessage
}

// ExtractionHealth says what a unit's extraction has not yet read.
type ExtractionHealth struct {
	Pending   int
	BudgetHit bool
	// Oldest is the oldest message not yet extracted; zero with none.
	Oldest time.Time
}

// Person is a person or group as a source knows them; Key is
// "<provider>:<id>", "group:<name>" or "system:<name>".
type Person struct {
	Key     string
	Name    string
	Aliases []string
}

// Anchor is a point in a unit, such as a message or a review.
type Anchor struct {
	Unit      plugin.Unit
	Ref       string
	Permalink string
	At        time.Time
}

// Card is what the plugin shows of one unit.
type Card struct {
	Label         string
	Title         string
	State         string
	Outcome       string
	Signals       []CardSignal
	LastHuman     *Human
	Why           string
	WaitsOn       []Person
	Since         time.Time
	Link          string
	TurnsLink     string
	StartedByMe   bool
	ClosurePosted bool
	Cares         bool
}

// CardSignal is one line of a card: a key, its value and its tone.
type CardSignal struct {
	Key, Value, Tone string
	At               time.Time
}

// Human is the last thing a person did on a unit.
type Human struct {
	Who  Person
	What string
	At   time.Time
	Link string
}

// Item is a commitment the plugin observed: who asks whom for what.
type Item struct {
	ID       string
	Unit     plugin.Unit
	Kind     string
	Summary  string
	Context  string
	Options  []Option
	Deadline string
	URLs     []string
	Contexts []string
	// Category is the task category the plugin proposes for a new task,
	// such as "inbox" or "ready"; never a state name. Empty is inbox.
	Category   string
	Origin     Anchor
	Requester  Person
	Assignees  []Person
	AssignedAt Anchor
	Basis      string
	WaitingOn  []Person
	State      string
	Resolution string
	Evidence   ItemEvidence
	Trail      []ItemEvidence
	Confidence string
	Ambiguity  string
	Provenance string
	Confirmed  bool
	// TaskKey groups items into one task: the item id when empty.
	TaskKey   string
	UserAt    time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Option is one of the choices a decision item offers.
type Option struct {
	Label    string
	ArguedBy []Person
}

// ItemEvidence is what moved an item, and where.
type ItemEvidence struct {
	Kind   string
	Anchor Anchor
}

// ItemOp is one change to the host's items ledger.
type ItemOp struct {
	Op         string
	Unit       plugin.Unit
	Item       string
	Anchor     Anchor
	Fields     *ItemFields
	Assignees  []Person
	Basis      string
	Resolution string
	Evidence   *ItemEvidence
	To         *plugin.Unit
	LinkKind   string
	Reason     string
}

// ItemFields are the fields an op sets; nil leaves a field unchanged.
type ItemFields struct {
	Kind, Summary, Context, Deadline, Ambiguity *string
	Options                                     *[]Option
	URLs, Contexts                              *[]string
	Category                                    *string
	Requester                                   *Person
	WaitingOn                                   *[]Person
	Confidence                                  *string
}

// ItemResult is the outcome of one op: the item it touched, or why it was
// refused.
type ItemResult struct {
	Item  string
	Error string
}

// ItemQuery selects items of the ledger.
type ItemQuery struct {
	Units    []plugin.Unit
	OpenOnly bool
}

// Draft is a post the loop proposes or sent; ID is zero for one not stored.
type Draft struct {
	ID       int64
	Unit     plugin.Unit
	Items    []string
	Task     string
	Kind     string
	Purpose  string
	Delivery string
	Effects  []string
	// Target is the plugin's target grammar; "" is the unit itself.
	Target   string
	Text     string
	Reaction string
	Replaced string
	Origin   DraftOrigin
	State    string
	Evidence Evidence
}

// DraftOrigin is who proposed a draft.
type DraftOrigin struct {
	Kind     string
	TurnSeq  int64
	Intent   string
	Workflow string
	Rule     string
	Key      string
	Unit     plugin.Unit
}

// Evidence is where a post landed.
type Evidence struct {
	Ref       string
	Permalink string
	At        time.Time
}

// Checkout is the repository a unit's work checks out.
type Checkout struct {
	// Repo is "owner/name"; Branch "" is the default branch; Head "" unknown.
	Repo, Branch, Head string
}

// StateWrite is a task's move the plugin writes back to its source.
type StateWrite struct {
	Unit     plugin.Unit
	Task     string
	State    string
	Category string
}

// Placement is where a unit's work runs.
type Placement struct {
	Repo, Checkout, Role string
	Shared               bool
}

// IngestRequest is one run of a manifest job.
type IngestRequest struct {
	Job string
	Now time.Time
}

// IngestResult is a job run's outcome, for the job log.
type IngestResult struct {
	Summary, Skipped string
}

// Tracked is a unit the plugin now follows.
type Tracked struct {
	Unit  plugin.Unit
	State string
}

// ActionRequest is an action a person runs on a unit.
type ActionRequest struct {
	Unit   plugin.Unit
	Action string
	Input  string
	Args   json.RawMessage
}

// ViewRequest asks for one of the plugin's views.
type ViewRequest struct {
	View  string
	Query json.RawMessage
}

// Identity is the viewer as the source knows them.
type Identity struct {
	Viewer Person
	Teams  []string
}

// BriefInput is what a brief of a unit reads now.
type BriefInput struct {
	Text  string
	Bytes int64
	Cut   bool
	Parts json.RawMessage
}

// BriefRevision is what a unit is at, read without its input.
type BriefRevision struct {
	Revision string
	Parts    json.RawMessage
}

// BriefWritten is what a stored brief was made from and how its last
// attempt went.
type BriefWritten struct {
	At          BriefRevision
	Version     string
	Error       string
	Failures    int
	AttemptedAt time.Time
}

// BriefStaleRequest asks whether a brief is due again.
type BriefStaleRequest struct {
	Written BriefWritten
	Current BriefRevision
	Version string
	Now     time.Time
}

// RoundState is the host's round bookkeeping of one unit, which a Plan
// reads.
type RoundState struct {
	Marks             map[string]string
	SentAt            time.Time
	AgentHome         string
	FirstRoundSkipped bool
	// Today counts the unit's automatic rounds today, DayTotal every unit's.
	Today, DayTotal int
	Paused          bool
	Pending         []PendingRound
}

// PendingRound is an automatic round waiting on the user until Deadline.
type PendingRound struct {
	Intent, Reason  string
	Since, Deadline time.Time
}

// Plan is a unit's round plan: the facts the profile's workflows read, its
// next deadline, and its rank among the units due.
type Plan struct {
	Facts []Fact
	Due   time.Time
	Rank  int64
}

// PayloadRequest asks for the payload of a turn on a unit.
type PayloadRequest struct {
	Unit     plugin.Unit
	Intent   string
	Role     string
	Trigger  string
	Inputs   json.RawMessage
	Guidance string
	Local    string
	Owned    bool
	Plain    bool
}

// TurnRef names a turn.
type TurnRef struct {
	Seq  int64
	ID   string
	Head string
}

// EffectRequest is an effect of the manifest to apply: Turn is set for a
// turn effect, Draft for a draft effect.
type EffectRequest struct {
	Effect string
	Unit   plugin.Unit
	Turn   *TurnRef
	Draft  *Draft
}

// ToolCall is a call of an MCP tool the manifest declares. Session is the
// MCP session's handle and Turn the turn it serves; Reader.Session reads the
// session's unit and draft rules.
type ToolCall struct {
	Tool    string
	Args    json.RawMessage
	Session string
	Turn    string
}

// ToolResult is the tool's MCP result.
type ToolResult struct {
	Result  json.RawMessage
	IsError bool
}

// CLIRequest is a command of the manifest the host runs.
type CLIRequest struct {
	Args  []string
	Stdin []byte
}

// CLIResult is the command's output, which the host prints.
type CLIResult struct {
	Stdout, Stderr []byte
	ExitCode       int
}
