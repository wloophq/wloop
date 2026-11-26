package api

import "time"

// HomePath serves the cockpit's home, as Home.
const HomePath = "/api/home"

// QueuePath serves the decision queue of a decision kind, as Queue; the query parameter q narrows it by a
// lens query, as CountersPath's does.
const QueuePath = "/api/queues/{kind}"

// QueueAllPath serves the queue of every decision kind, as Queue with no Kind and with View the view drawn as
// the queue: the kinds in the order decisions.toml gives, each kind's items oldest decision first, and Kinds every
// kind in that order. Its query parameter q is QueuePath's; each kind parameter keeps the items of that kind, every
// kind when none.
const QueueAllPath = "/api/queue"

// Home is the briefing, the home's sections and the capture field.
type Home struct {
	Briefing Briefing      `json:"briefing"`
	Sections []HomeSection `json:"sections"`
	Capture  HomeCapture   `json:"capture"`
}

// Briefing is the home's sentences, each a run of text and counter parts; Empty is what it says when
// every counter is zero.
type Briefing struct {
	Template  string           `json:"template"`
	Sentences [][]BriefingPart `json:"sentences"`
	Counters  []HomeCounter    `json:"counters"`
	Empty     string           `json:"empty,omitempty"`
}

// BriefingPart is text, or with Counter set the counter of that index in Briefing.Counters.
type BriefingPart struct {
	Text    string `json:"text,omitempty"`
	Counter *int   `json:"counter,omitempty"`
}

// HomeCounter counts the decisions of a kind (Kind), those its queue lists, or the tasks of a view; View is the view it opens.
type HomeCounter struct {
	ID    string       `json:"id"`
	Kind  string       `json:"kind,omitempty"`
	View  string       `json:"view"`
	Count int          `json:"count"`
	Label string       `json:"label"`
	Nouns []PluginNoun `json:"nouns,omitempty"`
}

// PluginNoun is a plugin's share of a counter, worded with the plugin's noun.
type PluginNoun struct {
	Plugin string `json:"plugin"`
	Count  int    `json:"count"`
	Noun   string `json:"noun"`
}

// HomeSection is the first rows of View, or, for a view drawn as the queue, the first Items of the queue of
// every decision kind; Total counts all of them, Time names the time each row shows ("owed" or "moved").
type HomeSection struct {
	ID    string      `json:"id"`
	View  string      `json:"view"`
	Label string      `json:"label"`
	Time  string      `json:"time"`
	Rows  []TaskView  `json:"rows"`
	Items []QueueItem `json:"items,omitempty"`
	// Empty is what a queue section says when it lists nothing.
	Empty string `json:"empty,omitempty"`
	Total int    `json:"total"`
}

// HomeCapture is the capture field's hint and key.
type HomeCapture struct {
	Hint string `json:"hint"`
	Key  string `json:"key,omitempty"`
}

// Queue is a decision kind's items, oldest decision first; View is the view whose rows draw its items.
type Queue struct {
	Kind  string      `json:"kind"`
	View  string      `json:"view"`
	Title string      `json:"title"`
	Empty string      `json:"empty"`
	Total int         `json:"total"`
	Items []QueueItem `json:"items"`
	// Kinds are the decision kinds the queue of every kind may list, in its order.
	Kinds []QueueKind `json:"kinds,omitempty"`
}

// QueueKind is a decision kind and the title of its queue.
type QueueKind struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// QueueItem is one decision of kind Kind, which Word words: a task's open gate, a failed delivery's approved
// draft gate, or a situation of one of its units.
type QueueItem struct {
	Kind string `json:"kind"`
	// Word is what one such decision is, as the home's counters word it (a plugin's noun for its units); Tone is
	// the design token decisions.toml draws the kind in; Since is when the decision began.
	Word      string          `json:"word,omitempty"`
	Tone      string          `json:"tone,omitempty"`
	Since     time.Time       `json:"since,omitzero"`
	Task      TaskView        `json:"task"`
	Gate      *GateView       `json:"gate,omitempty"`
	Situation *QueueSituation `json:"situation,omitempty"`
	Primary   QueueAction     `json:"primary"`
	// Options are the options of a question gate whose options pin held drafts, the drafts it stands for:
	// those drafts' own gates make no item. Picking an option answers the question and approves its draft.
	Options []QueueOption `json:"options,omitempty"`
}

// QueueOption is an option of a question an item stands for; Draft is the gate of the held draft picking
// it approves, nil for an option that pins none; Primary is what the button that picks it says, the profile's
// QueueOptionPrimary filled with the draft's verdict, else the option's label.
type QueueOption struct {
	ID          string    `json:"id"`
	Label       string    `json:"label"`
	Recommended bool      `json:"recommended,omitempty"`
	Draft       *GateView `json:"draft,omitempty"`
	Primary     string    `json:"primary"`
}

// QueueOptionPrimary is the strings.toml key of an option's button, {option} its word.
const QueueOptionPrimary = "Post {option}"

// QueueSituation is the situation of a provider's unit that makes the decision; Unit is that unit's string
// form, which tells apart the items of one task whose units are in the same situation.
type QueueSituation struct {
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
	Label    string `json:"label"`
	Unit     string `json:"unit"`
}

// QueueAction is an item's primary action: Kind "gate" (ID a gate intent), "action" (ID a catalog
// action), "open" (open the unit at its source) or "verb" (run the task verb ID on task Task with Args, as
// POST TaskPath(Task, ID) does: a failed delivery's retry on its draft's task, its gate approved already).
type QueueAction struct {
	Kind  string            `json:"kind"`
	ID    string            `json:"id,omitempty"`
	Task  string            `json:"task,omitempty"`
	Args  map[string]string `json:"args,omitempty"`
	Label string            `json:"label"`
}
