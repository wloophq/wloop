// Package turn renders what an agent reads for one turn of the loop: the
// context of the turn, its instructions and the prompt that points to them.
package turn

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/core/instructions"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/profile"
)

type Intent string

type Trigger int

const (
	ByHand Trigger = iota
	Automated
)

func (t Trigger) String() string {
	if t == Automated {
		return "automated"
	}
	return "by-hand"
}

// Planned is the review round the sweep decided to send. A round asked for
// by hand is Unplanned: your reviews on GitHub make it a follow-up or not.
type Planned int

const (
	Unplanned Planned = iota
	PlannedFirst
	PlannedFollowUp
)

func (p Planned) String() string {
	switch p {
	case PlannedFirst:
		return "first"
	case PlannedFollowUp:
		return "follow-up"
	}
	return ""
}

// PriorReview is your latest review of a pull request: the head it covered,
// when, and the latest verdict that was not a comment, "" when all were.
// Endorsed is the endorsement of the turn that published the last verdict,
// "" when no turn of the loop's did.
type PriorReview struct {
	Verdict, Head, Endorsed string
	At                      time.Time
}

// RecordedVerdict is the newest verdict a turn of the loop's published: the
// head its record covered, its endorsement and when it was recorded.
type RecordedVerdict struct {
	Head, Endorsed string
	At             time.Time
}

type PR struct {
	URL, Owner, Repo string
	Number           int
	Title, Author    string
	State            string
	// Association is GitHub's authorAssociation, such as FIRST_TIME_CONTRIBUTOR.
	Association     string
	Head, Base      string
	Labels          []string
	CrossRepository bool
	// Visibility is the repository's: public, private or internal; empty
	// when not read.
	Visibility string
	Prior      *PriorReview
	// ReviewRequested says your review is requested again.
	ReviewRequested bool
	// LastReviewVerdict is the state of your newest review, comments included.
	LastReviewVerdict string
	// Address is why an address round runs now; nil on any other turn.
	Address *RoundReason
}

// RoundReason is what an address round answers: the reviewers, the
// failing checks and a conflict with the base. Handled marks what an earlier
// round already covered.
type RoundReason struct {
	Reviewers []RoundReviewer
	Checks    []FailingCheck
	Conflict  *RoundConflict
}

// RoundConflict is the head's conflict with Base, as GitHub reports it.
type RoundConflict struct {
	Base    string
	Handled bool
}

type RoundReviewer struct {
	Login   string
	Handled bool
}

// FailingCheck is a check that failed on the head; Summary and URL are the
// CI's own text.
type FailingCheck struct {
	Name, Summary, URL string
	Handled            bool
}

func (r *RoundReason) ReviewerLogins() []string {
	out := []string{}
	if r != nil {
		for _, v := range r.Reviewers {
			out = append(out, v.Login)
		}
	}
	return out
}

func (r *RoundReason) CheckNames() []string {
	out := []string{}
	if r != nil {
		for _, c := range r.Checks {
			out = append(out, c.Name)
		}
	}
	return out
}

func (p PR) Slug() string { return fmt.Sprintf("%s/%s#%d", p.Owner, p.Repo, p.Number) }

// Request is what a caller knows when it starts a turn; Build completes it
// into a Context.
type Request struct {
	Intent  Intent
	Trigger Trigger
	Planned Planned
	Unit    unit.Unit
	// LastVerdict anchors PR.Prior to the head the loop's record covered, which
	// GitHub's review commit drifts from after later pushes.
	LastVerdict *RecordedVerdict
	Guidance    string
	Local       string // work in the checkout not on the pull request
	Queued      bool   // the prompt waits behind the agent's current turn
	Agent       string
	// Plain is profile try's dry run: the turn publishes and records nothing.
	Plain bool
	// Owned says the caller checked the pull request is the user's to develop.
	Owned bool
	// Inputs are the intent's declared inputs the caller set, by name.
	Inputs map[string]any
	// Payload nil makes Build read it through Builder.Payloads.
	Payload Payload

	History []instructions.PastTurn
	// Resume makes this a resume turn of one whose session is gone.
	Resume *instructions.Resume
	// BriefMissing says why the turn goes without a brief, when it does.
	BriefMissing string
	// Why is the workflow that started the turn; nil for a turn started by hand.
	Why *workflow.Why
	// Items and Entry are the unit's item ids and the GTD entry the turn was asked about.
	Items []string
	Entry string
}

