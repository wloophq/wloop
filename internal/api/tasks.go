package api

import (
	"cmp"
	"encoding/json"
	"net/url"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// The tasks' routes: ViewsPath serves the ViewsCatalog, TasksPath lists a view (query parameters view, area,
// context as "a,b" and since, a Since's String) and takes a TaskCreate, TaskVerb runs a verb, CountersPath
// serves the counters of the query parameter area (narrowed to the tasks holding each CountersTerm), DigestPath the daily digest of the query parameter day
// (YYYY-MM-DD).
const (
	ViewsPath    = "/api/views"
	TasksPath    = "/api/tasks"
	TaskDetail   = TasksPath + "/{id}"
	TaskVerb     = TaskDetail + "/{verb}"
	CountersPath = "/api/counters"
	DigestPath   = "/api/digest"
)

// A task's timeline, runs and chat, and the gates and runs as resources: TimelinePath serves a task's
// []TimelineEvent, newest first (query parameters kinds as "a,b", before a Seq and limit), RunsPath its []RunView,
// ChatPath takes a ChatRequest, AttachPath makes the caller's session, from SessionHeader, a run of the task (it takes an AttachRequest
// and answers a ByHandResult).
// GatesPath lists []GateView (query parameter status as "a,b", open when absent), GatePath resolves gate id with intent (a TaskRequest's args carry an
// answer). RunOpen opens a run's workspace in its manager, RunEnter enters its session, RunTranscript pages
// its transcript as a TranscriptPage (query parameters from and limit).
const (
	TimelinePath  = TaskDetail + "/timeline"
	RunsPath      = TaskDetail + "/runs"
	ChatPath      = TaskDetail + "/chat"
	AttachPath    = TaskDetail + "/attach"
	GatesPath     = "/api/gates"
	GatePath      = GatesPath + "/{id}/{intent}"
	RunOpen       = "/api/runs/{seq}/open"
	RunEnter      = "/api/runs/{seq}/enter"
	RunTranscript = "/api/runs/{seq}/transcript"
)

// TaskPath is the path of task id, followed by the segments of rest.
func TaskPath(id string, rest ...string) string {
	p := TasksPath + "/" + url.PathEscape(id)
	for _, r := range rest {
		p += "/" + url.PathEscape(r)
	}
	return p
}

type ViewsCatalog struct {
	Views    []TaskViewSpec `json:"views"` // core views in spec 9.1 order, review only once registered
	Areas    []string       `json:"areas"`
	Contexts []string       `json:"contexts"`
	Verbs    []VerbSpec     `json:"verbs"` // key from internal/keys
	// Review are the ids of the views the review walks, in order.
	Review []string `json:"review,omitempty"`
	// States are the profile's states in their declared order; Props its task properties.
	States []StateSpec `json:"states,omitempty"`
	Props  []PropSpec  `json:"props,omitempty"`
}

// TaskViewSpec is a view of views.toml: Over is what it lists (tasks or gates), Group the fact it groups by,
// Key the key that jumps to it, Badge whether the badge counts it, HideDisabled whether it hides the tasks of a
// disabled plugin, RemoveClean whether it offers to remove its tasks' clean workspaces (RemoveCleanPath).
type TaskViewSpec struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	Over         string `json:"over,omitempty"`
	Group        string `json:"group,omitempty"`
	Key          string `json:"key,omitempty"`
	Badge        bool   `json:"badge,omitempty"`
	HideDisabled bool   `json:"hide_disabled,omitempty"`
	RemoveClean  bool   `json:"remove_clean,omitempty"`
}

// StateSpec is a state of tasks.toml and the category Go reads of it.
type StateSpec struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Category string `json:"category"`
}

// PropSpec is a task property of tasks.toml: its type, an enum's values, who may write it, and the role of the
// loop's that acts on it ("" for none): area, contexts, star or deadline.
type PropSpec struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Values  []string `json:"values,omitempty"`
	Writers []string `json:"writers,omitempty"`
	Role    string   `json:"role,omitempty"`
}

// PropOf is the property of c that takes role; false when none does.
func (c ViewsCatalog) PropOf(role string) (PropSpec, bool) {
	for _, p := range c.Props {
		if p.Role == role {
			return p, true
		}
	}
	return PropSpec{}, false
}

