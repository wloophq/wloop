// Package items holds the cross-source items ledger's types.
package items

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wloophq/wloop/plugins/slack/link"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

type Kind string // "request", "question", "decision", "todo"

const (
	Request  Kind = "request"
	Question Kind = "question"
	Decision Kind = "decision"
	Todo     Kind = "todo"
)

type State string // "open", "resolved"

const (
	Open     State = "open"
	Resolved State = "resolved"
)

type Resolution string // "", "done", "answered", "fixed", "delegated", "declined", "dismissed", "superseded", "moot"

const (
	Done       Resolution = "done"
	Answered   Resolution = "answered"
	Fixed      Resolution = "fixed"
	Delegated  Resolution = "delegated"
	Declined   Resolution = "declined"
	Dismissed  Resolution = "dismissed"
	Superseded Resolution = "superseded"
	Moot       Resolution = "moot"
)

type Basis string // "explicit_mention", "dm_addressee", "reply_context", "self_assigned", "reassigned", "group_ask", "none"

const (
	ExplicitMention Basis = "explicit_mention"
	DMAddressee     Basis = "dm_addressee"
	ReplyContext    Basis = "reply_context"
	SelfAssigned    Basis = "self_assigned"
	Reassigned      Basis = "reassigned"
	GroupAsk        Basis = "group_ask"
	NoBasis         Basis = "none"
)

type EvidenceKind string // "message", "reaction", "external_claim", "unit_event", "sent_draft"

const (
	EvidenceMessage       EvidenceKind = "message"
	EvidenceReaction      EvidenceKind = "reaction"
	EvidenceExternalClaim EvidenceKind = "external_claim"
	EvidenceUnitEvent     EvidenceKind = "unit_event"
	EvidenceSentDraft     EvidenceKind = "sent_draft"
)

type Confidence string // "high", "low"

const (
	High Confidence = "high"
	Low  Confidence = "low"
)

type Provenance string // "extracted", "derived", "user", "turn", "effects" (the outbox, recording what it sent), "workflow"

const (
	Extracted Provenance = "extracted"
	Derived   Provenance = "derived"
	User      Provenance = "user"
	Turn      Provenance = "turn"
	Effects   Provenance = "effects"
	Workflow  Provenance = "workflow"
)

type Initial string // "", "inbox", "next": the plugin's proposed first task state; "" is inbox

const (
	InitialInbox Initial = "inbox"
	InitialNext  Initial = "next"
)

// Limits of an item's text and lists.
const (
	MaxSummary = 300
	MaxContext = 4000
	MaxTrail   = 20
)

type Person struct {
	// Key is "<provider>:<id>" ("github:octocat", "slack:U000AAA"), "group:<name>" for a group
	// or role, or "system:<brand.Name>" for the loop itself (the requester of loop proposals).
	Key     string   `json:"key"`
	Name    string   `json:"name"`
	Aliases []string `json:"aliases,omitempty"` // other keys of the same human
}

// Identity is the user's keys over the enabled plugins: each Identity's Key and Aliases.
type Identity struct {
	Keys []string
}

// Is says whether p's Key or an alias is in id.Keys.
func (p Person) Is(id Identity) bool {
	for _, k := range append([]string{p.Key}, p.Aliases...) {
		if k != "" && slices.Contains(id.Keys, k) {
			return true
		}
	}
	return false
}

type Option struct {
	Label    string   `json:"label"`
	ArguedBy []Person `json:"argued_by,omitempty"`
}

type Evidence struct {
	Kind   EvidenceKind `json:"kind"`
	Anchor unit.Anchor  `json:"anchor"`
}

