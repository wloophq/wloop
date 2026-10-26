package builtin

// workflowGrammar is the workflow manifest's keys.
var workflowGrammar = []Key{
	{Path: "when", In: SurfaceWorkflow, Kind: KindPred, Doc: "The match condition."},
	{Path: "approval.wait_when", In: SurfaceWorkflow, Kind: KindPred, Doc: "When a matched item waits for a person."},
	{Path: "withdraw.when", In: SurfaceWorkflow, Kind: KindPred, Doc: "When an open draft the workflow proposed is withdrawn."},
	{Path: "draft_approval.rule[].when", In: SurfaceWorkflow, Kind: KindPred, Doc: "When the approval rule applies to a draft."},
}
