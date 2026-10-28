package api

import (
	"net/url"
	"strconv"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// The Outbox's routes: Drafts lists drafts (query parameters state, a comma list, and entry) and, posted a
// DraftCreate, holds a draft the user wrote; DraftDetail reads one and DraftActionPath runs one of
// DraftActions on it with a DraftAction body.
const (
	Drafts          = "/api/drafts"
	DraftDetail     = Drafts + "/{id}"
	DraftActionPath = DraftDetail + "/{action}"
)

// DraftActions are the actions DraftActionPath takes.
var DraftActions = []string{"approve", "edit", "discard", "retry"}

// DraftPath is the path of draft id, followed by the segments of rest.
func DraftPath(id int64, rest ...string) string {
	p := Drafts + "/" + strconv.FormatInt(id, 10)
	for _, r := range rest {
		p += "/" + url.PathEscape(r)
	}
	return p
}

type DraftsResponse struct {
	Drafts []Draft `json:"drafts"`
}

type Draft struct {
	ID    int64    `json:"id"`
	Unit  string   `json:"unit"` // "<provider>/<key>"
	Items []string `json:"items"`
	Task  string   `json:"entry,omitempty"`
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
	Replaced  string        `json:"replaced,omitempty"`
	Edited    bool          `json:"edited"`
	Origin    DraftOrigin   `json:"origin"`
	Approval  DraftApproval `json:"approval"`
	Deadline  *time.Time    `json:"deadline"`
	State     string        `json:"state"`
	Error     string        `json:"error"`
	Reason    string        `json:"reason"`
	Permalink string        `json:"permalink"`
	Would     DraftWould    `json:"would"`
	// Actions are the draft actions of the catalog as they stand on the draft.
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
	// unit's items know them; the draft routes fill them and the item details, the Outbox entries do not.
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

type DraftApproval struct {
	Mode   string `json:"mode"`
	Window string `json:"window"` // a duration as duration.Format writes it ("10m", "2h", "1d"), "" none
}

type DraftWould struct {
	Policy  string `json:"policy"`
	Reason  string `json:"reason"`
	Outcome string `json:"outcome"`
}

// The kinds of draft the user writes: a reply in the unit's conversation (a comment on a pull request) and
// a reaction to it.
const (
	DraftReply = "reply"
	DraftReact = "react"
)

// DraftCreate is a draft the user writes on Unit, for Entry and its Items when set: Kind DraftReply with
// Text, or DraftReact with Reaction. It waits in the Outbox for approval, unless Send approves it at once
// to go out through the same publisher. Source is cockpit, notification, tui or terminal.
type DraftCreate struct {
	Unit     unit.Unit `json:"unit"`
	Task     string    `json:"entry,omitempty"`
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

type DraftAction struct {
	Text   *string `json:"text,omitempty"`
	Reason string  `json:"reason,omitempty"`
	// Confirm lets retry post a draft that may have been posted already.
	Confirm bool `json:"confirm,omitempty"`
	// Rev is the draft's version the caller showed (a new draft's is 0); a draft changed since answers 409.
	// Approve and retry take it; approve posts Text when given, else the text at Rev.
	Rev *int64 `json:"rev,omitempty"`
}