// Starred says whether e holds the starred value, the first, of the property c's star role acts on.
func (c ViewsCatalog) Starred(e TaskView) bool {
	p, ok := c.PropOf(string(task.RoleStar))
	return ok && len(p.Values) > 0 && e.PropText(p.Name) == p.Values[0]
}

// VerbSpec is a verb a task offers: Handler is what it runs besides its move, as the profile names it ("" for
// none), From the states it moves a task from (every state when empty), To the state it moves it to ("" for none).
type VerbSpec struct {
	Verb    string `json:"verb"`
	Handler string `json:"handler,omitempty"`
	Label   string `json:"label"`
	Summary string `json:"summary,omitempty"`
	Input   string `json:"input,omitempty"` // the question a verb that asks for a value puts first
	// Send is what a reply of a verb whose handler is reply does by default: "now" sends it, "draft" holds it
	// for approval; every client's reply composer starts on it.
	Send string   `json:"send,omitempty"`
	Key  string   `json:"key"`
	From []string `json:"from,omitempty"`
	To   string   `json:"to,omitempty"`
}

type TaskView struct {
	ID         string      `json:"id"`
	State      string      `json:"state"`
	Category   string      `json:"category,omitempty"`
	Title      string      `json:"title"`
	Notes      string      `json:"notes,omitempty"`
	Changed    bool        `json:"changed,omitempty"`
	Units      []unit.Unit `json:"units"`
	UnitLabels []string    `json:"unit_labels,omitempty"` // Units as a person reads them, in order
	Cards      []CardView  `json:"cards,omitempty"`       // detail only
	Verbs      []VerbState `json:"verbs"`
	// Actions are the intents that serve the task's own unit, such as start.
	Actions   []ActionState `json:"actions,omitempty"`
	Attention *Attention    `json:"attention,omitempty"`
	Created   time.Time     `json:"created"`
	Updated   time.Time     `json:"updated"`

	// A project's: whether it takes its children one at a time, and its review.
	Sequential  bool      `json:"sequential,omitempty"`
	ReviewEvery string    `json:"review_every,omitempty"` // a Go duration
	ReviewedAt  time.Time `json:"reviewed_at,omitzero"`
	ReviewDue   bool      `json:"review_due,omitempty"`

	// Props are the task's properties by name, each in its type's JSON: a string, a number, an RFC 3339 time,
	// a Go duration string, a list of strings, or a list of people for an actor.
	Props map[string]json.RawMessage `json:"props,omitempty"`
	// OwedBy is who owes the task's next step, as the profile's owed_by says: "me" or a person's key.
	OwedBy string `json:"owed_by,omitempty"`
	// Askers are who waits on you through the task, by key: the requesters of its open asks to you.
	Askers []string `json:"askers,omitempty"`
	// ExtractionLag is how long the oldest message of its links has waited to be extracted, a Go duration;
	// empty when none waits.
	ExtractionLag string `json:"extraction_lag,omitempty"`
	// Asks are the items the task holds, detail only.
	Asks  []AskView   `json:"asks,omitempty"`
	Gates []GateView  `json:"gates,omitempty"` // the open ones
	Runs  []RunView   `json:"runs,omitempty"`  // detail only
	Brief *RecordView `json:"brief,omitempty"`
	Wake  *WakeView   `json:"wake,omitempty"`
	// Parent is the parent task's id; Children are its children's, in sibling order.
	Parent   string   `json:"parent,omitempty"`
	Children []string `json:"children,omitempty"`
}

// AskView is an item a task serves, as its asker put it, with the source messages it quotes.
type AskView struct {
	Item      string   `json:"item"`
	Summary   string   `json:"summary"`
	Asker     string   `json:"asker,omitempty"`
	Permalink string   `json:"permalink,omitempty"`
	Quotes    []string `json:"quotes,omitempty"`
}

// GateView is a gate as clients offer it: Task and Unit what it is on ("" when none), Text its words from
// gates.toml, Intents what a person may resolve it with, in order, Default the intent its Deadline resolves it
// with, Delivery a draft gate's delivery state (held, queued, sending, sent, failed, dropped), Subject its subject as
// its kind serves it: a draft gate's is its Draft.
type GateView struct {
	ID       int64           `json:"id"`
	Task     string          `json:"task,omitempty"`
	Unit     string          `json:"unit,omitempty"`
	Kind     string          `json:"kind"`
	Name     string          `json:"name,omitempty"`
	Status   string          `json:"status"`
	Text     string          `json:"text,omitempty"`
	Intents  []IntentView    `json:"intents"`
	Deadline time.Time       `json:"deadline,omitzero"`
	Default  string          `json:"default,omitempty"`
	Delivery string          `json:"delivery,omitempty"`
	Subject  json.RawMessage `json:"subject,omitempty"`
}

