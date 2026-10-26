// Package catalog is the action catalog: the actions a pull request's row
// offers, the builtin ones and the intents', derived from facts the daemon
// gathers.
package catalog

import (
	"slices"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/pr"
	intentload "github.com/wloophq/wloop/internal/intent"
)

// Facts are what decides the actions a row offers; the action slot of
// manifest predicates reads the same values through Get.
type Facts struct {
	Ref pr.Ref
	Row api.Row
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
	if !f.Row.Unit.IsZero() && f.Row.Unit != f.Ref.Unit() {
		return true
	}
	return f.Mine || f.Override.Adopted || f.authorWorkspace()
}

func (f Facts) slug() string { return f.Ref.String() }

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
		return f.Ref.Owner
	case "pr.repo":
		return f.Ref.Repo
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
	case "row.adopted":
		return o.Adopted
	case "row.held":
		return o.Hold
	case "row.muted":
		return f.Muted
	case "row.question":
		return f.Question
	case "row.pending":
		return f.Row.Pending != nil
	case "row.intake":
		return f.intake() != nil
	case "row.author_workspace":
		return f.authorWorkspace()
	case "row.yours":
		return f.yours()
	case "row.review_workspace":
		return f.Workspace.ID != "" && f.Workspace.IsReview
	case "row.nudge.who":
		return slices.Clone(f.NudgeWho)
	case "row.nudge.why":
		return f.NudgeWhy
	case "row.nudge.refusal":
		return f.NudgeRefusal
	case "config.address_reviews":
		return f.Config.AddressReviews
	case "config.address_checks":
		return f.Config.AddressChecks
	case "config.followups_without_workspace":
		return f.Config.FollowupsWithoutWorkspace
	case "config.address_without_workspace":
		return f.Config.AddressWithoutWorkspace
	}
	return nil
}