type Item struct {
	ID         string      `json:"id"` // stable per unit: derived ids from the plugin's rule ("gh:review"), turn creates "t<hash8>.<i>", others "i<N>"
	Unit       unit.Unit   `json:"unit"`
	Kind       Kind        `json:"kind"`
	Summary    string      `json:"summary"` // <= 300
	Context    string      `json:"context"` // <= 4000
	Options    []Option    `json:"options,omitempty"`
	Deadline   string      `json:"deadline,omitempty"`
	URLs       []string    `json:"urls,omitempty"`
	Contexts   []string    `json:"contexts,omitempty"` // proposed contexts ("@decide", "@repo-api"); tasks copy them, the user edits there
	Initial    Initial     `json:"initial,omitempty"`  // proposed first task state, honored for a new task
	Origin     unit.Anchor `json:"origin"`
	Requester  Person      `json:"requester"`
	Assignees  []Person    `json:"assignees,omitempty"`
	AssignedAt unit.Anchor `json:"assigned_at,omitzero"`
	Basis      Basis       `json:"basis"`
	WaitingOn  []Person    `json:"waiting_on,omitempty"`
	State      State       `json:"state"`
	Resolution Resolution  `json:"resolution,omitempty"`
	Evidence   Evidence    `json:"evidence,omitzero"`
	Trail      []Evidence  `json:"trail,omitempty"` // evidence ops, oldest first, at most 20 kept
	Confidence Confidence  `json:"confidence"`
	Ambiguity  string      `json:"ambiguity,omitempty"`
	Provenance Provenance  `json:"provenance"` // of the op that created it
	Confirmed  bool        `json:"confirmed"`
	UserAt     time.Time   `json:"user_at,omitzero"` // time of the last applied user op
	CreatedAt  time.Time   `json:"created_at"`
	UpdatedAt  time.Time   `json:"updated_at"`
}

type OpKind string // "create", "update", "assign", "resolve", "reopen", "link", "evidence"

const (
	OpCreate   OpKind = "create"
	OpUpdate   OpKind = "update"
	OpAssign   OpKind = "assign"
	OpResolve  OpKind = "resolve"
	OpReopen   OpKind = "reopen"
	OpLink     OpKind = "link"
	OpEvidence OpKind = "evidence"
)

// Op is the only way the ledger changes; it is also the wire form of turn_end's "ledger".
type Op struct {
	Op         OpKind      `json:"op"`
	Unit       unit.Unit   `json:"unit,omitzero"`        // a turn's ops default to the turn's unit
	Item       string      `json:"item,omitempty"`       // required but on create; a create may name its id
	Anchor     unit.Anchor `json:"anchor"`               // required; Anchor.Unit defaults to Unit
	Fields     *Fields     `json:"fields,omitempty"`     // create, update
	Assignees  []Person    `json:"assignees,omitempty"`  // assign
	Basis      Basis       `json:"basis,omitempty"`      // assign
	Resolution Resolution  `json:"resolution,omitempty"` // resolve
	Evidence   *Evidence   `json:"evidence,omitempty"`   // resolve (nil means {message, Anchor}); evidence: required
	To         *unit.Unit  `json:"to,omitempty"`         // link
	LinkKind   link.Kind   `json:"link_kind,omitempty"`  // link
	Reason     string      `json:"reason,omitempty"`
}

// Fields: nil means unchanged; create needs Kind and Summary.
type Fields struct {
	Kind       *Kind       `json:"kind,omitempty"`
	Summary    *string     `json:"summary,omitempty"`
	Context    *string     `json:"context,omitempty"`
	Deadline   *string     `json:"deadline,omitempty"`
	Ambiguity  *string     `json:"ambiguity,omitempty"`
	Options    *[]Option   `json:"options,omitempty"`
	URLs       *[]string   `json:"urls,omitempty"`
	Contexts   *[]string   `json:"contexts,omitempty"`
	Initial    *Initial    `json:"initial,omitempty"`
	Requester  *Person     `json:"requester,omitempty"`
	WaitingOn  *[]Person   `json:"waiting_on,omitempty"`
	Confidence *Confidence `json:"confidence,omitempty"`
}

// ValidateShape names the refusals per field, as record.Problems does.
func (o Op) ValidateShape() error {
	var c checker
	if !slices.Contains(opKinds, o.Op) {
		c.bad("op", "unknown %q", o.Op)
	}
	if o.Unit.IsZero() {
		c.bad("unit", "required")
	}
	c.anchor("anchor", o.Anchor, o.Unit)
	if o.Op != OpCreate && o.Op != OpLink && o.Item == "" {
		c.bad("item", "required")
	}
	if o.Item != "" {
		c.id("item", o.Item)
	}
	switch o.Op {
	case OpCreate:
		if o.Fields == nil {
			c.bad("fields", "required")
			break
		}
		if o.Fields.Kind == nil {
			c.bad("fields.kind", "required")
		}
		if o.Fields.Summary == nil {
			c.bad("fields.summary", "required")
		}
		c.fields(o.Fields)
	case OpUpdate:
		if o.Fields == nil {
			c.bad("fields", "required")
			break
		}
		c.fields(o.Fields)
	case OpAssign:
		if len(o.Assignees) == 0 {
			c.bad("assignees", "required")
		}
		c.people("assignees", o.Assignees)
		if o.Basis != "" && !slices.Contains(bases, o.Basis) {
			c.bad("basis", "unknown %q", o.Basis)
		}
	case OpResolve:
		switch {
		case o.Resolution == "":
			c.bad("resolution", "required")
		case !slices.Contains(resolutions, o.Resolution):
			c.bad("resolution", "unknown %q", o.Resolution)
		}
		if o.Evidence != nil {
			c.evidence(o.Evidence, o.Unit)
		}
	case OpLink:
		if o.To == nil || o.To.IsZero() {
			c.bad("to", "required")
		}
		if !o.LinkKind.Valid() {
			c.bad("link_kind", "unknown %q", o.LinkKind)
		}
	case OpEvidence:
		if o.Evidence == nil {
			c.bad("evidence", "required")
		} else {
			c.evidence(o.Evidence, o.Unit)
		}
	}
	if len(c.p) == 0 {
		return nil
	}
	return c.p
}