// IntentView is an intent a gate offers; Answer says it takes text, as a question's answer.
type IntentView struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Answer bool   `json:"answer,omitempty"`
}

// RunView is a run of a task: the turn Seq of Intent in State, where it ran, its session and that session's
// state, the command that resumes it, and the run actions it offers (open, enter, transcript).
type RunView struct {
	Seq          int64    `json:"seq"`
	Intent       string   `json:"intent"`
	State        string   `json:"state"`
	Manager      string   `json:"manager,omitempty"`
	Workspace    string   `json:"workspace,omitempty"`
	Worktree     string   `json:"worktree,omitempty"`
	Session      string   `json:"session,omitempty"`
	SessionState string   `json:"session_state,omitempty"`
	Resume       string   `json:"resume,omitempty"`
	Actions      []string `json:"actions,omitempty"`
}

// TranscriptLine is one message of a run's agent transcript: who wrote it (user or assistant) and its text.
type TranscriptLine struct {
	Role string    `json:"role"`
	Text string    `json:"text"`
	At   time.Time `json:"at,omitzero"`
}

// TranscriptPage is what RunTranscript serves: the transcript's lines from the asked one, oldest first, and
// how many lines it holds in all.
type TranscriptPage struct {
	Lines []TranscriptLine `json:"lines"`
	Total int              `json:"total"`
}

// AttachRequest is what AttachPath reads of the calling session's environment besides SessionHeader: its
// directory, pane, agent session, agent home and agent.
type AttachRequest struct {
	Cwd          string `json:"cwd"`
	Pane         string `json:"pane,omitempty"`
	AgentSession string `json:"agent_session,omitempty"`
	Home         string `json:"home,omitempty"`
	Agent        string `json:"agent,omitempty"`
}

// RecordView is a record of a declared kind, such as a brief, with its data as its schema holds it.
type RecordView struct {
	Kind string          `json:"kind"`
	At   time.Time       `json:"at"`
	Data json.RawMessage `json:"data"`
}

// TimelineEvent is one event of a task's timeline: Seq its stable position, which pages it, and Unit the unit it
// happened on, "" for the task itself.
type TimelineEvent struct {
	Seq   int64           `json:"seq"`
	Kind  string          `json:"kind"`
	Sub   string          `json:"sub,omitempty"`
	Actor string          `json:"actor,omitempty"`
	At    time.Time       `json:"at"`
	Unit  string          `json:"unit,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// ChatRequest is a message to a task's agent; Intent picks the intent of the turn it starts when no session
// is live, the profile's chat intent when "".
type ChatRequest struct {
	Text   string `json:"text"`
	Intent string `json:"intent,omitempty"`
}

// WakeView is a task's wake: until a time, or When, a condition, holds; Return is the state it goes back to.
type WakeView struct {
	Until  time.Time `json:"until,omitzero"`
	When   string    `json:"when,omitempty"`
	Return string    `json:"return,omitempty"`
}

type ItemView struct {
	Unit    unit.Unit `json:"unit"`
	ID      string    `json:"id"`
	Summary string    `json:"summary"`
	State   string    `json:"state"`
	Kind    string    `json:"kind"`
}

// CardView is a unit's stored card on a task's detail.
type CardView = card.Snapshot

type VerbState struct {
	Verb    string `json:"verb"`
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason,omitempty"`
	// Runners are, on an enabled delegate, the runners of the action it runs: headless, session or both.
	Runners []RunnerState `json:"runners,omitempty"`
}

// RunnerState is a runner a delegate runs on; a session is disabled when the unit has nowhere to run one.
type RunnerState struct {
	Runner  string `json:"runner"`
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason,omitempty"`
}

// TasksPage is a page of a view; a view over gates lists Gates and no Tasks.
type TasksPage struct {
	Epoch    string     `json:"epoch"`
	Rev      uint64     `json:"rev"`
	Full     bool       `json:"full"`
	Tasks    []TaskView `json:"tasks"`
	Gates    []GateView `json:"gates,omitempty"`
	Removed  []string   `json:"removed"`
	Counters Counters   `json:"counters"`
	// Loading are the plugins whose units are not all reported yet.
	Loading []Loading `json:"loading,omitempty"`
}

