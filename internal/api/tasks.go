package api

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// The tasks' routes: ViewsPath serves the ViewsCatalog, TasksPath lists a view (query parameters view, area,
// context as "a,b", since, a Since's String, and TasksUnits) and takes a TaskCreate, TaskVerb runs a verb, CountersPath
// serves the counters of the query parameter area (narrowed to the tasks the lens query CountersQuery keeps), DigestPath the daily digest of the query parameter day
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
// []TimelineEvent, newest first (query parameters kinds as "a,b", before a Seq and limit; as=nodes answers
// []TimelineNode instead), RunsPath its []RunView,
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

// TasksUnits is the query parameter of TasksPath, repeated, that names a unit as its String does: a page
// since a position also holds the tasks holding it, as a change named it.
const TasksUnits = "units"

// TasksMatch is the query parameter of TasksPath that lists every open task whose title holds it, case
// aside, whatever the view, for a picker of another task; both clients read the same list. The page is Full
// and its views hold only the id, title, state, category and units of each task, the newest change first.
const TasksMatch = "match"

// TasksExcept is the query parameter of a TasksMatch request that names the task the picker picks for, which
// the list leaves out, as its id reads.
const TasksExcept = "except"

// ParseUnits reads the TasksUnits of a request.
func ParseUnits(raw []string) ([]unit.Unit, error) {
	out := make([]unit.Unit, 0, len(raw))
	for _, r := range raw {
		provider, key, ok := strings.Cut(r, ":")
		if !ok || provider == "" || key == "" {
			return nil, fmt.Errorf("unit %q is not <provider>:<key>", r)
		}
		out = append(out, unit.Unit{Provider: unit.ProviderID(provider), Key: key})
	}
	return out, nil
}

// TaskPath is the path of task id, followed by the segments of rest.
func TaskPath(id string, rest ...string) string {
	p := TasksPath + "/" + url.PathEscape(id)
	for _, r := range rest {
		p += "/" + url.PathEscape(r)
	}
	return p
}

// LensWords are what a list's lens bar says, templates whose only actions are {{.name}} substitutions:
// DefaultGroup and BoardGroup over label, Copied over count and view, CopiedGrouped over those and group, TagKey
// over the tag facet's label and key, TagKeyEmpty over key, Grouped, Sorted and Descending over label.
type LensWords struct {
	DefaultGroup  string `json:"default_group"`
	BoardGroup    string `json:"board_group"`
	NoGroup       string `json:"no_group"`
	Copied        string `json:"copied"`
	CopiedGrouped string `json:"copied_grouped"`
	TagKey        string `json:"tag_key"`
	TagKeyEmpty   string `json:"tag_key_empty"`
	Grouped       string `json:"grouped"`
	Sorted        string `json:"sorted"`
	Descending    string `json:"descending"`
	NoLens        string `json:"no_lens"`
	// RowWords are what a row says beside its facets.
	RowWords
}

