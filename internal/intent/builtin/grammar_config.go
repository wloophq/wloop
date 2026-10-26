package builtin

// configGrammar is the configuration's keys.
var configGrammar = []Key{
	{Path: "workflows.*.only_when", In: SurfaceConfig, Kind: KindPred, Doc: "A condition ANDed with the workflow's when."},
	{Path: "workflows.*.draft_approval.rule[].when", In: SurfaceConfig, Kind: KindPred, Doc: "When the approval rule applies to a draft."},
	{Path: "intents.*.project_settings.when", In: SurfaceConfig, Kind: KindPred, Doc: "Where the intent's sessions load the checkout's settings."},
}
