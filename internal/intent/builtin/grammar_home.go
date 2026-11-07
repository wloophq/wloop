package builtin

import "slices"

// SurfaceHome is home.toml, the cockpit's home: the briefing's counters and words, its sections and the
// capture field.
const SurfaceHome Surface = "home"

// HomeTimes are what a home section's rows show the time of.
func HomeTimes() []Value {
	return []Value{{Name: "owed", Doc: "Since when the task's next step is owed."}, {Name: "moved", Doc: "When the task last moved."}}
}

var homeGrammar = slices.Concat([]Key{
	{Path: "template", In: SurfaceHome, Kind: KindText, Default: "default", Doc: "The briefing's template, which a client may draw its own way."},
	{Path: "join", In: SurfaceHome, Kind: KindText, Localized: true, Doc: "What goes between two clauses of a sentence, and between two plugins' counts, such as \", \"."},
	{Path: "last_join", In: SurfaceHome, Kind: KindText, Localized: true, Doc: "What goes before the last clause of a sentence, and the last plugin's count, such as \" and \"."},
	{Path: "end", In: SurfaceHome, Kind: KindText, Localized: true, Doc: "What ends a sentence, such as \".\"."},
	{Path: "count", In: SurfaceHome, Kind: KindText, Localized: true, Doc: "A counter's words, `{n}` its count and `{noun}` what it counts, such as \"{n} {noun}\"."},
	{Path: "per_sentence", In: SurfaceHome, Kind: KindInt, Default: "3", Doc: "The most clauses a sentence of the briefing holds."},
	{Path: "empty", In: SurfaceHome, Kind: KindText, Localized: true, Doc: "What the briefing says when every counter is zero."},
	{Path: "counters[]", In: SurfaceHome, Kind: KindTable, Doc: "The briefing's counters, in order; one with nothing to count says nothing. A user's list replaces the whole list."},
	{Path: "counters[].id", In: SurfaceHome, Kind: KindText, Doc: "The counter's id; a kind's counter defaults to the kind."},
	{Path: "counters[].kind", In: SurfaceHome, Kind: KindEnum, Values: DecisionKinds, Doc: "The decision kind the counter counts, whose view and words decisions.toml gives; set kind or view."},
	{Path: "counters[].view", In: SurfaceHome, Kind: KindText, Doc: "The view of views.toml whose tasks the counter counts; set kind or view."},
	{Path: "sections[]", In: SurfaceHome, Kind: KindTable, Doc: "The home's sections, in order: the first rows of a view, which a link opens whole. A user's list replaces the whole list."},
	{Path: "sections[].id", In: SurfaceHome, Kind: KindText, Required: true, Doc: "The section's id. Required."},
	{Path: "sections[].view", In: SurfaceHome, Kind: KindText, Required: true, Doc: "The view of views.toml it shows the first rows of; its label titles the section. Required."},
	{Path: "sections[].limit", In: SurfaceHome, Kind: KindInt, Default: "3", Doc: "The most rows it shows."},
	{Path: "sections[].time", In: SurfaceHome, Kind: KindEnum, Values: HomeTimes, Default: "moved", Doc: "The time each row shows."},
	{Path: "capture", In: SurfaceHome, Kind: KindTable, Doc: "The capture field."},
	{Path: "capture.hint", In: SurfaceHome, Kind: KindText, Localized: true, Doc: "What the empty field says."},
}, formsText(SurfaceHome, "counters[].noun", "What one of the counter's tasks is; a kind's counter defaults to the kind's noun."),
	formsText(SurfaceHome, "counters[].clause", "The briefing's clause on the counter, `{counter}` where its count goes; a kind's counter defaults to the kind's clause."))