// RowWords are the words of a row: its age, its facet buttons' title and aria-label, the head of a mixed list's
// unit, the flags and tooltips no facet carries. A plugin's ui.toml may replace any for its own rows.
type RowWords struct {
	AgeNow           string `json:"age_now"`
	AgeMinutes       string `json:"age_minutes"`
	AgeHours         string `json:"age_hours"`
	AgeDays          string `json:"age_days"`
	FilterLabel      string `json:"filter_label"`
	FilterTitle      string `json:"filter_title"`
	FilterTitleGroup string `json:"filter_title_group"`
	FilterGroup      string `json:"filter_group"`
	ListCount        string `json:"list_count"`
	ListEmpty        string `json:"list_empty"`
	CountdownTitle   string `json:"countdown_title"`
	CountdownSending string `json:"countdown_sending"`
	CountdownSeconds string `json:"countdown_seconds"`
	CountdownMinutes string `json:"countdown_minutes"`
	TipNamed         string `json:"tip_named"`
	TipKind          string `json:"tip_kind"`
	TipWhy           string `json:"tip_why"`
	LegendTitle      string `json:"legend_title"`
	SortBy           string `json:"sort_by"`
	ColUnit          string `json:"col_unit"`
	Stale            string `json:"stale"`
	StaleTitle       string `json:"stale_title"`
	Briefing         string `json:"briefing"`
	BriefingTitle    string `json:"briefing_title"`
	BriefingAttempt  string `json:"briefing_attempt"`
	BriefingError    string `json:"briefing_error"`
	Questions        string `json:"questions"`
	QuestionsTitle   string `json:"questions_title"`
	Starred          string `json:"starred"`
	Until            string `json:"until"`
	Due              string `json:"due"`
	Unread           string `json:"unread"`
	UnreadTitle      string `json:"unread_title"`
	ReviewDue        string `json:"review_due"`
	NoNextAction     string `json:"no_next_action"`
	OpenChildren     string `json:"open_children"`
	Sequential       string `json:"sequential"`
	Every            string `json:"every"`
	Reviewed         string `json:"reviewed"`
	More             string `json:"more"`
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
	// Facets are the profile's task facets, DefaultSort what clients call a view's own order.
	Facets      []FacetSpec `json:"facets,omitempty"`
	DefaultSort string      `json:"default_sort,omitempty"`
	Lens        LensWords   `json:"lens"`
	// DetailVerbs are the verbs a task's detail shows as buttons, in order (tasks.toml's detail.verbs); Effects
	// the sentence that says what each effect does when a draft is sent, by effect name (gates.toml's effects).
	DetailVerbs []string          `json:"detail_verbs"`
	Effects     map[string]string `json:"effects"`
	// DraftPurposes name what a draft is for, by purpose, and DraftStates where its delivery stands, by state
	// (gates.toml's drafts.purposes and drafts.states).
	DraftPurposes map[string]string `json:"draft_purposes"`
	DraftStates   map[string]string `json:"draft_states"`
	// RunStates word a run's turn state and SessionStates its session's, by state (tasks.toml's runs).
	RunStates     map[string]string `json:"run_states"`
	SessionStates map[string]string `json:"session_states"`
	// Version changes whenever anything above does.
	Version string `json:"version,omitempty"`
}

// TaskViewSpec is a view of views.toml: Summary is one line on what it lists, Empty what it shows when it lists
// nothing, Over what it lists (tasks or gates), When and Sort its rule as the profile writes it, Group the fact it
// groups by, Key the key that jumps to it, Badge whether the badge counts it, HideDisabled whether it hides the
// tasks of a disabled plugin, RemoveClean whether it offers to remove its tasks' clean workspaces.
type TaskViewSpec struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	Summary      string `json:"summary,omitempty"`
	Empty        string `json:"empty,omitempty"`
	When         string `json:"when,omitempty"`
	Sort         string `json:"sort,omitempty"`
	Over         string `json:"over,omitempty"`
	Group        string `json:"group,omitempty"`
	Key          string `json:"key,omitempty"`
	Badge        bool   `json:"badge,omitempty"`
	HideDisabled bool   `json:"hide_disabled,omitempty"`
	RemoveClean  bool   `json:"remove_clean,omitempty"`
	// Rail says whether the rail lists the view; a view only the home, a queue or search reads does not.
	Rail bool `json:"rail"`
	// Layout is how the cockpit draws the view: list, board or outbox.
	Layout string `json:"layout,omitempty"`
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
	Send string `json:"send,omitempty"`
	// Pick is, on a verb that takes another task, the tasks a person picks it from: open (the open tasks but
	// this one, as TasksPath lists them with TasksMatch and TasksExcept), merged (TaskView.MergedFrom) or related
	// (TaskView.Related); the pick goes in the argument task.ArgTask. Empty for a verb that takes none.
	Pick string   `json:"pick,omitempty"`
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
	Actions []ActionState `json:"actions,omitempty"`
	// UnitActions are, for each unit the task holds that has a row, the actions its row offers on a task's
	// page (the task surface), run on that unit; detail only.
	UnitActions []UnitActions `json:"unit_actions,omitempty"`
	Attention   *Attention    `json:"attention,omitempty"`
	Created     time.Time     `json:"created"`
	Updated     time.Time     `json:"updated"`

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
	// Facets are the task's values of every facet, by facet id, from its facts, its brief and its units' rows,
	// joined as the profile's facets say; Values[0] is the group and sort key.
	Facets map[string][]string `json:"facets,omitempty"`
	// Reason is why the task's primary unit waits, its row's summary reason; empty when it has no row.
	Reason string `json:"reason,omitempty"`
	// Asks are the items the task holds, detail only.
	Asks  []AskView   `json:"asks,omitempty"`
	Gates []GateView  `json:"gates,omitempty"` // the open ones
	Runs  []RunView   `json:"runs,omitempty"`  // detail only
	Brief *RecordView `json:"brief,omitempty"`
	Wake  *WakeView   `json:"wake,omitempty"`
	// Parent is the parent task's id; Children are its children's, in sibling order.
	Parent   string   `json:"parent,omitempty"`
	Children []string `json:"children,omitempty"`
	// Related are the tasks related to this one, as its property of role related lists them; MergedFrom the
	// tasks merged into it, in merge order, each as its merge record keeps it: the ones split gives back.
	Related    []TaskRef `json:"related,omitempty"`
	MergedFrom []TaskRef `json:"merged_from,omitempty"`
	// Drafts are the ids of the task's pending drafts (held or failed), ascending.
	Drafts []int64 `json:"drafts"`
	// ReplyBroadcast is "on" or "off" when a reply the user writes on the task would reply in a thread that can
	// also post to its conversation, as its publisher would place it, which the reply verb's broadcast argument
	// turns; "" otherwise. Detail only.
	ReplyBroadcast string `json:"reply_broadcast,omitempty"`
}

