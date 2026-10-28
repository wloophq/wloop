// Package gate is a decision waiting on the user: a draft to send, a round
// to run, a question to answer, a proposal to decide. The gate holds the
// decision; its subject keeps its own lifecycle in its own table.
package gate

import (
	"time"

	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Kind is what a gate decides, which names its consequence.
type Kind string

const (
	KindDraft    Kind = "draft"    // publish a draft
	KindPlan     Kind = "plan"     // release a plan item, a round
	KindQuestion Kind = "question" // deliver an answer to a session
	KindPost     Kind = "post"     // deliver an acknowledgement to a session
	KindProposal Kind = "proposal" // decide proposed task or item operations
	KindTrust    Kind = "trust"    // unblock a repository's first run
	KindStep     Kind = "step"     // a profile workflow's gate.open
)

// Kinds are every gate kind.
var Kinds = []Kind{KindDraft, KindPlan, KindQuestion, KindPost, KindProposal, KindTrust, KindStep}

// Status is where a gate's decision stands; every status but Open is final.
type Status string

const (
	Open       Status = "open"
	Approved   Status = "approved"
	Rejected   Status = "rejected"
	Answered   Status = "answered"
	Dismissed  Status = "dismissed"
	Superseded Status = "superseded"
)

// Gate is one decision.
//
// Task is "" while no task holds the gate's unit: the store keeps NULL. The
// gate sets it when its unit has a task; otherwise the reconcile of W1a
// back-fills it once a task takes the unit; a message on a zero unit gets a
// capture task the messages service creates, titled with the message's text.
//
// Subject names the subject in its own table (a draft id, a plan item key,
// a message id), as the kind reads it. Intents are the names a person may
// resolve the gate with, in the order clients offer them; Intent is the one
// it was resolved with, its Status the one gates.toml gives that intent.
// Deadline is zero for a gate that waits for ever; one that passes resolves
// to Default with the rule "window". Intent, By, Rule, Answer and ClosedAt are
// zero while Open.
type Gate struct {
	ID       int64
	Task     task.ID
	Unit     unit.Unit
	Kind     Kind
	Name     string
	Subject  string
	Status   Status
	Intents  []string
	Intent   string
	Answer   string
	By       task.Actor
	Rule     string
	Deadline time.Time
	Default  string
	OpenedAt time.Time
	ClosedAt time.Time
}
