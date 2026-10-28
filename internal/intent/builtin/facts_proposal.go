package builtin

import "github.com/wloophq/wloop/internal/intent/expr"

// ProposalSlot holds the facts the words of an item proposal's gate read.
const ProposalSlot FactSlot = "proposal"

var proposalFacts = []Fact{
	fact("proposal.op", expr.String, []FactSlot{ProposalSlot}, "The operation proposed on the item: update, resolve, reopen and the others an op names."),
	fact("proposal.item", expr.String, []FactSlot{ProposalSlot}, "The id of the item the operation changes."),
	fact("proposal.unit", expr.String, []FactSlot{ProposalSlot}, "The item's unit, as a person reads it."),
	fact("proposal.reason", expr.String, []FactSlot{ProposalSlot}, "Why the operation was proposed; empty when its proposer gave no reason."),
	fact("proposal.provenance", expr.String, []FactSlot{ProposalSlot}, "Who proposed it, as the ledger names its provenance: extracted, turn or derived."),
}