// TaskRef is another task as a task's page names it: its id, title and state; Merged says it is merged
// into another task and reads as that one. Within are, on an entry of MergedFrom, the tasks merged into it
// before it was merged, shown without a split: that entry is split first.
type TaskRef struct {
	ID     string    `json:"id"`
	Title  string    `json:"title"`
	State  string    `json:"state,omitempty"`
	Merged bool      `json:"merged,omitempty"`
	Within []TaskRef `json:"within,omitempty"`
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
// its kind serves it: a draft gate's is its Draft, a plan gate's its plan item's id. Plan is a plan gate's item while
// it waits.
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
	Plan     *PlanItem       `json:"plan,omitempty"`
	// Units are what a step gate is about: the units its task holds, as a person finds them.
	Units []PlanUnit `json:"units,omitempty"`
}

// IntentView is an intent a gate offers: Summary says what resolving with it does, for the card of a gate whose
// gates.toml sets card_says, Answer that it takes text, as a question's answer.
type IntentView struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Summary string `json:"summary,omitempty"`
	Answer  bool   `json:"answer,omitempty"`
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
	Seq   int64  `json:"seq"`
	Kind  string `json:"kind"`
	Sub   string `json:"sub,omitempty"`
	Actor string `json:"actor,omitempty"`
	// Session is the session an agent acted through, and By how the profile names a change made through one.
	Session string          `json:"session,omitempty"`
	By      string          `json:"by,omitempty"`
	At      time.Time       `json:"at"`
	Unit    string          `json:"unit,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// TimelineNode is one fact of a task's timeline, as GET TimelinePath?as=nodes serves it, newest first: the events
// a profile's [facts.<name>] Rule folded, oldest first, or one event no fact took (Rule ""). Label and Tone are the
// fact's words; End is zero for a single event and for a span still Running.
type TimelineNode struct {
	Rule    string          `json:"rule,omitempty"`
	Label   string          `json:"label"`
	Tone    string          `json:"tone,omitempty"`
	At      time.Time       `json:"at"`
	End     time.Time       `json:"end,omitzero"`
	Running bool            `json:"running,omitempty"`
	Actor   string          `json:"actor,omitempty"`
	Events  []TimelineEvent `json:"events"`
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

// UnitActions are actions a unit's row offers, with the unit and its label.
type UnitActions struct {
	Unit    unit.Unit     `json:"unit"`
	Label   string        `json:"label"`
	Actions []ActionState `json:"actions"`
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
	// Specs is the version of GET /api/views; a client holding another reads them again.
	Specs string `json:"specs,omitempty"`
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
// send "now"}; merge, split, relate, unrelate {task (task.ArgTask): the other task's id}.
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
	// Draft is the held draft a confirm's yes approves, at the rev the question pinned; nil for none.
	Draft *record.DraftRef `json:"draft,omitempty"`
	// Pinned are the drafts the confirm and the options pin, each as its card serves it now, so a person sees
	// what an answer approves; a pinned draft that is gone is absent, and one whose Rev is not its pin's
	// changed since the question pinned it.
	Pinned    []Draft   `json:"pinned,omitempty"`
	Origin    string    `json:"origin"`
	CreatedAt time.Time `json:"created_at"`
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
