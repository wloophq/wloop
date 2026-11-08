package api

import "github.com/wloophq/wloop/internal/domain/unit"

// Omnibox paths: SearchPath takes the query parameter q and answers a Search; OmniboxResolvePath takes an
// OmniboxText and answers an OmniboxResolved; OmniboxAskPath takes an OmniboxText and answers an
// OmniboxAnswer; OmniboxRunPath takes an OmniboxRun and answers an OmniboxRan.
const (
	SearchPath         = "/api/search"
	OmniboxResolvePath = "/api/omnibox/resolve"
	OmniboxAskPath     = "/api/omnibox/ask"
	OmniboxRunPath     = "/api/omnibox/run"
)

// Search is every task matching Query, grouped by whom it waits on.
type Search struct {
	Query  string        `json:"query"`
	Total  int           `json:"total"`
	Groups []SearchGroup `json:"groups"`
}

// SearchGroup is the first results of a group; Count counts all of them.
type SearchGroup struct {
	ID      string         `json:"id"`
	Label   string         `json:"label"`
	Count   int            `json:"count"`
	Results []SearchResult `json:"results"`
}

// SearchResult is a task found, the rail views it appears in and its unit's label.
type SearchResult struct {
	Task      TaskView `json:"task"`
	Views     []string `json:"views"`
	UnitLabel string   `json:"unit_label,omitempty"`
}

// OmniboxText is what the user typed; Follow carries the questions the agent asked and their answers.
type OmniboxText struct {
	Text   string        `json:"text"`
	Follow []OmniboxTurn `json:"follow,omitempty"`
}

// OmniboxTurn is a question the agent asked and the user's answer.
type OmniboxTurn struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// OmniboxResolved is how typed text resolves, by Kind: "unit", "verb" (a known verb's operations), "search",
// or "agent" (the agent would help); Why is the code, snake_case, of why it fell to search or agent, and
// Params the values its words name.
type OmniboxResolved struct {
	Kind    string            `json:"kind"`
	Why     string            `json:"why,omitempty"`
	Params  map[string]string `json:"params,omitempty"`
	Unit    *OmniboxUnit      `json:"unit,omitempty"`
	Preview *OmniboxProposal  `json:"preview,omitempty"`
}

// OmniboxUnit is a resolved unit: its tasks and actions.
type OmniboxUnit struct {
	Unit      unit.Unit    `json:"unit"`
	Label     string       `json:"label"`
	Permalink string       `json:"permalink,omitempty"`
	Tasks     []TaskView   `json:"tasks"`
	Actions   []ActionSpec `json:"actions"`
}

// OmniboxProposal is a set of operations to confirm, and the question the agent asks first, if any.
type OmniboxProposal struct {
	Summary    string             `json:"summary,omitempty"`
	Operations []OmniboxOperation `json:"operations"`
	Question   string             `json:"question,omitempty"`
}

// OmniboxOperation is one operation: Op "action" (Action on Unit), "verb" (Verb on Task), "capture" (a
// task titled Title) or "start" (a task from Brief, started in Repo).
type OmniboxOperation struct {
	Op       string     `json:"op"`
	Unit     *unit.Unit `json:"unit,omitempty"`
	Task     string     `json:"task,omitempty"`
	Action   string     `json:"action,omitempty"`
	Verb     string     `json:"verb,omitempty"`
	Title    string     `json:"title,omitempty"`
	Brief    string     `json:"brief,omitempty"`
	Repo     string     `json:"repo,omitempty"`
	Guidance string     `json:"guidance,omitempty"`
	Reason   string     `json:"reason,omitempty"`
}

// OmniboxRun is the operations the user confirmed.
type OmniboxRun struct {
	Operations []OmniboxOperation `json:"operations"`
}

// OmniboxRan is each operation's result, in order.
type OmniboxRan struct {
	Results []OmniboxResult `json:"results"`
}

// OmniboxResult is an operation's outcome as its handler answered it: Error is the code, snake_case, of why
// it failed and Params the values its words name, Refusal the handler's refusal code; Task is the task it
// created, if any.
type OmniboxResult struct {
	OK      bool              `json:"ok"`
	Status  int               `json:"status"`
	Error   string            `json:"error,omitempty"`
	Params  map[string]string `json:"params,omitempty"`
	Refusal string            `json:"refusal,omitempty"`
	Output  []string          `json:"output,omitempty"`
	Task    string            `json:"task,omitempty"`
}

// OmniboxAnswer is the read-only agent's answer, a list or a proposal, with what it proposed that was
// dropped and the call's cost.
type OmniboxAnswer struct {
	Answer   *OmniboxList     `json:"answer,omitempty"`
	Proposal *OmniboxProposal `json:"proposal,omitempty"`
	Dropped  []OmniboxDropped `json:"dropped,omitempty"`
	CostUSD  *float64         `json:"cost_usd,omitempty"`
}

// OmniboxList is an answer's text and the tasks it lists.
type OmniboxList struct {
	Text  string     `json:"text"`
	Tasks []TaskView `json:"tasks"`
}

// OmniboxDropped is a proposed operation refused: Why is the code, snake_case, of why, and Params the values
// its words name.
type OmniboxDropped struct {
	Operation OmniboxOperation  `json:"operation"`
	Why       string            `json:"why"`
	Params    map[string]string `json:"params,omitempty"`
}
