package builtin

// intentGrammar is the intent manifest's keys.
var intentGrammar = []Key{
	{Path: "records", In: SurfaceIntent, Kind: KindPred, Default: "true", Doc: "When the turn records its outcome."},
	{Path: "also_profiles[].when", In: SurfaceIntent, Kind: KindPred, Doc: "When the turn reads the other judgment."},
	{Path: "headline.new[].when", In: SurfaceIntent, Kind: KindPred, Doc: "When the headline case applies."},
	{Path: "draft_approval.rule[].when", In: SurfaceIntent, Kind: KindPred, Doc: "When the approval rule applies to a draft."},
}