// Problems are the refusals of an op, one "<field>: <why>" each.
type Problems []string

func (p Problems) Error() string { return strings.Join(p, "; ") }

var (
	opKinds     = []OpKind{OpCreate, OpUpdate, OpAssign, OpResolve, OpReopen, OpLink, OpEvidence}
	kinds       = []Kind{Request, Question, Decision, Todo}
	resolutions = []Resolution{Done, Answered, Fixed, Delegated, Declined, Dismissed, Superseded, Moot}
	bases       = []Basis{ExplicitMention, DMAddressee, ReplyContext, SelfAssigned, Reassigned, GroupAsk, NoBasis}
	evidences   = []EvidenceKind{EvidenceMessage, EvidenceReaction, EvidenceExternalClaim, EvidenceUnitEvent, EvidenceSentDraft}
	confidences = []Confidence{High, Low}
	initials    = []Initial{"", InitialInbox, InitialNext}
)

const maxID = 100

type checker struct{ p Problems }

func (c *checker) bad(field, format string, args ...any) {
	c.p = append(c.p, field+": "+fmt.Sprintf(format, args...))
}

// anchor checks a with its Unit defaulting to u; a missing u is the unit field's refusal.
func (c *checker) anchor(field string, a unit.Anchor, u unit.Unit) {
	if a.Unit.IsZero() {
		a.Unit = u
	}
	if a.Unit.IsZero() {
		a.Unit = unit.Unit{Provider: "-", Key: "-"}
	}
	if err := a.Valid(); err != nil {
		c.bad(field, "%v", err)
	}
}

func (c *checker) id(field, id string) {
	if len(id) > maxID || strings.TrimSpace(id) != id || strings.ContainsAny(id, " \t\r\n") {
		c.bad(field, "%q is not an item id", id)
	}
}

func (c *checker) text(field, s string, max int) {
	if n := utf8.RuneCountInString(s); n > max {
		c.bad(field, "%d characters; at most %d", n, max)
	}
}

func (c *checker) fields(f *Fields) {
	if f.Kind != nil && !slices.Contains(kinds, *f.Kind) {
		c.bad("fields.kind", "unknown %q", *f.Kind)
	}
	if f.Summary != nil {
		if strings.TrimSpace(*f.Summary) == "" {
			c.bad("fields.summary", "required")
		}
		c.text("fields.summary", *f.Summary, MaxSummary)
	}
	if f.Context != nil {
		c.text("fields.context", *f.Context, MaxContext)
	}
	if f.Confidence != nil && !slices.Contains(confidences, *f.Confidence) {
		c.bad("fields.confidence", "unknown %q", *f.Confidence)
	}
	if f.Initial != nil && !slices.Contains(initials, *f.Initial) {
		c.bad("fields.initial", "unknown %q", *f.Initial)
	}
	if f.Requester != nil {
		c.people("fields.requester", []Person{*f.Requester})
	}
	if f.WaitingOn != nil {
		c.people("fields.waiting_on", *f.WaitingOn)
	}
}

func (c *checker) people(field string, ps []Person) {
	for _, p := range ps {
		if strings.TrimSpace(p.Key) == "" {
			c.bad(field, "a person without a key")
			return
		}
	}
}

func (c *checker) evidence(e *Evidence, u unit.Unit) {
	if !slices.Contains(evidences, e.Kind) {
		c.bad("evidence.kind", "unknown %q", e.Kind)
	}
	c.anchor("evidence.anchor", e.Anchor, u)
}
