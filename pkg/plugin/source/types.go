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
	Unit plugin.Unit `json:"unit,omitzero"`
	// Rev grows with every change of the unit the plugin reports; the host
	// compares it with the rev its facts came from.
	Rev uint64    `json:"rev,omitempty"`
	At  time.Time `json:"at,omitzero"`
	// Gone says the unit no longer exists in its source.
	Gone bool `json:"gone,omitempty"`
	// Data is the unit's data, which its templates and rows read.
	Data json.RawMessage `json:"data,omitempty"`
	// Situation is what the unit waits on as its source says; nil for none.
	Situation *Situation `json:"situation,omitempty"`
	Card      *Card      `json:"card,omitempty"`
	Items     []Item     `json:"items,omitempty"`
	Facts     []Fact     `json:"facts,omitempty"`
	Signals   []Signal   `json:"signals,omitempty"`
	// Owes are who the unit waits on.
	Owes   []Person `json:"owes,omitempty"`
	Offers []Offer  `json:"offers,omitempty"`
	// Extraction is the health of the unit's extraction; nil for a unit
	// the plugin extracts nothing from.
	Extraction *ExtractionHealth `json:"extraction,omitempty"`
	// Contexts and Due are the task fields the plugin writes.
	Contexts []string  `json:"contexts,omitempty"`
	Due      time.Time `json:"due,omitzero"`
	// Links are the addresses the unit's source links to, which the host
	// resolves with every manifest's codec.
	Links []Link `json:"links,omitempty"`
	// Facets are the unit's values of the manifest's facets, by facet id;
	// Views the ids of the manifest's views its row is in.
	Facets map[string]string `json:"facets,omitempty"`
	Views  []string          `json:"views,omitempty"`
}

// Situation is what a unit waits on, as the plugin derives it from its
// source alone. The host overlays its own state: a run on the unit, a mute
// (matched by Kind and WaitingOn) and a pending round.
type Situation struct {
	// Kind is one of the manifest's situations.
	Kind      string    `json:"kind,omitempty"`
	WaitingOn string    `json:"waiting_on,omitempty"`
	Since     time.Time `json:"since,omitzero"`
	Reason    string    `json:"reason,omitempty"`
	// Stalled is an After value: the situation is stalled once it holds.
	Stalled Value `json:"stalled,omitzero"`
}

