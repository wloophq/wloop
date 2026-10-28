package api

import (
	"net/url"
	"time"

	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// The tasks' routes: ViewsPath serves the ViewsCatalog, TasksPath lists a view (query parameters view, area,
// context as "a,b" and since, a Since's String) and takes a capture, TaskVerb runs a verb, CountersPath
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
}

type TaskViewSpec struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type VerbSpec struct {
	Verb    string `json:"verb"`
	Label   string `json:"label"`
	Summary string `json:"summary,omitempty"`
	Input   string `json:"input,omitempty"` // the question a verb that asks for a value puts first
	Key     string `json:"key"`
}

type TaskView struct {
	ID               string       `json:"id"`
	Kind             string       `json:"kind"`
	State            string       `json:"state"`
	Title            string       `json:"title"`
	Notes            string       `json:"notes,omitempty"`
	Focus            bool         `json:"focus"`
	FocusAuto        string       `json:"focus_auto,omitempty"`
	Contexts         []string     `json:"contexts"`
	Area             string       `json:"area,omitempty"`
	Project          string       `json:"project,omitempty"`
	ProjectTitle     string       `json:"project_title,omitempty"`
	Deadline         time.Time    `json:"deadline,omitzero"`
	SnoozeUntil      time.Time    `json:"snooze_until,omitzero"`
	SnoozeOnActivity bool         `json:"snooze_on_activity,omitempty"`
	Muted            bool         `json:"muted,omitempty"`
	Waiting          *WaitingView `json:"waiting,omitempty"`
	Changed          bool         `json:"changed,omitempty"`
	Units            []unit.Unit  `json:"units"`
	UnitLabels       []string     `json:"unit_labels,omitempty"` // Units as a person reads them, in order
	Items            []ItemView   `json:"items"`
	Cards            []CardView   `json:"cards,omitempty"` // detail only
	Verbs            []VerbState  `json:"verbs"`
	// Actions are the intents that serve the task's own unit, such as start.
	Actions   []ActionState `json:"actions,omitempty"`
	Attention *Attention    `json:"attention,omitempty"`
	Message   *Message      `json:"message,omitempty"`
	Draft     *Draft        `json:"draft,omitempty"`
	Created   time.Time     `json:"created"`
	Updated   time.Time     `json:"updated"`

	// A project's: its next action's id, its open actions, and its review.
	Sequential      bool      `json:"sequential,omitempty"`
	NextAction      string    `json:"next_action,omitempty"`
	NextActionTitle string    `json:"next_action_title,omitempty"`
	OpenActions     int       `json:"open_actions,omitempty"`
	ReviewEvery     string    `json:"review_every,omitempty"` // a Go duration
	ReviewedAt      time.Time `json:"reviewed_at,omitzero"`
	ReviewDue       bool      `json:"review_due,omitempty"`
}

type WaitingView struct {
	On        []WaitingPerson `json:"on"`
	Since     time.Time       `json:"since"`
	LastNudge time.Time       `json:"last_nudge,omitzero"`
	Resurface time.Time       `json:"resurface,omitzero"`
}

type WaitingPerson struct {
	Name string `json:"name"`
	Link string `json:"link,omitempty"`
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

type TasksPage struct {
	Epoch    string     `json:"epoch"`
	Rev      uint64     `json:"rev"`
	Full     bool       `json:"full"`
	Tasks    []TaskView `json:"tasks"`
	Removed  []string   `json:"removed"`
	Counters Counters   `json:"counters"`
}

type CaptureRequest struct {
	Title string     `json:"title"`
	Notes string     `json:"notes,omitempty"`
	Unit  *unit.Unit `json:"unit,omitempty"`
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

type Message struct {
	ID        int64           `json:"id"`
	TaskID    string          `json:"task_id"`
	Unit      unit.Unit       `json:"unit"`
	Kind      string          `json:"kind"`
	Text      string          `json:"text"`
	Options   []record.Option `json:"options,omitempty"`
	Recommend string          `json:"recommend,omitempty"`
	Confirm   bool            `json:"confirm,omitempty"`
	Origin    string          `json:"origin"`
	State     string          `json:"state"`
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
