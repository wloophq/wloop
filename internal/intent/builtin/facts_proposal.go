package builtin

import "github.com/wloophq/wloop/internal/intent/expr"

// ProposalSlot holds the facts the words of a proposal gate read: an item proposal's, or a verb's gate.
const ProposalSlot FactSlot = "proposal"

var proposalFacts = []Fact{
	fact("proposal.op", expr.String, []FactSlot{ProposalSlot}, "The operation proposed on the item: update, resolve, reopen and the others an op names."),
	fact("proposal.item", expr.String, []FactSlot{ProposalSlot}, "The id of the item the operation changes."),
	fact("proposal.unit", expr.String, []FactSlot{ProposalSlot}, "The item's unit, as a person reads it."),
	fact("proposal.reason", expr.String, []FactSlot{ProposalSlot}, "Why the operation was proposed; empty when its proposer gave no reason."),
	fact("proposal.provenance", expr.String, []FactSlot{ProposalSlot}, "Who proposed it, as the ledger names its provenance: extracted, turn or derived."),
	fact("proposal.verb", expr.String, []FactSlot{ProposalSlot}, "On a verb's gate, the verb it runs once approved; empty on an item proposal's."),
	fact("proposal.task", expr.String, []FactSlot{ProposalSlot}, "On a verb's gate, the title of the task the verb runs on."),
	fact("proposal.other", expr.String, []FactSlot{ProposalSlot}, "On the gate of a verb that takes another task, that task's title; empty otherwise."),
	fact("proposal.keep", expr.String, []FactSlot{ProposalSlot}, "On a merge's gate, the title of the task the survivor rule keeps as the gate opens; empty otherwise."),
	fact("proposal.fold", expr.String, []FactSlot{ProposalSlot}, "On a merge's gate, the title of the task it folds into proposal.keep; empty otherwise."),
}
