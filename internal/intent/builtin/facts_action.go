package builtin

import "github.com/wloophq/wloop/internal/intent/expr"

// ActionFacts are the facts only the action slot reads: what a row offers
// decides on them.
var ActionFacts = []Fact{
	prFact("pr.draft", expr.Bool, act, "The pull request is a draft, from the inventory row."),
	prFact("pr.review_requested_me", expr.Bool, act, "A review is requested from the viewer, from the inventory row."),
	fact("row.kind", expr.String, act, "The row's situation kind."),
	fact("row.open", expr.Bool, act, "The pull request is open: kind is not done, or, untracked, its GitHub state is open."),
	fact("row.tracked", expr.Bool, act, "The pull request is in the inventory."),
	fact("pr.adopted", expr.Bool, act, "The pull request was taken over into the loop."),
	fact("pr.held", expr.Bool, act, "Rounds on the pull request are held."),
	fact("pr.muted", expr.Bool, act, "The pull request is muted, by the server clock."),
	fact("row.question", expr.Bool, act, "A turn waits on the user's answer."),
	fact("row.pending", expr.Bool, act, "A round is pending."),
	fact("row.intake", expr.Bool, act, "An intake is open on the pull request."),
	fact("row.author_workspace", expr.Bool, act, "A linked workspace holds the author role."),
	fact("row.yours", expr.Bool, act, "The row's unit is the viewer's to act on: any unit but a pull request, or a pull request that is the viewer's, taken over or held by an author workspace."),
	fact("row.review_workspace", expr.Bool, act, "A linked workspace holds the review role."),
	fact("pr.nudge.who", expr.List, act, "Whom a nudge would address, from nudge.targets."),
	fact("pr.nudge.why", expr.String, act, "Why the pull request waits on them, from nudge.targets."),
	fact("pr.nudge.refusal", expr.String, act, "The text nudge.prepare would refuse with now; empty when allowed."),
}
