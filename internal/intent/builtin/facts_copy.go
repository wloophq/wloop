package builtin

import "github.com/wloophq/wloop/internal/intent/expr"

// CopySlot holds the facts the copy texts of a built-in action read: the unit it runs on and its brief.
const CopySlot FactSlot = "copy"

var cpy = []FactSlot{CopySlot}

var copyFacts = []Fact{
	fact("copy.title", expr.String, cpy, "The unit's title."),
	fact("copy.link", expr.String, cpy, "The unit's web address; empty when it has none that is http or https."),
	fact("copy.label", expr.String, cpy, "The unit as people read it, such as owner/repo#12."),
	fact("copy.brief.goal", expr.String, cpy, "The brief's goal; empty when the unit has no brief."),
	fact("copy.brief.context", expr.String, cpy, "The brief's context; empty when the unit has no brief."),
	fact("copy.brief.kind", expr.String, cpy, "The brief's kind; empty when the unit has no brief."),
	fact("copy.brief.size", expr.String, cpy, "The brief's size; empty when the unit has no brief."),
	fact("copy.brief.risk", expr.String, cpy, "The brief's risk: low, medium or high; empty when the unit has no brief."),
	fact("copy.brief.risk_why", expr.String, cpy, "Why the brief rates its risk so; empty when the unit has no brief."),
	fact("copy.brief.open_questions", expr.List, cpy, "The brief's open questions; empty when the unit has no brief."),
}
