package builtin

// gatesGrammar is the keys of gates.toml.
var gatesGrammar = []Key{
	{Path: "gates", In: SurfaceGates, Kind: KindMap, Doc: "The gates a profile names, by name."},
	{Path: "gates.*", In: SurfaceGates, Kind: KindTable, Doc: "A gate, by its name: the one a gate.open step, an intent's record.missing or a verb's by.<actor> opens."},
	{Path: "gates.*.label", In: SurfaceGates, Kind: KindText, Doc: "What the gate asks of you, as clients list it."},
	{Path: "gates.*.intents", In: SurfaceGates, Kind: KindMap, Doc: "What you may resolve the gate with, by intent, in the order clients offer them."},
	{Path: "gates.*.intents.*", In: SurfaceGates, Kind: KindTable, Doc: "An intent, by its name."},
	{Path: "gates.*.intents.*.label", In: SurfaceGates, Kind: KindText, Doc: "The name people read on its button."},
}
