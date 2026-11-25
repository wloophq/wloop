package builtin

// predsGrammar is the keys of a plugin layer's predicates.toml.
var predsGrammar = []Key{
	{Path: "*", In: SurfacePreds, Kind: KindPred, Doc: "A predicate the plugin's manifest declares in [[source.predicates]], by name, over the facts its facts list names, the ones the plugin hands with the question; " +
		"a higher layer's replaces a lower one's, and one that does not compile leaves the lower one in force. The plugin asks whether it holds for a unit with the unit's facts, " +
		"such as the GitHub plugin's reviewer_required: `!(pr.owner in ['acme'])`."},
	{Path: "keep", In: SurfacePreds, Kind: KindMap, Doc: "By predicate, a fact a predicate set in this layer or a higher one should read: one that does not loads with the entry's warning, which `profile check` prints."},
	{Path: "keep.*", In: SurfacePreds, Kind: KindTable, Doc: "The fact the predicate should read, and the warning one that does not loads with."},
	{Path: "keep.*.fact", In: SurfacePreds, Kind: KindFact, Doc: "The fact, such as the GitHub plugin's pr.review_requested_me for review_wanted."},
	{Path: "keep.*.warning", In: SurfacePreds, Kind: KindText, Doc: "What `profile check` says of a predicate that does not read the fact: why dropping it misleads, and how to keep it."},
}

// profileGrammar is the keys of defaults.toml, labels.toml and a judgment
// block's front matter.
var profileGrammar = []Key{
	{Path: "provider", In: SurfaceDefaults, Kind: KindEnum, Values: intentProviderValues, Doc: "The provider of an intent whose manifest names none."},
	{Path: "session_name", In: SurfaceDefaults, Kind: KindTemplate, Doc: "The session.name of an intent whose manifest has none."},
	{Path: "refusal", In: SurfaceDefaults, Kind: KindText, Doc: "What a [[refuse]] case whose when holds says when its why renders empty."},

	{Path: "other", In: SurfaceLabels, Kind: KindText, Doc: "The heading of a dimension dims does not name: {Dim} and {value}."},
	{Path: "tag", In: SurfaceLabels, Kind: KindText, Doc: "The heading of a tag condition: {key} and {value}."},
	{Path: "dims", In: SurfaceLabels, Kind: KindMap, Doc: "A dimension's heading, by dimension: {value}."},
	{Path: "dims.*", In: SurfaceLabels, Kind: KindText, Doc: "The dimension's heading."},
	{Path: "values", In: SurfaceLabels, Kind: KindMap, Doc: "The heading of one condition, by \"<dim>=<value>\"."},
	{Path: "values.*", In: SurfaceLabels, Kind: KindText, Doc: "The condition's heading."},

	{Path: "override", In: SurfaceBlock, Kind: KindBool, Default: "false", Doc: "Drops the blocks before it whose path is an ancestor of its own."},
	{Path: "title", In: SurfaceBlock, Kind: KindText, Doc: "Replaces the block's heading."},
	{Path: "include", In: SurfaceBlock, Kind: KindList, Elem: KindFile, Doc: "Up to eight .md or .txt files of the profile the block inlines, relative to it."},
	{Path: "paths", In: SurfaceBlock, Kind: KindList, Elem: KindText, Doc: "The block applies only when a changed file matches one of these globs."},
	{Path: "delegates", In: SurfaceBlock, Kind: KindMap, Doc: "Per agent, skills the finding step runs besides the procedure's own work."},
	{Path: "delegates.*", In: SurfaceBlock, Kind: KindList, Elem: KindText, Doc: "The agent's skills."},
}
