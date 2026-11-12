package draft

import (
	"encoding/json"
	"time"
)

// KindSpec is a draft kind as its plugin's manifest declares it, keyed by
// Provider and Name.
type KindSpec struct {
	Provider, Name string
	// Delivery is how the plugin posts a draft of the kind.
	Delivery string
	// Schema and Preview are paths in the plugin's directory: the JSON
	// schema of the kind's payload, and the template of its summary line.
	Schema, Preview string
	// Basis is who states what a draft of the kind was written against:
	// BasisAgent, or BasisNone.
	Basis        string
	TextRequired bool
	// Editable are what a person may change on a draft of the kind: "text",
	// or a JSON pointer into its payload.
	Editable []string
	// Facts are the payload fields an approval rule reads.
	Facts []FactSpec
}

// The bases a KindSpec names.
const (
	BasisNone  = ""
	BasisAgent = "agent"
)

// FactSpec is a payload field read as an approval fact: Name is
// draft.<provider>.<kind>.<field>, Pointer a JSON pointer into the payload
// to a required string enum.
type FactSpec struct {
	Name, Pointer string
}

// Revision is one version of a draft's content, as its reviser left it.
type Revision struct {
	Rev     int64
	Text    string
	Payload json.RawMessage
	Basis   string
	By      Actor
	At      time.Time
}

// ActorKind is who changed a draft.
type ActorKind string

const (
	ActorPerson  ActorKind = "person"
	ActorSession ActorKind = "session"
	ActorRule    ActorKind = "rule"
	ActorWindow  ActorKind = "window"
)

// Actor is who changed a draft: Session is the agent session of an
// ActorSession, "" otherwise.
type Actor struct {
	Kind    ActorKind
	Session string
}
