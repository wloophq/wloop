package builtin

// keysGrammar is the keys of keys.toml.
var keysGrammar = []Key{
	{Path: "help", In: SurfaceKeys, Kind: KindTable, Doc: "The help line of each core key, by context (list, global, question, review, plan), then action (`nav:inbox`, `done`); a numbered one as `answer:{n}`, `{n}` its number."},
	{Path: "help.*", In: SurfaceKeys, Kind: KindTable, Doc: "The keys of one context, by action."},
	{Path: "help.*.*", In: SurfaceKeys, Kind: KindText, Required: true, Doc: "What the key does, as the help and the palette list it. Required."},
}
