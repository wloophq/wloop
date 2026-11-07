package builtin

// SurfaceOmnibox is omnibox.toml: how the omnibox groups what it finds, and the verbs it knows.
const SurfaceOmnibox Surface = "omnibox"

var omniboxGrammar = []Key{
	{Path: "search", In: SurfaceOmnibox, Kind: KindTable, Doc: "How a search groups the tasks it finds."},
	{Path: "search.limit", In: SurfaceOmnibox, Kind: KindInt, Default: "5", Doc: "The most results a group shows; its count counts them all."},
	{Path: "search.other", In: SurfaceOmnibox, Kind: KindText, Localized: true, Doc: "The label of the last group, which holds what no group's view holds."},
	{Path: "search.groups[]", In: SurfaceOmnibox, Kind: KindTable, Doc: "A group, in order: a result goes to the first group whose view holds it. A user's list replaces the whole list."},
	{Path: "search.groups[].id", In: SurfaceOmnibox, Kind: KindText, Required: true, Doc: "The group's id. Required."},
	{Path: "search.groups[].label", In: SurfaceOmnibox, Kind: KindText, Localized: true, Required: true, Doc: "The group's label. Required."},
	{Path: "search.groups[].view", In: SurfaceOmnibox, Kind: KindText, Required: true, Doc: "The view of views.toml whose tasks the group holds. Required."},
}
