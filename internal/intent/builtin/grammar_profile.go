package builtin

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
