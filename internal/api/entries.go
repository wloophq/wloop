package api

import (
	"net/url"
	"time"

	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// The entries' routes: GTDPath serves the GTDCatalog, EntriesPath lists a view (query parameters view, area,
// context as "a,b" and since, a Since's String) and takes a capture, EntryVerb runs a verb, CountersPath
// serves the counters of the query parameter area (narrowed to the entries holding each CountersTerm), DigestPath the daily digest of the query parameter day
// (YYYY-MM-DD).
const (
	GTDPath      = "/api/gtd"
	EntriesPath  = "/api/entries"
	EntryDetail  = EntriesPath + "/{id}"
	EntryVerb    = EntryDetail + "/{verb}"
	CountersPath = "/api/counters"
	DigestPath   = "/api/digest"
)

// EntryPath is the path of entry id, followed by the segments of rest.
func EntryPath(id string, rest ...string) string {
	p := EntriesPath + "/" + url.PathEscape(id)
	for _, r := range rest {
		p += "/" + url.PathEscape(r)
	}
	return p
}

type GTDCatalog struct {
	Views    []GTDView `json:"views"` // core views in spec 9.1 order, review only once registered
	Areas    []string  `json:"areas"`
	Contexts []string  `json:"contexts"`
	Verbs    []GTDVerb `json:"verbs"` // key from internal/keys
}

type GTDView struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type GTDVerb struct {
	Verb    string `json:"verb"`
	Label   string `json:"label"`
	Summary string `json:"summary,omitempty"`
	Key     string `json:"key"`
}

type EntryView struct {
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
	Attention        *Attention   `json:"attention,omitempty"`
	Message          *Message     `json:"message,omitempty"`
	Draft            *Draft       `json:"draft,omitempty"`
	Created          time.Time    `json:"created"`
	Updated          time.Time    `json:"updated"`

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

// CardView is a unit's stored card on an entry's detail.
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

type EntriesPage struct {
	Epoch    string      `json:"epoch"`
	Rev      uint64      `json:"rev"`
	Full     bool        `json:"full"`
	Entries  []EntryView `json:"entries"`
	Removed  []string    `json:"removed"`
	Counters Counters    `json:"counters"`
}

type CaptureRequest struct {
	Title string     `json:"title"`
	Notes string     `json:"notes,omitempty"`
	Unit  *unit.Unit `json:"unit,omitempty"`
}

// EntryRequest args: waiting {on, resurface}; schedule {until (RFC3339 or YYYY-MM-DD), on_activity "true"};
// project {id | title (creates one)}; context {set "a,b"}; area {set}; answer: as ActionRequest fields;
// delegate {guidance, mode "headless" | "session"}; reply {text | reaction, kind "reply" | "react",
// send "now"}.
type EntryRequest struct {
	Args   map[string]string `json:"args,omitempty"`
	Source string            `json:"source"`
}

type EntryResult struct {
	Entry EntryView `json:"entry"`
	Lines []string  `json:"lines,omitempty"`
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
	EntryID   string          `json:"entry_id"`
	Unit      unit.Unit       `json:"unit"`
	Kind      string          `json:"kind"`
	Text      string          `json:"text"`
	Options   []record.Option `json:"options,omitempty"`
	Recommend string          `json:"recommend,omitempty"`
	Confirm   bool            `json:"confirm,omitempty"`
	Origin    string          `json:"origin"`
	State     string          `json:"state"`
	CreatedAt time.Time       `json:"created_at"`
}

// UnitLabel is the i-th unit as a person reads it.
func (e EntryView) UnitLabel(i int) string {
	if i < len(e.UnitLabels) && e.UnitLabels[i] != "" {
		return e.UnitLabels[i]
	}
	return e.Units[i].Label()
}
