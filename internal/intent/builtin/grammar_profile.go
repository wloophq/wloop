package builtin

// profileGrammar is the keys of defaults.toml and labels.toml.
var profileGrammar = []Key{
	{Path: "provider", In: SurfaceDefaults, Kind: KindText, Doc: "The provider of an intent whose manifest names none."},

	{Path: "other", In: SurfaceLabels, Kind: KindText, Doc: "The heading of a dimension dims does not name: {Dim} and {value}."},
	{Path: "tag", In: SurfaceLabels, Kind: KindText, Doc: "The heading of a tag condition: {key} and {value}."},
	{Path: "dims", In: SurfaceLabels, Kind: KindMap, Doc: "A dimension's heading, by dimension: {value}."},
	{Path: "dims.*", In: SurfaceLabels, Kind: KindText, Doc: "The dimension's heading."},
	{Path: "values", In: SurfaceLabels, Kind: KindMap, Doc: "The heading of one condition, by \"<dim>=<value>\"."},
	{Path: "values.*", In: SurfaceLabels, Kind: KindText, Doc: "The condition's heading."},
}
