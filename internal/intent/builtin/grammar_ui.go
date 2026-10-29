package builtin

// uiGrammar is the keys of ui.toml, the words of a source plugin's views,
// facets, situations and menu bar sections, by the ids its manifest's
// [source.ui] and [[source.situations]] declare.
var uiGrammar = []Key{
	{Path: "views", In: SurfaceUI, Kind: KindTable, Doc: "The words of the plugin's views, by view id."},
	{Path: "views.*", In: SurfaceUI, Kind: KindTable, Doc: "One view's words."},
	{Path: "views.*.label", In: SurfaceUI, Kind: KindText, Required: true, Doc: "The view's name, as navigation lists it. Required."},
	{Path: "views.*.unit", In: SurfaceUI, Kind: KindText, Doc: "What one of the view's rows is, such as \"Pull request\", the unit column's heading; empty for a view whose rows mix kinds."},
	{Path: "facets", In: SurfaceUI, Kind: KindTable, Doc: "The words of the plugin's facets, by facet id."},
	{Path: "facets.*", In: SurfaceUI, Kind: KindTable, Doc: "One facet's words."},
	{Path: "facets.*.label", In: SurfaceUI, Kind: KindText, Required: true, Doc: "The facet's name, as the filter menu and the groups show it. Required."},
	{Path: "facets.*.empty", In: SurfaceUI, Kind: KindText, Doc: "What a row with no value shows."},
	{Path: "facets.*.values", In: SurfaceUI, Kind: KindTable, Doc: "How each value shows, by value; a value it does not name shows as itself."},
	{Path: "facets.*.values.*", In: SurfaceUI, Kind: KindText, Doc: "One value's text."},
	{Path: "facets.*.buckets", In: SurfaceUI, Kind: KindTable, Doc: "How each age bucket of a time facet shows, by bucket id."},
	{Path: "facets.*.buckets.*", In: SurfaceUI, Kind: KindText, Doc: "One bucket's text."},
	{Path: "situations", In: SurfaceUI, Kind: KindTable, Doc: "How each situation the plugin derives shows, by kind, in a few words."},
	{Path: "situations.*", In: SurfaceUI, Kind: KindText, Doc: "One situation's words."},
	{Path: "events", In: SurfaceUI, Kind: KindTable, Doc: "How each event kind the plugin declares shows on a timeline, by kind."},
	{Path: "events.*", In: SurfaceUI, Kind: KindTable, Doc: "One event kind's words."},
	{Path: "events.*.label", In: SurfaceUI, Kind: KindText, Required: true, Doc: "The event in a few words. Required."},
	{Path: "events.*.loop", In: SurfaceUI, Kind: KindBool, Doc: "The event is the loop's doing, which the activity's loop filter keeps."},
	{Path: "menubar", In: SurfaceUI, Kind: KindTable, Doc: "The heading of each menu bar section, by section id."},
	{Path: "menubar.*", In: SurfaceUI, Kind: KindText, Doc: "One section's heading."},
}
