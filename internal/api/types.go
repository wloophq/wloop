package api

import (
	"encoding/json"
	"time"

	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const (
	ViewNeedsMe = "needs-me"
	ViewStalled = "stalled"
	ViewMine    = "mine"
	ViewLoop    = "loop"
	ViewAll     = "all"
	ViewDone    = "done"
	// ViewApproved holds open pull requests that wait on nothing of mine,
	// whose workspace may go before the merge.
	ViewApproved = "approved"
)

// Kind is a row's provider-scoped situation; its labels come from
// CatalogResponse.Situations.
type Kind string

// Row is one unit's row: the generic part every surface reads, the served
// facet values, and the provider's payload, opaque to the core.
type Row struct {
	Unit unit.Unit `json:"unit"`
	// Link and Label are the server's presentation of the unit, such as
	// its pull request's URL and "owner/repo#12".
	Link    string              `json:"link"`
	Label   string              `json:"label"`
	Title   string              `json:"title"`
	Kind    Kind                `json:"kind"`
	Summary Summary             `json:"summary"`
	Views   []string            `json:"views"`
	Facets  map[string][]string `json:"facets"` // facet id -> values; Values[0] is the group and sort key
	// Actions are what the row offers now, in the order of the specs.
	Actions []ActionState `json:"actions"`
	// SpecsVersion is the version of GET /api/actions the actions follow.
	SpecsVersion string `json:"specs_version"`
	// Question is the open question or stop of the turn the situation
	// comes from.
	Question *QuestionView   `json:"question,omitempty"`
	Pending  *PendingRound   `json:"pending,omitempty"`
	Items    []RowItem       `json:"items,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

// Since is a client's position in the rows: the epoch and revision it last
// merged. The zero Since asks for every row.
type Since struct {
	Epoch string
	Rev   uint64
}

// Change says the rows moved: the units whose row changed or left since
// the previous change, or All when too much moved to list. A "notify"
// change carries the notices a workflow raised for the user.
type Change struct {
	Type    string      `json:"type"` // "changed" or "notify"
	Epoch   string      `json:"epoch"`
	Rev     uint64      `json:"rev"`
	Units   []unit.Unit `json:"units,omitempty"`
	All     bool        `json:"all,omitempty"`
	Health  bool        `json:"health,omitempty"`
	Notices []Notice    `json:"notices,omitempty"`
	// Counters are the Area "" counters, on every "changed" change after a TopicEntries or TopicDrafts note.
	Counters *Counters `json:"counters,omitempty"`
}

// Notice is a notification a workflow's notify.self step raised; ID is
// unique per raise.
type Notice struct {
	ID       string    `json:"id"`
	Workflow string    `json:"workflow"`
	Unit     unit.Unit `json:"unit"`
	Label    string    `json:"label,omitempty"` // Unit as a person reads it
	Text     string    `json:"text"`
}

// RowsPage is a view's rows at Rev: every row when Full, else the rows that
// changed since the asked revision and the units that left the view.
type RowsPage struct {
	Epoch    string      `json:"epoch"`
	Rev      uint64      `json:"rev"`
	Full     bool        `json:"full"`
	Rows     []Row       `json:"rows"`
	Removed  []unit.Unit `json:"removed"`
	Counters *Counters   `json:"counters,omitempty"`
}

// RowFacts are a row's numbers for the backlog panels.
type RowFacts struct {
	CreatedAt    time.Time  `json:"created_at,omitzero"`
	ChangedFiles int        `json:"changed_files"`
	Checks       CheckFacts `json:"checks"`
	Turns        TurnFacts  `json:"turns"`
	// Next is sorted by At and holds only future instants.
	Next []Upcoming `json:"next,omitempty"`
}

type CheckFacts struct {
	State   string   `json:"state"`
	Failing []string `json:"failing,omitempty"`
}

// TurnFacts counts every turn of the pull request; Open is those sent,
// started or asked, and CostUSD counts an unreported cost as 0.
type TurnFacts struct {
	Count      int     `json:"count"`
	Open       int     `json:"open"`
	LastIntent string  `json:"last_intent,omitempty"`
	Tokens     int64   `json:"tokens"`
	CostUSD    float64 `json:"cost_usd"`
}

// Upcoming is a future instant of the row: What is gate, unmute or
// brief_retry.
type Upcoming struct {
	At   time.Time `json:"at"`
	What string    `json:"what"`
}

const (
	UpcomingGate       = "gate"
	UpcomingUnmute     = "unmute"
	UpcomingBriefRetry = "brief_retry"
)

// QuestionView is a turn that waits on the user: asked, failed or
// unrecorded, with its last record's question and outcome.
type QuestionView struct {
	RecordID    int64           `json:"record_id"`
	State       string          `json:"state"`
	Kind        string          `json:"kind"`
	Text        string          `json:"text"`
	Options     []record.Option `json:"options,omitempty"`
	Recommend   string          `json:"recommend,omitempty"`
	Publishes   bool            `json:"publishes,omitempty"`
	Verdict     string          `json:"verdict,omitempty"`
	Blocking    int             `json:"blocking"`
	NonBlocking int             `json:"non_blocking"`
	LastMessage string          `json:"last_message,omitempty"`
}

// AttentionAction ids are catalog ids, gtd:<verb> (gtd:<verb>:<mode> with
// the delegate's mode) for an entry's verb, answer:option:<id>, answer:yes,
// answer:no and answer:text for the answer's choices, and reply:save and
// reply:send for a reply typed in the notification.
// The actions of an Inbox entry's notification: Draft for me delegates it
// headless, Reply saves the typed text to the Outbox as a reply on its unit
// and Send reply sends it at once.
const (
	AttentionDraftForMe = "gtd:delegate:headless"
	AttentionReply      = "reply:save"
	AttentionSendReply  = "reply:send"
)

type AttentionAction struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	TextInput bool   `json:"text_input,omitempty"`
	// Basis is the row state's basis the action was offered on.
	Basis string `json:"basis,omitempty"`
}

// Attention is a notification as the daemon decides it; ID is stable for as
// long as what it notifies lasts.
type Attention struct {
	ID       string            `json:"id"`
	Category string            `json:"category"`
	Title    string            `json:"title"`
	Subtitle string            `json:"subtitle"`
	Body     string            `json:"body"`
	Image    string            `json:"image,omitempty"`
	Actions  []AttentionAction `json:"actions"`
}

// TurnView is the pull request's newest turn with its records and answers.
type TurnView struct {
	Turn        Turn         `json:"turn"`
	Records     []TurnRecord `json:"records"`
	Answers     []TurnAnswer `json:"answers"`
	LastMessage string       `json:"last_message,omitempty"`
}

type Turn struct {
	Seq        int64     `json:"seq"`
	Intent     string    `json:"intent"`
	ID         string    `json:"id"`
	Trigger    string    `json:"trigger"`
	Guidance   string    `json:"guidance"`
	Head       string    `json:"head"`
	SentTo     string    `json:"sent_to"`
	Session    string    `json:"session"`
	State      string    `json:"state"`
	SentAt     time.Time `json:"sent_at"`
	StartedAt  time.Time `json:"started_at,omitzero"`
	EndedAt    time.Time `json:"ended_at,omitzero"`
	ResumeOf   int64     `json:"resume_of,omitempty"`
	ReplacedBy int64     `json:"replaced_by,omitempty"`
	Before     string    `json:"before,omitempty"`
	// Agent is the agent that ran the turn, BriefRun the brief run its
	// instructions carried and AgentSession the agent session that started it.
	Agent        string `json:"agent,omitempty"`
	BriefRun     int64  `json:"brief_run,omitempty"`
	AgentSession string `json:"agent_session,omitempty"`
	// Mode is "" for a session turn; Workflow the workflow that started it.
	Mode     string `json:"mode,omitempty"`
	Workflow string `json:"workflow,omitempty"`
}

type TurnRecord struct {
	ID           int64         `json:"id"`
	TurnSeq      int64         `json:"turn_seq"`
	Session      string        `json:"session"`
	AgentSession string        `json:"agent_session"`
	Head         string        `json:"head"`
	Hash         string        `json:"hash"`
	At           time.Time     `json:"at"`
	Acked        bool          `json:"acked"`
	Record       record.Record `json:"record"`
}

type TurnAnswer struct {
	ID       int64     `json:"id"`
	TurnSeq  int64     `json:"turn_seq"`
	Text     string    `json:"text"`
	OptionID string    `json:"option_id,omitempty"`
	Yes      bool      `json:"yes"`
	No       bool      `json:"no,omitempty"`
	Source   string    `json:"source"`
	Route    string    `json:"route"`
	Session  string    `json:"session,omitempty"`
	At       time.Time `json:"at"`
}

type Detail struct {
	Row    Row        `json:"row"`
	Events []Event    `json:"events"`
	Brief  *BriefView `json:"brief,omitempty"`
	// ProfileChain names the review profile layers a round here reads.
	ProfileChain string    `json:"profile_chain"`
	Turn         *TurnView `json:"turn,omitempty"`
	// Card and Items are the unit card's (#unit-card).
	Card  *card.Snapshot `json:"card,omitempty"`
	Items []items.Item   `json:"items"`
}

type Workspace struct {
	// Manager owns the workspace; ID is its id there.
	Manager     string   `json:"manager,omitempty"`
	ID          string   `json:"id"`
	DisplayName string   `json:"display_name"`
	Status      string   `json:"status"`
	IsReview    bool     `json:"is_review"`
	AgentStates []string `json:"agent_states"`
	Comment     string   `json:"comment"`
	// IsMain is the repository's main checkout on the pull request's branch,
	// which the loop never removes.
	IsMain bool `json:"is_main"`
}

// Override is what the user set on a unit; the zero Override leaves the
// wire.
type Override struct {
	MutedUntil           time.Time `json:"muted_until"`
	MuteUntilStateChange bool      `json:"mute_until_state_change"`
	MutedKind            string    `json:"muted_kind"`
	MutedWaitingOn       string    `json:"muted_waiting_on"`
	Hold                 bool      `json:"hold"`
	Adopted              bool      `json:"adopted"`
	Tracked              bool      `json:"tracked"`
	NextGuidance         string    `json:"next_guidance"`
}

type Event struct {
	PR    pr.Ref    `json:"pr"`
	Kind  string    `json:"kind"`
	Key   string    `json:"key"`
	Actor string    `json:"actor"`
	At    time.Time `json:"at"`
	Data  string    `json:"data"`
}

// IsZero reports an override that sets nothing.
func (o Override) IsZero() bool {
	return o.MutedUntil.IsZero() && o == Override{MutedUntil: o.MutedUntil}
}

// PendingRound is an automatic round waiting for the user's answer; a
// client counts down to Deadline itself.
type PendingRound struct {
	Intent   string    `json:"intent"`
	Reason   string    `json:"reason"`
	Since    time.Time `json:"since"`
	Deadline time.Time `json:"deadline"`
}

type BriefSummary struct {
	Kind          string   `json:"kind"`
	Size          string   `json:"size"`
	Risk          string   `json:"risk"`
	Goal          string   `json:"goal"`
	OpenQuestions int      `json:"open_questions"`
	Tags          []string `json:"tags,omitempty"`
}

type Brief struct {
	Goal          string   `json:"goal"`
	Context       string   `json:"context"`
	Kind          string   `json:"kind"`
	Size          string   `json:"size"`
	Risk          string   `json:"risk"`
	RiskWhy       string   `json:"risk_why"`
	OpenQuestions []string `json:"open_questions"`
	Tags          []string `json:"tags,omitempty"`
}

type BriefView struct {
	State string    `json:"state"`
	Brief *Brief    `json:"brief,omitempty"`
	Model string    `json:"model,omitempty"`
	At    time.Time `json:"at,omitzero"`
	Error string    `json:"error,omitempty"`
	// Stale marks a brief that is due again: the pull request moved on.
	Stale bool `json:"stale"`
}

type BriefStatus struct {
	Ready       int       `json:"ready"`
	Pending     int       `json:"pending"`
	Failed      int       `json:"failed"`
	LastError   string    `json:"last_error,omitempty"`
	PausedUntil time.Time `json:"paused_until,omitzero"`
}

type RateLimit struct {
	Cost      int       `json:"cost"`
	Remaining int       `json:"remaining"`
	Limit     int       `json:"limit"`
	ResetAt   time.Time `json:"reset_at"`
}

type Health struct {
	Version string `json:"version"`
	// Build names the cockpit the daemon serves; a page that loaded another
	// reloads.
	Build     string       `json:"build"`
	Jobs      []JobStatus  `json:"jobs"`
	Warnings  []string     `json:"warnings"`
	RateLimit RateLimit    `json:"rate_limit"`
	SlowSync  bool         `json:"slow_sync"`
	Viewer    string       `json:"viewer"`
	Paused    bool         `json:"paused"`
	PausedAt  time.Time    `json:"paused_at,omitzero"`
	App       *AppStatus   `json:"app,omitempty"`
	Brief     *BriefStatus `json:"brief,omitempty"`
	// Briefs is the brief breaker; nil when briefs are off.
	Briefs *BriefsHealth `json:"briefs,omitempty"`
	// Notifier is whether the menu bar follows the daemon's events.
	Notifier bool `json:"notifier"`
	// StalledReviewHours is stalled.review, for a client that judges a pull
	// request the inventory does not hold.
	StalledReviewHours float64 `json:"stalled_review_hours"`
	// WorkspaceManagers name the workspace managers the daemon runs on, in
	// their configured order, such as "Claude Desktop" and "Orca".
	WorkspaceManagers []string `json:"workspace_managers,omitempty"`
	// GitAccess is, per remote host, how the daemon's own git network
	// operations went, sorted by host.
	GitAccess []GitAccess `json:"git_access,omitempty"`
}

// GitAccess is the latest git network operation the daemon ran against a
// host and its latest success; LastOK is nil when none succeeded yet.
type GitAccess struct {
	Host   string            `json:"host"`
	Last   GitAccessOutcome  `json:"last"`
	LastOK *GitAccessOutcome `json:"last_ok,omitempty"`
}

// GitAccessOutcome is one git network operation: when, which (such as
// "fetch of acme/widget#7") and its error, "" when it succeeded.
type GitAccessOutcome struct {
	At    time.Time `json:"at"`
	Op    string    `json:"op"`
	Error string    `json:"error,omitempty"`
}

// GitAccessRecord is an outcome another process saw against Host, such as
// doctor --git's ls-remote, for the daemon to keep with its own.
type GitAccessRecord struct {
	Host string `json:"host"`
	GitAccessOutcome
}

type JobStatus struct {
	Name    string        `json:"name"`
	Every   time.Duration `json:"every"`
	LastRun time.Time     `json:"last_run"`
	LastOK  time.Time     `json:"last_ok"`
	LastErr string        `json:"last_error"`
	Running bool          `json:"running"`
}

// MeterStat is the metered cost of one resource, or of one operation of it
// when Op is set.
type MeterStat struct {
	Res         string `json:"res"`
	Op          string `json:"op,omitempty"`
	Calls       int64  `json:"calls"`
	Spawns      int64  `json:"spawns,omitempty"`
	Errors      int64  `json:"errors,omitempty"`
	NotModified int64  `json:"not_modified,omitempty"`
	Points      int64  `json:"points,omitempty"`
	Bytes       int64  `json:"bytes,omitempty"`
	Rows        int64  `json:"rows,omitempty"`
	DurMS       int64  `json:"dur_ms,omitempty"`
	MaxMS       int64  `json:"max_ms,omitempty"`
}

// Load is the daemon's cost since Since, per resource (Op is empty).
type Load struct {
	Since time.Time   `json:"since"`
	Stats []MeterStat `json:"stats"`
	// CLIOps are the workspace manager's operations that spawned its CLI,
	// per op.
	CLIOps []MeterStat `json:"cli_ops,omitempty"`
	// Fallbacks is how many calls went to the CLI because the manager's
	// runtime drifted; nil when the manager does not count them.
	Fallbacks *int64 `json:"fallbacks,omitempty"`
}

// AppStatus is what the menu bar app reports about itself.
type AppStatus struct {
	State         string `json:"state"`
	Notifications string `json:"notifications"`
}

type ActionRequest struct {
	Until      string `json:"until"`
	UntilState bool   `json:"until_state"`
	Guidance   string `json:"guidance"`
	AgentHome  string `json:"agent_home,omitempty"`
	Force      bool   `json:"force,omitempty"`
	// Basis is the action state's basis the client saw; the daemon refuses
	// the action when the row no longer rests on it.
	Basis string `json:"basis,omitempty"`
	// Inputs are the intent's declared inputs, by name.
	Inputs map[string]any `json:"inputs,omitempty"`
	// OptionID, Yes, No and Source answer a question; Guidance is the
	// answer's text, a note to an option, yes or no. Source is cockpit,
	// notification, tui or terminal.
	OptionID string `json:"option_id,omitempty"`
	Yes      bool   `json:"yes,omitempty"`
	No       bool   `json:"no,omitempty"`
	Source   string `json:"source,omitempty"`
	// NoBrief starts a review, address or run without waiting for the
	// brief.
	NoBrief bool `json:"no_brief,omitempty"`
	// Items and Entry tie what the action's turn drafts to the item ids of the unit and the entry it
	// was asked from.
	Items []string `json:"items,omitempty"`
	Entry string   `json:"entry,omitempty"`
}

// ActionResult carries, for an action an intake runs, the intake that
// accepted it instead of the action's output.
// AnswerQueued is the output line of an answer that waits for its busy
// agent to stop.
const AnswerQueued = "answer queued; it is typed into the agent session when the agent stops"

type ActionResult struct {
	OK     bool     `json:"ok"`
	Output []string `json:"output,omitempty"`
	Intake *Intake  `json:"intake,omitempty"`
	// Seq is the turn an intent started headless, answered 202; 0 otherwise.
	Seq int64 `json:"seq,omitempty"`
}

// PauseRequest pauses or resumes the repositories in Repos and the pull
// requests in PRs; naming neither pauses or resumes everything.
type PauseRequest struct {
	Paused bool     `json:"paused"`
	Repos  []string `json:"repos,omitempty"` // owner/repo
	PRs    []string `json:"prs,omitempty"`   // owner/repo#n
}

type ExploreRequest struct {
	Query string `json:"query"`
}

// Candidate is a pull request Explore found, with what deciding whether to
// help on it needs.
type Candidate struct {
	Ref            pr.Ref    `json:"ref"`
	URL            string    `json:"url"`
	Title          string    `json:"title"`
	Author         string    `json:"author"`
	State          string    `json:"state"`
	IsDraft        bool      `json:"draft"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	Additions      int       `json:"additions"`
	Deletions      int       `json:"deletions"`
	ChangedFiles   int       `json:"changed_files"`
	HeadBranch     string    `json:"head"`
	BaseBranch     string    `json:"base"`
	ReviewDecision string    `json:"review_decision"`
	ReviewRequests []string  `json:"review_requests"` // logins and team:org/slug
	ChecksState    string    `json:"checks"`
	Comments       int       `json:"comments"`
	// MyReview is the viewer's latest review state, "" when none.
	MyReview       string     `json:"my_review"`
	MyReviewOnHead bool       `json:"my_review_on_head"`
	Brief          *BriefView `json:"brief,omitempty"`
	// Actions are what the pull request offers now, as its row would.
	Actions []ActionState `json:"actions"`
}

type ExploreResult struct {
	Query      string      `json:"query"` // what GitHub was asked
	Read       string      `json:"read"`  // how the input was read
	Empty      string      `json:"empty,omitempty"`
	Total      int         `json:"total"`
	Candidates []Candidate `json:"candidates"`
	Cost       int         `json:"cost"`
	RateLimit  RateLimit   `json:"rate_limit"`
	Recent     []string    `json:"recent"`
}

type RemoveDoneResult struct {
	Removed []pr.Ref `json:"removed"`
	Kept    []Kept   `json:"kept"`
}

type Kept struct {
	Ref       pr.Ref `json:"ref"`
	Reason    string `json:"reason"`
	Detail    string `json:"detail,omitempty"`
	Source    string `json:"source,omitempty"`
	Forceable bool   `json:"forceable,omitempty"`
}

type ActivityItem struct {
	Event  Event  `json:"event"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	Author string `json:"author"`
	State  string `json:"state"`
	Mine   bool   `json:"mine"`
}

type CheckDetail struct {
	Name    string   `json:"name"`
	URL     string   `json:"url,omitempty"`
	Summary string   `json:"summary,omitempty"`
	Steps   []string `json:"steps,omitempty"`
}

// Info is the cockpit address the daemon writes for the command line.
type Info struct {
	Port  int    `json:"port"`
	Token string `json:"token"`
}
