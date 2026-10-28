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
	// Where names the conversation or person the draft posts to; "" when its publisher could not tell.
	Where    string `json:"where"`
	Text     string `json:"text"`
	Reaction string `json:"reaction"`
	Original string `json:"original"`
	// Replaced is the text a sent edit replaced.
	Replaced string      `json:"replaced,omitempty"`
	Edited   bool        `json:"edited"`
	Origin   DraftOrigin `json:"origin"`
	// State is where its delivery stands: held while its gate is open, queued, sending, sent, failed or dropped.
	State     string `json:"state"`
	Error     string `json:"error"`
	Reason    string `json:"reason"`
	Permalink string `json:"permalink"`
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
	Send   bool   `json:"send,omitempty"`
	Source string `json:"source"`
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
