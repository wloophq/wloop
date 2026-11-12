package api

import (
	"encoding/json"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// Drafts lists drafts (query parameters state, a comma list, id, a comma list of draft ids, and task; with
// none, the Outbox's: those whose gate is open, and those whose gate approved them and whose delivery failed)
// and, posted a DraftCreate, holds a draft the user wrote. A draft's decision is its gate's, resolved through
// GatePath; a draft gate's Subject is its Draft.
const Drafts = "/api/drafts"

// DraftCloseSilently takes a DraftCloseSilentlyRequest: it closes the held draft's conversation as the
// draft would, applying its effects without posting it, and answers the Draft.
const DraftCloseSilently = Drafts + "/{id}/close-silently"

// DraftCloseSilentlyRequest names the draft's version the person saw.
type DraftCloseSilentlyRequest struct {
	Rev int64 `json:"rev"`
}

type DraftsResponse struct {
	Drafts []Draft `json:"drafts"`
}

type Draft struct {
	ID    int64    `json:"id"`
	Unit  string   `json:"unit"` // "<provider>/<key>"
	Items []string `json:"items"`
	Task  string   `json:"task,omitempty"`
	Kind  string   `json:"kind"`
	// Purpose is what the draft is for, Delivery how it is posted (message,
	// reaction or comment) and Effects what posting it applies.
	Purpose  string   `json:"purpose"`
	Delivery string   `json:"delivery"`
	Effects  []string `json:"effects"`
	Target   string   `json:"target"`
	// Broadcast is "on" or "off" for a thread reply also posted, or not, to its conversation, which edit's
	// broadcast argument turns; "" when the draft replies in no thread.
	Broadcast string `json:"broadcast"`
	// Where names the conversation or person the draft posts to; "" when its publisher could not tell.
	Where string `json:"where"`
	Text  string `json:"text"`
	// Why is why the draft was proposed, in the interface language when it was made; Language is the
	// language Text is written in.
	Why      string `json:"why"`
	Language string `json:"language"`
	Reaction string `json:"reaction"`
	Original string `json:"original"`
	// Replaced is the text a sent edit replaced.
	Replaced string      `json:"replaced,omitempty"`
	Edited   bool        `json:"edited"`
	Origin   DraftOrigin `json:"origin"`
	// State is where its delivery stands: held while its gate is open, queued, sending, sent, failed or dropped.
	State  string `json:"state"`
	Error  string `json:"error"`
	Reason string `json:"reason"`
	// Quoted says, in the profile's words (gates.toml's drafts.quoted), which conversation a draft posted in
	// the direct conversation of the person it is to quotes; "" for any other draft.
	Quoted string `json:"quoted,omitempty"`
	// ReasonText is Reason in the profile's words (gates.toml's drafts.reasons) for a dropped draft; "" when
	// it words none.
	ReasonText string `json:"reason_text,omitempty"`
	Permalink  string `json:"permalink"`
	// Gate is the draft's gate, 0 for none; Deadline is when its window resolves it, nil for none; Intents are
	// its open gate's intents by the status each resolves it to (approved, rejected), the first of each.
	Gate     int64             `json:"gate,omitempty"`
	Deadline *time.Time        `json:"deadline"`
	Intents  map[string]string `json:"intents,omitempty"`
	// Actions are the draft actions of the catalog as they stand on the draft: its delivery's, edit and retry.
	Actions []ActionState `json:"actions"`
	// Rev is the stored draft's version; approve and retry send the one they showed.
	Rev       int64     `json:"rev"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// Label, Title and Link present the unit as its row does; with no row, Label is the unit's own.
	Label string `json:"label,omitempty"`
	Title string `json:"title,omitempty"`
	Link  string `json:"link,omitempty"`
	// Names are the display names of the people Text mentions, by mention key ("slack:U0123"), as the
	// unit's items know them; the draft routes fill them and the item details, the Outbox tasks do not.
	Names map[string]string `json:"names,omitempty"`
	// ItemDetails are the items the draft is about, with their summaries.
	ItemDetails []ItemView `json:"item_details,omitempty"`
	// Payload is what the draft posts besides Text, verbatim, and Fields every value of it as its card shows
	// it, in the payload's order; both empty for a draft of a kind with no payload.
	Payload json.RawMessage `json:"payload,omitempty"`
	Fields  []DraftField    `json:"fields,omitempty"`
	// Basis is what the draft was written against, such as a pull request's head; Stale says its unit moved
	// past it, so approving it is refused.
	Basis string `json:"basis,omitempty"`
	Stale bool   `json:"stale,omitempty"`
	// Hold keeps every approval rule from releasing the draft.
	Hold bool `json:"hold,omitempty"`
	// ByRule is the approval rule of the user's that sent the draft, "" when none did.
	ByRule string `json:"by_rule,omitempty"`
	// Notes are the profile's sentences on how the draft stands (gates.toml's drafts.stale and drafts.hold,
	// notices.toml's draft_by_rule), in that order.
	Notes []string `json:"notes,omitempty"`
}

// DraftField is a value of a draft's payload as its card shows it: Label is the plugin's word for its key
// (ui.toml's drafts.<kind>.fields), else the key, or the item's position in a list. A scalar has Value, as
// posted, and Shown, its word when the plugin words it; an object or a list has Fields. Editable says a
// person may change it, to one of Options, its schema's enum.
type DraftField struct {
	Pointer  string        `json:"pointer"`
	Label    string        `json:"label"`
	Value    string        `json:"value,omitempty"`
	Shown    string        `json:"shown,omitempty"`
	Fields   []DraftField  `json:"fields,omitempty"`
	Editable bool          `json:"editable,omitempty"`
	Options  []DraftOption `json:"options,omitempty"`
}

// DraftOption is a value an editable field takes, with its word.
type DraftOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// OutboxPage is the Outbox's drafts in groups, the largest first, then by title.
type OutboxPage struct {
	Groups []OutboxGroup `json:"groups"`
}

// OutboxGroup is the drafts one workflow proposed (Workflow its name), else those of one Purpose no workflow
// proposed, else those written by hand (both ""). Title and About are the profile's words for it, in the
// served locale.
type OutboxGroup struct {
	Workflow string        `json:"workflow,omitempty"`
	Purpose  string        `json:"purpose,omitempty"`
	Title    string        `json:"title"`
	About    string        `json:"about,omitempty"`
	Drafts   []OutboxDraft `json:"drafts"`
}

// OutboxDraft is a draft with what a person reads to decide on it.
type OutboxDraft struct {
	Draft
	Context DraftContext `json:"context"`
}

// DraftContext is what a draft answers: the asks of its task, with their quotes, and the items it is about,
// with where each stands.
type DraftContext struct {
	Asks  []AskView    `json:"asks"`
	Items []OutboxItem `json:"items"`
}

// OutboxItem is an item a draft is about.
type OutboxItem struct {
	ID      string `json:"id"`
	Summary string `json:"summary"`
	State   string `json:"state"`
}

type DraftOrigin struct {
	Kind     string `json:"kind"`
	TurnSeq  int64  `json:"turn_seq"`
	Intent   string `json:"intent"`
	Workflow string `json:"workflow"`
	Rule     string `json:"rule"`
	// Unit is the label of the proposing turn's unit when it is not the
	// draft's, "" when it is.
	Unit string `json:"unit,omitempty"`
}

// The kinds of draft the user writes: a reply in the unit's conversation (a comment on a pull request) and
// a reaction to it.
const (
	DraftReply = "reply"
	DraftReact = "react"
)

// DraftCreate is a draft the user writes on Unit, for Task and its Items when set: Kind DraftReply with
// Text, or DraftReact with Reaction. It waits in the Outbox for approval, unless Send approves it at once
// to go out through the same publisher. Source is cockpit, notification, tui or terminal.
type DraftCreate struct {
	Unit     unit.Unit `json:"unit"`
	Task     string    `json:"task,omitempty"`
	Items    []string  `json:"items,omitempty"`
	Kind     string    `json:"kind,omitempty"`
	Text     string    `json:"text,omitempty"`
	Reaction string    `json:"reaction,omitempty"`
	// Target is the message a reply answers or a reaction goes on, when it is
	// not the unit's own.
	Target string `json:"target,omitempty"`
	// Broadcast asks, "on" or "off", that a reply in a thread also post to its conversation, or not; "" leaves it
	// to the publisher, which also ignores it on a reply in no thread.
	Broadcast string `json:"broadcast,omitempty"`
	Send      bool   `json:"send,omitempty"`
	Source    string `json:"source"`
}

// DraftOf is the draft of e's first draft gate, its subject; false when e has none.
func (e TaskView) DraftOf() (Draft, bool) {
	for _, g := range e.Gates {
		if g.Kind != GateKindDraft || len(g.Subject) == 0 {
			continue
		}
		var d Draft
		if json.Unmarshal(g.Subject, &d) == nil {
			return d, true
		}
	}
	return Draft{}, false
}

// DraftGate is d as the gate it waits on serves it: its subject d, its delivery d's state.
func DraftGate(d Draft) GateView {
	v := GateView{ID: d.Gate, Kind: GateKindDraft, Delivery: d.State, Intents: []IntentView{}}
	v.Subject, _ = json.Marshal(d)
	return v
}

// GateKindDraft is the Kind of a draft's gate.
const GateKindDraft = "draft"
