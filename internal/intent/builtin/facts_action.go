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
// the draft is in a state the action does not move it from, its plugin
// posts nothing, it applies no effect without its message, one of its
// effects needs its message, no workflow proposed it, or its workflow is
// already stopped on its conversation.
var DraftRefusals = []string{"", "plugin_off", "state", "posts_nothing", "no_effects", "effect_posts", "not_from_workflow", "skipped"}

// DraftViewSlot holds the facts a draft facet reads: what the Outbox groups,
// filters, sorts and searches its drafts by.
const DraftViewSlot FactSlot = "draft_view"

// DraftViewEnv is the env of a draft facet's fact.
func DraftViewEnv() expr.Env { return Env(DraftViewSlot, nil) }

// RowKindFact is the row's situation kind, which each plugin's row_kinds
// close on its units.
const RowKindFact = "row.kind"

// ActionFacts are the facts the action slot reads, what a row offers decides
// on them, then the draft actions' facts, some of which a draft facet reads
// too.
var ActionFacts = slices.Concat(rowActionFacts, draftActionFacts)

// coreActionFacts are ActionFacts before Merge adds the plugins' action facts.
var coreActionFacts = slices.Clone(ActionFacts)

var rowActionFacts = []Fact{
	fact(RowKindFact, expr.String, act, "The row's situation kind; a plugin's row_kinds close it on its units."),
	fact("row.open", expr.Bool, act, "The pull request is open: kind is not done, or, untracked, its GitHub state is open."),
	fact("row.tracked", expr.Bool, act, "The pull request is in the inventory."),
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
	taskFact("task.captured_by_user", expr.Bool, act, "The user captured the task: origin user and kind capture; false for a derived task, such as one from a Slack thread."),
}

var draftViewFacts = []Fact{
	fact("draft.workflow", expr.String, []FactSlot{DraftViewSlot}, "The workflow behind the draft, a turn's draft included; empty for a draft no workflow is behind."),
	fact("draft.kind", expr.String, []FactSlot{DraftViewSlot}, "The draft's kind as its plugin's manifest names it, such as review, thread_reply or nudge."),
	fact("draft.where", expr.String, []FactSlot{DraftViewSlot}, "Where the draft's unit is, as its row gives it, a repository or a channel; for a draft on a task whose row gives none, the task's value of the task facet of the same id, such as its placement; empty when neither gives one."),
	fact("draft.people", expr.List, []FactSlot{DraftViewSlot}, "The people the draft is to or mentions, each by the name its plugin resolves the mention to, else by the mention key, such as slack:U0123."),
	fact("draft.created_at", expr.Time, []FactSlot{DraftViewSlot}, "When the draft was written. Zero when unset; test with `> timestamp(0)`."),
}

var draftActionFacts = slices.Concat(draftViewFacts, []Fact{
	fact("draft.state", expr.String, []FactSlot{DraftActionSlot, DraftViewSlot}, "The draft's state."),
	fact("draft.provider", expr.String, []FactSlot{DraftActionSlot, DraftViewSlot}, "The provider whose plugin posts the draft: its unit's, or, for a draft on a unit no plugin posts to, such as a task, the one its target names."),
	enum(fact("draft.refusal", expr.String, []FactSlot{DraftActionSlot}, "Why the action is disabled: plugin_off, state, posts_nothing, no_effects, effect_posts, not_from_workflow or skipped; empty when it is enabled."),
		enumOf(DraftRefusals...)),
	fact("draft.maybe_posted", expr.Bool, []FactSlot{DraftActionSlot}, "The draft failed after its post may have reached its provider."),
})

// taskFact is a fact of a task, which only task intents read.
func taskFact(name string, t expr.Type, slots []FactSlot, doc string) Fact {
	f := fact(name, t, slots, doc)
	f.Provider = unit.Task
	return f
}