// Link is an address a unit's source links to.
type Link struct {
	URL      string `json:"url,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Item     string `json:"item,omitempty"`
	Evidence Anchor `json:"evidence,omitzero"`
}

// Signal is a link signal the manifest declares, on one unit.
type Signal struct {
	Name string `json:"name,omitempty"`
	On   bool   `json:"on,omitempty"`
	// Since is when it turned on; zero when unknown or off.
	Since time.Time `json:"since,omitzero"`
}

// Offer is an action the plugin offers on a unit.
type Offer struct {
	Action string          `json:"action,omitempty"`
	Args   json.RawMessage `json:"args,omitempty"`
}

// ExtractionHealth says what a unit's extraction has not yet read.
type ExtractionHealth struct {
	Pending   int  `json:"pending,omitempty"`
	BudgetHit bool `json:"budget_hit,omitempty"`
	// Oldest is the oldest message not yet extracted; zero with none.
	Oldest time.Time `json:"oldest,omitzero"`
}

// Person is a person or group as a source knows them; Key is
// "<provider>:<id>", "group:<name>" or "system:<name>".
type Person struct {
	Key     string   `json:"key,omitempty"`
	Name    string   `json:"name,omitempty"`
	Aliases []string `json:"aliases,omitempty"`
}

// Anchor is a point in a unit, such as a message or a review.
type Anchor struct {
	Unit      plugin.Unit `json:"unit,omitzero"`
	Ref       string      `json:"ref,omitempty"`
	Permalink string      `json:"permalink,omitempty"`
	At        time.Time   `json:"at,omitzero"`
}

// Card is what the plugin shows of one unit.
type Card struct {
	Label         string       `json:"label,omitempty"`
	Title         string       `json:"title,omitempty"`
	State         string       `json:"state,omitempty"`
	Outcome       string       `json:"outcome,omitempty"`
	Signals       []CardSignal `json:"signals,omitempty"`
	LastHuman     *Human       `json:"last_human,omitempty"`
	Why           string       `json:"why,omitempty"`
	WaitsOn       []Person     `json:"waits_on,omitempty"`
	Since         time.Time    `json:"since,omitzero"`
	Link          string       `json:"link,omitempty"`
	TurnsLink     string       `json:"turns_link,omitempty"`
	StartedByMe   bool         `json:"started_by_me,omitempty"`
	ClosurePosted bool         `json:"closure_posted,omitempty"`
	Cares         bool         `json:"cares,omitempty"`
}

// CardSignal is one line of a card: a key, its value and its tone.
type CardSignal struct {
	Key   string    `json:"key,omitempty"`
	Value string    `json:"value,omitempty"`
	Tone  string    `json:"tone,omitempty"`
	At    time.Time `json:"at,omitzero"`
}

// Human is the last thing a person did on a unit.
type Human struct {
	Who  Person    `json:"who,omitzero"`
	What string    `json:"what,omitempty"`
	At   time.Time `json:"at,omitzero"`
	Link string    `json:"link,omitempty"`
}

// Item is a commitment the plugin observed: who asks whom for what.
type Item struct {
	ID       string      `json:"id,omitempty"`
	Unit     plugin.Unit `json:"unit,omitzero"`
	Kind     string      `json:"kind,omitempty"`
	Summary  string      `json:"summary,omitempty"`
	Context  string      `json:"context,omitempty"`
	Options  []Option    `json:"options,omitempty"`
	Deadline string      `json:"deadline,omitempty"`
	URLs     []string    `json:"urls,omitempty"`
	Contexts []string    `json:"contexts,omitempty"`
	// Category is the task category the plugin proposes for a new task,
	// such as "inbox" or "ready"; never a state name. Empty is inbox.
	Category   string         `json:"category,omitempty"`
	Origin     Anchor         `json:"origin,omitzero"`
	Requester  Person         `json:"requester,omitzero"`
	Assignees  []Person       `json:"assignees,omitempty"`
	AssignedAt Anchor         `json:"assigned_at,omitzero"`
	Basis      string         `json:"basis,omitempty"`
	WaitingOn  []Person       `json:"waiting_on,omitempty"`
	State      string         `json:"state,omitempty"`
	Resolution string         `json:"resolution,omitempty"`
	Evidence   ItemEvidence   `json:"evidence,omitzero"`
	Trail      []ItemEvidence `json:"trail,omitempty"`
	Confidence string         `json:"confidence,omitempty"`
	Ambiguity  string         `json:"ambiguity,omitempty"`
	Provenance string         `json:"provenance,omitempty"`
	Confirmed  bool           `json:"confirmed,omitempty"`
	// TaskKey groups items into one task: the item id when empty.
	TaskKey   string    `json:"task_key,omitempty"`
	UserAt    time.Time `json:"user_at,omitzero"`
	CreatedAt time.Time `json:"created_at,omitzero"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

// Option is one of the choices a decision item offers.
type Option struct {
	Label    string   `json:"label,omitempty"`
	ArguedBy []Person `json:"argued_by,omitempty"`
}

// ItemEvidence is what moved an item, and where.
type ItemEvidence struct {
	Kind   string `json:"kind,omitempty"`
	Anchor Anchor `json:"anchor,omitzero"`
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
	// Base is the branch the work merges into; Fork says the head lives in
	// another repository.
	Base string
	Fork bool
}

// StateWrite is a task's move the plugin writes back to its source.
type StateWrite struct {
	Unit     plugin.Unit
	Task     string
	State    string
	Category string
}

// Placements are where a unit's work runs: the plugin's default, and what
// its source suggests for an item; each nil when there is none.
type Placements struct {
	Default, Suggested *Placement
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

// TrackRequest reads a unit, and follows it when Follow is set.
type TrackRequest struct {
	Unit   plugin.Unit
	Follow bool
}

// Tracked is a unit as its source names it.
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
	// Attempt names what an attempt at the unit now is made from, so a
	// failure counts against that attempt only.
	Attempt string
}

// BriefWritten is what a stored brief was made from and how its last
// attempt went.
type BriefWritten struct {
	At          BriefRevision
	Version     string
	Error       string
	Failures    int
	AttemptedAt time.Time
	// AttemptOf is the Attempt of the last attempt.
	AttemptOf string
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
	Marks     map[string]string
	SentAt    time.Time
	AgentHome string
	// Today counts the unit's automatic rounds today, DayTotal every unit's.
	Today, DayTotal int
	// Paused says the unit's rounds are held, by the user or a pause.
	Paused  bool
	Pending []PendingRound
	// Overrides are the unit's host overrides, by name.
	Overrides map[string]string
	// Blocked is set while a round's start is blocked on a question.
	Blocked *Blocked
}

// Blocked is a round's start blocked on the agent's question.
type Blocked struct {
	Question, Root, Folder string
	Since                  time.Time
}

// PendingRound is an automatic round waiting on the user until Deadline.
type PendingRound struct {
	Intent, Reason  string
	Since, Deadline time.Time
}

// Plan is a unit's round plan: the rounds it would launch. The workflows'
// facts and the unit's deadline come from its snapshot.
type Plan struct {
	Rounds []Round
}

// Round is a round a plan would launch on a unit.
type Round struct {
	Name      string
	Workspace WorkspaceRef
	Head      string
	Reason    string
	// Observed says the round's cause was already seen.
	Observed bool
	// TurnFacts are the turn slot's facts of the round's turn.
	TurnFacts []Fact
	// OnSent and OnSkipped are the marks the host writes when it sends or
	// skips the round; "" removes a mark.
	OnSent, OnSkipped map[string]string
	// NotDue says why the round is not due now; "" is due. A round not due
	// is not launched, but skipping it still writes OnSkipped.
	NotDue string
}

// WorkspaceRef names a workspace in its manager.
type WorkspaceRef struct {
	Manager, ID string
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
	// LastVerdict is the head the loop's last record covered; nil none.
	LastVerdict *Verdict
	// Round is the planned round: "first", "follow-up" or "".
	Round string
	Agent string
	// BriefMissing says why the turn runs without a brief; "" with one.
	BriefMissing string
	Items        []string
	Task         string
}

// Verdict is what the loop's last record of a unit covered.
type Verdict struct {
	Head, Endorsed string
	At             time.Time
}

// PayloadResult is a turn's payload: the argument its prompt names, how the
// unit is worded, the facts its instructions read, its data for templates
// and the contents the prompt carries.
type PayloadResult struct {
	Arg      string
	Wording  Wording
	Facts    []Fact
	Data     json.RawMessage
	Contents string
}

// Wording is how a turn names its unit; the plugin takes the words from its
// profile layer.
type Wording struct {
	Noun, This, Ref, Short string
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

// ToolResult names one of the tool's manifest answers and its fields; the
// host words it from the layered tools.toml.
type ToolResult struct {
	Answer  string
	Fields  json.RawMessage
	IsError bool
}

// CLIRequest is a command of the manifest the host runs, with the
// configuration the plugin reads, as the plugin has no broker in CLI mode.
type CLIRequest struct {
	Args   []string
	Stdin  []byte
	Config Config
}
