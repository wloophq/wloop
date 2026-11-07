package api

// HomePath serves the cockpit's home, as Home.
const HomePath = "/api/home"

// QueuePath serves the decision queue of a decision kind, as Queue; the query parameter q narrows it by a
// lens query, as CountersPath's does.
const QueuePath = "/api/queues/{kind}"

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

// HomeCounter counts the tasks of a decision kind (Kind) or of a view; View is the view it opens.
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

// HomeSection is the first rows of View; Total counts all of them, Time names the time each row shows
// ("owed" or "moved").
type HomeSection struct {
	ID    string     `json:"id"`
	View  string     `json:"view"`
	Label string     `json:"label"`
	Time  string     `json:"time"`
	Rows  []TaskView `json:"rows"`
	Total int        `json:"total"`
}

// HomeCapture is the capture field's hint and key.
type HomeCapture struct {
	Hint string `json:"hint"`
	Key  string `json:"key,omitempty"`
}

// Queue is a decision kind's items, oldest decision first.
type Queue struct {
	Kind  string      `json:"kind"`
	Title string      `json:"title"`
	Empty string      `json:"empty"`
	Total int         `json:"total"`
	Items []QueueItem `json:"items"`
}

// QueueItem is one decision: a task's open gate, or a situation of one of its units.
type QueueItem struct {
	Task      TaskView        `json:"task"`
	Gate      *GateView       `json:"gate,omitempty"`
	Situation *QueueSituation `json:"situation,omitempty"`
	Primary   QueueAction     `json:"primary"`
}

// QueueSituation is the situation of a provider's unit that makes the decision.
type QueueSituation struct {
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
	Label    string `json:"label"`
}

// QueueAction is an item's primary action: Kind "gate" (ID a gate intent), "action" (ID a catalog
// action) or "open" (open the unit at its source).
type QueueAction struct {
	Kind  string `json:"kind"`
	ID    string `json:"id,omitempty"`
	Label string `json:"label"`
}
