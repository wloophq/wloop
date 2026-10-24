package slackrows

import (
	"encoding/json"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/items"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
)

// Payload is a Slack row's provider part: what the card reads beyond the
// generic row.
type Payload struct {
	Conversation string `json:"conversation"`
	// Channel is the channel's name without "#"; "" for a DM.
	Channel string `json:"channel,omitempty"`
	DM      bool   `json:"dm,omitempty"`
	// With are the other people of a DM, by name.
	With  []string `json:"with,omitempty"`
	Topic string   `json:"topic,omitempty"`
	// Verdict is the last extraction's; "" before any.
	Verdict         string         `json:"verdict,omitempty"`
	StartedByViewer bool           `json:"started_by_viewer,omitempty"`
	ClosurePosted   bool           `json:"closure_posted,omitempty"`
	Followed        bool           `json:"followed,omitempty"`
	Replies         int            `json:"replies"`
	LastActivity    time.Time      `json:"last_activity,omitzero"`
	LastHuman       *LastHuman     `json:"last_human,omitempty"`
	WaitsOn         []items.Person `json:"waits_on,omitempty"`
	Viewer          string         `json:"viewer,omitempty"`
}

// LastHuman is the newest message a human other than the viewer wrote.
type LastHuman struct {
	Who  items.Person `json:"who"`
	What string       `json:"what"`
	At   time.Time    `json:"at"`
	Link string       `json:"link,omitempty"`
}

// PayloadOf decodes a Slack row's payload; false for another provider or none.
func PayloadOf(r api.Row) (Payload, bool) {
	if r.Unit.Provider != dslack.Provider || len(r.Payload) == 0 {
		return Payload{}, false
	}
	var p Payload
	if err := json.Unmarshal(r.Payload, &p); err != nil {
		return Payload{}, false
	}
	return p, true
}
