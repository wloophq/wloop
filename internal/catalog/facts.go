// Package catalog is the action catalog: the actions a pull request's row
// offers, the builtin ones and the intents', derived from facts the daemon
// gathers.
package catalog

import (
	"slices"

	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"

	"github.com/wloophq/wloop/internal/api"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// Facts are what decides the actions a row offers; the action slot of
// manifest predicates reads the same values through Get.
type Facts struct {
	// PR is the unit of the row's pull request, and Owner and Repo its
	// repository; zero on a row of any other unit.
	PR          unit.Unit
	Owner, Repo string
	Row         api.Row
	// Author, Workspace and Override are the pull request's, from its row's
	// payload.
	Author    string
	Workspace api.Workspace
	Override  api.Override
	// HeldByCap is a row whose automatic round the daily cap held.
	HeldByCap bool
	Tracked   bool
	Open      bool
	Mine      bool
	Muted     bool
	// MutedStored is a mute kept in the override, active or not.
	MutedStored       bool
	Draft             bool
	CrossRepository   bool
	ReviewRequestedMe bool
	State, Base       string
	Question          bool
	// StartBlocked is a round a start block holds until the user acts.
	StartBlocked bool
	// TrustFolder is the folder whose trust lifts the start block, "" for
	// a block on another question, and TrustHomes the agent homes trusting
	// it writes in.
	TrustFolder string
	TrustHomes  []string
	// Intakes are the open intakes, the oldest first.
	Intakes         []Intake
	NudgeWho        []string
	NudgeWhy        string
	NudgeRefusal    string
	GateOn, BriefOn bool
	Forceable       string
	Config          Config
	// Asked is the action a request asks for, whose inputs an open intake
	// joins on; zero for a row served to a client.
	Asked   Asked
	Intents *intentload.Loaded
	// CapturedByUser is a row of a task the user captured; false for any
	// other row, a derived task's included.
	CapturedByUser bool
	// Recommended is the execution mode recommended for the row's items ("session", "headless"), "" none;
	// the row offers every mode's action whatever it says.
	Recommended string
}

// Config is the configuration the action slot reads.
type Config struct {
	AddressReviews, AddressChecks                      bool
	FollowupsWithoutWorkspace, AddressWithoutWorkspace bool
}

// Intake is an open intake with the action it carries, "" for a turn by
// hand, and its request's inputs.
type Intake struct {
	api.Intake
	Action      string
	Inputs      map[string]any
	FromSession bool
}

// Asked is an action a request asks for, with its inputs.
type Asked struct {
	Name   string
	Inputs map[string]any
}

// intake is the oldest waiting intake, else the oldest open one.
func (f Facts) intake() *Intake {
	if len(f.Intakes) == 0 {
		return nil
	}
	if i := slices.IndexFunc(f.Intakes, func(o Intake) bool { return o.Waiting() }); i >= 0 {
		return &f.Intakes[i]
	}
	return &f.Intakes[0]
}

func (f Facts) authorWorkspace() bool {
	w := f.Workspace
	return w.ID != "" && !w.IsReview && !w.IsMain
}

// yours says the row's unit is the user's to act on: any unit but a pull request, or a pull request that
// is the user's, taken over or held by an author workspace.
func (f Facts) yours() bool {
	if !f.Row.Unit.IsZero() && f.Row.Unit != f.PR {
		return true
	}
	return f.Mine || f.Override.Adopted || f.authorWorkspace()
}

func (f Facts) slug() string { return f.PR.Key }

// Get resolves a fact of the action slot by its dotted name; nil for one
// the slot does not have.
func (f Facts) Get(field string) any {
	o := f.Override
	switch field {
	case "pr.mine":
		return f.Mine
	case "pr.author":
		return f.Author
	case "pr.state":
		return f.State
	case "pr.slug":
		return f.slug()
	case "pr.owner":
		return f.Owner
	case "pr.repo":
		return f.Owner + "/" + f.Repo
	case "pr.repo_name":
		return f.Repo
	case "pr.base":
		return f.Base
	case "pr.cross_repository":
		return f.CrossRepository
	case "pr.draft":
		return f.Draft
	case "pr.review_requested_me":
		return f.ReviewRequestedMe
	case "row.kind":
		return string(f.Row.Kind)
	case "row.open":
		return f.Open
	case "row.tracked":
		return f.Tracked
	case "pr.adopted":
		return o.Adopted
	case "pr.held":
		return o.Hold
	case "pr.muted":
		return f.Muted
	case "row.question":
		return f.Question
	case "row.pending":
		return f.Row.Pending != nil
	case "row.capped":
		return f.HeldByCap
	case "row.intake":
		return f.intake() != nil
	case "row.intake_seq":
		if i := f.intake(); i != nil {
			return int(i.Seq)
		}
		return 0
	case "row.trust_folder":
		return f.TrustFolder
	case "row.trust_homes":
		return slices.Clone(f.TrustHomes)
	case "row.remove_refusal":
		return f.Forceable
	case "row.recommended":
		return f.Recommended
	case "row.author_workspace":
		return f.authorWorkspace()
	case "row.yours":
		return f.yours()
	case "row.review_workspace":
		return f.Workspace.ID != "" && f.Workspace.IsReview
	case "task.captured_by_user":
		return f.CapturedByUser
	case "pr.nudge.who":
		return slices.Clone(f.NudgeWho)
	case "pr.nudge.why":
		return f.NudgeWhy
	case "pr.nudge.refusal":
		return f.NudgeRefusal
	case "config.plugins.github.address_reviews":
		return f.Config.AddressReviews
	case "config.plugins.github.address_checks":
		return f.Config.AddressChecks
	case "config.plugins.github.followups_without_workspace":
		return f.Config.FollowupsWithoutWorkspace
	case "config.plugins.github.address_without_workspace":
		return f.Config.AddressWithoutWorkspace
	}
	return nil
}

// OfferFacts are the facts of e a verb's offer reads.
func OfferFacts(e task.Task) workflow.Facts {
	f := workflow.Facts{"task.id": string(e.ID), builtin.OfferKind: e.Prop(task.PropKind).Text, "task.state": string(e.State),
		"task.muted": e.Snooze.Mute, "task.unit": "", "task.unit_provider": ""}
	if u, ok := e.FirstUnit(); ok {
		f["task.unit"], f["task.unit_provider"] = u.String(), string(u.Provider)
	}
	return f
}
