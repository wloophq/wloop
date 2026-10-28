package builtin

import (
	"slices"

	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/expr"
)

// DraftActionSlot holds the facts the texts of a draft's built-in actions
// read.
const DraftActionSlot FactSlot = "draft_action"

// DraftRefusals are why a draft's action is disabled: its plugin is off,
// the draft is in a state the action does not move it from, or its plugin
// posts nothing.
var DraftRefusals = []string{"", "plugin_off", "state", "posts_nothing"}

// PRRowKinds are a pull request row's situation kinds, as the cockpit
// ranks them.
var PRRowKinds = []string{"round_pending", "loop_waiting", "loop_stopped", "blocked", "to_address", "held", "loop_working",
	"changes", "checks", "conflicts", "reply", "reviewer_back", "review", "no_reviewer", "merge", "draft", "muted", "done"}

// ActionFacts are the facts only the action slot reads, what a row offers
// decides on them, then the draft actions' facts.
var ActionFacts = slices.Concat(rowActionFacts, draftActionFacts)

var rowActionFacts = []Fact{
	prFact("pr.draft", expr.Bool, act, "The pull request is a draft, from the inventory row."),
	prFact("pr.review_requested_me", expr.Bool, act, "A review is requested from the viewer, from the inventory row."),
	enum(fact("row.kind", expr.String, act, "The row's situation kind."), map[unit.ProviderID][]string{unit.GitHub: PRRowKinds}),
	fact("row.open", expr.Bool, act, "The pull request is open: kind is not done, or, untracked, its GitHub state is open."),
	fact("row.tracked", expr.Bool, act, "The pull request is in the inventory."),
	fact("pr.adopted", expr.Bool, act, "The pull request was taken over into the loop."),
	fact("pr.held", expr.Bool, act, "Rounds on the pull request are held."),
	fact("pr.muted", expr.Bool, act, "The pull request is muted, by the server clock."),
	fact("row.question", expr.Bool, act, "A turn waits on the user's answer."),
	fact("row.pending", expr.Bool, act, "A round is pending."),
	fact("row.capped", expr.Bool, act, "The daily budget held the row's automatic round."),
	fact("row.intake", expr.Bool, act, "An intake is open on the pull request."),
	fact("row.intake_seq", expr.Int, act, "The number of the open intake, the waiting one first; 0 when none is open."),
	fact("row.author_workspace", expr.Bool, act, "A linked workspace holds the author role."),
	fact("row.yours", expr.Bool, act, "The row's unit is the viewer's to act on: any unit but a pull request, or a pull request that is the viewer's, taken over or held by an author workspace."),
	fact("row.review_workspace", expr.Bool, act, "A linked workspace holds the review role."),
	fact("row.trust_folder", expr.String, act, "The folder whose trust lifts the row's start block; empty for a block on another question."),
	fact("row.trust_homes", expr.List, act, "The agent homes trusting row.trust_folder writes in."),
	fact("row.remove_refusal", expr.String, act, "What refused removing the row's workspace, which a force remove overrides; empty when nothing did."),
	enum(fact("row.recommended", expr.String, act, "The runner recommended for the row's items: session or headless; empty when none is."),
		enumOf("", "session", "headless")),
	taskFact("entry.captured_by_user", expr.Bool, act, "The user captured the entry: origin user and kind capture; false for a derived entry, such as one from a Slack thread."),
	fact("pr.nudge.who", expr.List, act, "Whom a nudge would address, from nudge.targets."),
	fact("pr.nudge.why", expr.String, act, "Why the pull request waits on them, from nudge.targets."),
	fact("pr.nudge.refusal", expr.String, act, "The text nudge.prepare would refuse with now; empty when allowed."),
}

var draftActionFacts = []Fact{
	fact("draft.state", expr.String, []FactSlot{DraftActionSlot}, "The draft's state."),
	fact("draft.provider", expr.String, []FactSlot{DraftActionSlot}, "The provider of the draft's unit."),
	enum(fact("draft.refusal", expr.String, []FactSlot{DraftActionSlot}, "Why the action is disabled: plugin_off, state or posts_nothing; empty when it is enabled."),
		enumOf(DraftRefusals...)),
	fact("draft.maybe_posted", expr.Bool, []FactSlot{DraftActionSlot}, "The draft failed after its post may have reached its provider."),
}

// taskFact is a fact of a GTD entry, which only entry intents read.
func taskFact(name string, t expr.Type, slots []FactSlot, doc string) Fact {
	f := fact(name, t, slots, doc)
	f.Provider = unit.Task
	return f
}
