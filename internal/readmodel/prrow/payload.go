package prrow

import (
	"encoding/json"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/situation"
)

// Payload is the GitHub row's payload; JSON keys as today's Row, plus viewer, unmuted_kind,
// unmuted_waiting_on, last_human and mute.
type Payload struct {
	Author           string            `json:"author"`
	Repo             string            `json:"repo"`
	Head             string            `json:"head"`
	Approved         bool              `json:"approved"`
	Reasons          []string          `json:"reasons"`
	Workspace        api.Workspace     `json:"workspace"`
	Override         api.Override      `json:"override,omitzero"`
	Brief            *api.BriefSummary `json:"brief,omitempty"`
	Intake           *api.Intake       `json:"intake,omitempty"`
	Facts            *api.RowFacts     `json:"facts,omitempty"`
	Viewer           string            `json:"viewer"`                       // the deriver's viewer login
	UnmutedKind      string            `json:"unmuted_kind,omitempty"`       // the kind the row would have without its mute ("" when not muted)
	UnmutedWaitingOn string            `json:"unmuted_waiting_on,omitempty"` // the waiting-on the row would have without its mute
	UnmutedReason    string            `json:"unmuted_reason,omitempty"`     // the reason the row would have without its mute
	LastHuman        *LastHuman        `json:"last_human,omitempty"`         // newest human event on the PR not by the viewer; nil none
	Mute             *Mute             `json:"mute,omitempty"`               // the mute its task's wake holds; nil when not muted

	briefRetryAt time.Time
}

type LastHuman struct {
	Who  string    `json:"who"`
	What string    `json:"what"` // the event kind: opened, push, review, comment
	At   time.Time `json:"at"`
	Link string    `json:"link"`
}

type Mute struct {
	Until            time.Time `json:"until,omitzero"`
	UntilStateChange bool      `json:"until_state_change,omitempty"`
}

// PayloadOf is false for another provider or no payload.
func PayloadOf(r api.Row) (Payload, bool) {
	if r.Unit.Provider != unit.GitHub || len(r.Payload) == 0 {
		return Payload{}, false
	}
	var p Payload
	if err := json.Unmarshal(r.Payload, &p); err != nil {
		return Payload{}, false
	}
	return p, true
}

// NeedsMe is the exact needs-me predicate of Deriver.views, evaluated on UnmutedKind and
// UnmutedWaitingOn when the row is muted; views adds needs-me only when it holds and the row is not muted.
func NeedsMe(r api.Row) bool {
	p, ok := PayloadOf(r)
	if !ok {
		return false
	}
	kind, waitingOn := situation.Kind(r.Kind), r.Summary.WaitsOn
	if kind == situation.KindMuted {
		kind, waitingOn = situation.Kind(p.UnmutedKind), p.UnmutedWaitingOn
	}
	return needsMe(kind, waitingOn, p)
}

// InScope: the row has reasons, a review workspace, or is adopted or tracked.
func InScope(r api.Row) bool {
	p, ok := PayloadOf(r)
	return ok && (len(p.Reasons) > 0 || p.Workspace.IsReview || p.Override.Adopted || p.Override.Tracked)
}