// TurnFacts are the facts of a turn Build reads besides the request.
type TurnFacts struct {
	Mine, CrossRepository                                                             bool
	LastReviewVerdict                                                                 string
	AddressReviews, AddressChecks, FollowupsWithoutWorkspace, AddressWithoutWorkspace bool
	// RequestedTeams are the teams a review was requested through, as <org>-<slug>.
	RequestedTeams []string
}

type Context struct {
	Intent  Intent
	Trigger Trigger
	Planned Planned
	Plain   bool
	Owned   bool
	// NeedsTakeOver, Round, FollowUpApproval, Headline and Records are
	// filled by Complete from the intent's manifest.
	NeedsTakeOver      bool
	Queued             bool
	Guidance           string
	Local              string
	Inputs             map[string]any
	Facts              TurnFacts
	Def                dintent.Def
	Round              string
	FollowUpApproval   bool
	Headline           string
	Records            bool
	AutomatedReviewers []string
	Guide              profile.Guide
	// Guides are the composed guides by profile: the main one and each also_profile one that applies.
	Guides        map[string]profile.Guide
	Brief         *Brief
	BriefMissing  string
	Agent         string
	Command       string
	ManagerBinary string
	History       []instructions.PastTurn
	Resume        *instructions.Resume
	// Why is nil for a turn started by hand.
	Why *Why
	// Unit, Payload, Wording and Items are the turn's unit as its provider hands it.
	Unit    unit.Unit
	Payload Payload
	Wording Wording
	Items   []instructions.OpenItem // the unit's open items, filled through Builder.Items
	// Dossier is the unit's dossier, nil when it has none or the turn names none.
	Dossier *Dossier
	// WithoutProjectSettings says the session runs without the checkout's
	// own agent settings, so its instruction files are not in context.
	WithoutProjectSettings bool
}

// Dossier is a unit's dossier as a session turn reaches it.
type Dossier struct {
	Path string
	// Granted says the session was launched with the dossier in its policy.
	Granted bool
}

// Why is what made a workflow start the turn, as the instructions render it.
type Why struct {
	Workflow, Trigger, Event, EventData string
	Matched                             []WhyFact
}

// WhyFact is a fact the workflow's condition read, with its value as text.
type WhyFact struct{ Name, Value string }

func whyOf(w *workflow.Why) *Why {
	if w == nil {
		return nil
	}
	out := &Why{Workflow: w.Workflow, Trigger: string(w.Trigger)}
	if w.Event != nil {
		out.Event, out.EventData = w.Event.Kind, w.Event.Data
	}
	for _, f := range w.Matched {
		out.Matched = append(out.Matched, WhyFact{Name: f.Name, Value: FactText(f.Value)})
	}
	return out
}

// FactText is a fact value as the why shows it.
func FactText(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case []string:
		return strings.Join(v, ", ")
	case []any:
		parts := make([]string, len(v))
		for i, e := range v {
			parts[i] = FactText(e)
		}
		return strings.Join(parts, ", ")
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case time.Time:
		return v.UTC().Format(time.RFC3339)
	case time.Duration:
		return v.String()
	}
	return fmt.Sprint(v)
}

// WhySection is the why section of rendered instructions, from its heading to
// the next one; empty when the instructions carry none.
func WhySection(text string) string {
	at := strings.Index(text, "\n"+whyHeading+"\n")
	if at < 0 {
		return ""
	}
	section := text[at+1:]
	if end := strings.Index(section[len(whyHeading):], "\n## "); end >= 0 {
		section = section[:len(whyHeading)+end]
	}
	return strings.TrimRight(section, "\n")
}

const whyHeading = "## Why this turn started"

// PR is the github payload, nil on any other unit.
func (c Context) PR() *PR {
	p, _ := c.Payload.(*PR)
	return p
}

// prior is the github payload's prior review, nil without one.
func (c Context) prior() *PriorReview {
	if p := c.PR(); p != nil {
		return p.Prior
	}
	return nil
}

func (c Context) Automated() bool { return c.Trigger == Automated }