// TaskCreate creates a task: a Title alone captures it. Brief is its brief's markdown, Props its properties
// as their text, Placement a hint of where its work runs, such as a repository, and Unit the unit it is about.
type TaskCreate struct {
	Title     string            `json:"title"`
	Brief     string            `json:"brief,omitempty"`
	Props     map[string]string `json:"props,omitempty"`
	Placement string            `json:"placement,omitempty"`
	Unit      *unit.Unit        `json:"unit,omitempty"`
}

// TaskRequest args: waiting {on, resurface}; schedule {until (RFC3339 or YYYY-MM-DD), on_activity "true"};
// project {id | title (creates one)}; context {set "a,b"}; area {set}; answer: as ActionRequest fields;
// delegate {guidance, mode "headless" | "session"}; reply {text | reaction, kind "reply" | "react",
// send "now"}.
type TaskRequest struct {
	Args   map[string]string `json:"args,omitempty"`
	Source string            `json:"source"`
}

type TaskResult struct {
	Task  TaskView `json:"task"`
	Lines []string `json:"lines,omitempty"`
}

type Counters struct {
	Area     string         `json:"area"`
	Views    map[string]int `json:"views"`
	Areas    map[string]int `json:"areas"`
	Contexts map[string]int `json:"contexts"`
	Badge    int            `json:"badge"`
}

// Message is the subject of a question, post or proposal gate, as GateView.Subject serves it: what was asked,
// with the user's answers to it.
type Message struct {
	ID        int64           `json:"id"`
	Unit      unit.Unit       `json:"unit"`
	Kind      string          `json:"kind"`
	Text      string          `json:"text"`
	Options   []record.Option `json:"options,omitempty"`
	Recommend string          `json:"recommend,omitempty"`
	Confirm   bool            `json:"confirm,omitempty"`
	Origin    string          `json:"origin"`
	CreatedAt time.Time       `json:"created_at"`
	// Answers are the user's answers to the message, oldest first, each with when the agent read it.
	Answers []TurnAnswer `json:"answers,omitempty"`
}

// UnitLabel is the i-th unit as a person reads it.
func (e TaskView) UnitLabel(i int) string {
	if i < len(e.UnitLabels) && e.UnitLabels[i] != "" {
		return e.UnitLabels[i]
	}
	return e.Units[i].Label()
}

// PropText is the property name's text: a string, an enum or a duration; "" when unset or of another type.
func (e TaskView) PropText(name string) string {
	var s string
	_ = json.Unmarshal(e.Props[name], &s)
	return s
}

// PropList is the property name's list of strings; nil when unset or of another type.
func (e TaskView) PropList(name string) []string {
	var l []string
	_ = json.Unmarshal(e.Props[name], &l)
	return l
}

// PropTime is the property name's time; zero when unset or of another type.
func (e TaskView) PropTime(name string) time.Time {
	var t time.Time
	_ = json.Unmarshal(e.Props[name], &t)
	return t
}

// VerbOf is the verb of c that runs handler: the first whose handler it is, or with no handler, whose name it is, as
// the tasks service binds a kind's own verbs; false when none does.
func (c ViewsCatalog) VerbOf(handler string) (string, bool) {
	for _, v := range c.Verbs {
		if cmp.Or(v.Handler, v.Verb) == handler {
			return v.Verb, true
		}
	}
	return "", false
}

// HandlerOf is the handler verb runs in c: its profile handler, else its own name, as the tasks service binds it.
func (c ViewsCatalog) HandlerOf(verb string) string {
	for _, v := range c.Verbs {
		if v.Verb == verb {
			return cmp.Or(v.Handler, v.Verb)
		}
	}
	return verb
}

// MessageOf is the message of e's first open question, post or proposal gate, and that gate.
func (e TaskView) MessageOf() (Message, GateView, bool) {
	for _, g := range e.Gates {
		if g.Status != "open" || len(g.Subject) == 0 || !slices.Contains(messageGateKinds, g.Kind) {
			continue
		}
		var m Message
		if json.Unmarshal(g.Subject, &m) == nil && m.ID != 0 {
			return m, g, true
		}
	}
	return Message{}, GateView{}, false
}

// messageGateKinds are the kinds of the gates a message is the subject of.
var messageGateKinds = []string{"question", "post", "proposal"}
