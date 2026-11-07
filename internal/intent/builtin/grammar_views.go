package builtin

// viewsGrammar is the keys of views.toml.
var viewsGrammar = []Key{
	{Path: "empty", In: SurfaceViews, Localized: true, Kind: KindTemplate, Required: true, Doc: "What a view without its own empty shows when it lists nothing, over the view's label as `{{.label}}`. Required in the bundled profile; a user layer may replace it."},
	{Path: "default_sort", In: SurfaceViews, Localized: true, Kind: KindText, Required: true, Doc: "What clients call a view's own order in the sort menu, such as \"View order\". Required in the bundled profile; a user layer may replace it."},
	{Path: "lens", In: SurfaceViews, Kind: KindTable, Doc: "What every list's lens bar says. Each is a template whose only actions are the `{{.name}}` its doc lists, which the clients fill in. Required in the bundled profile; a user layer may replace any."},
	{Path: "lens.default_group", In: SurfaceViews, Localized: true, Kind: KindTemplate, Required: true, Doc: "The group menu's entry for the field a view groups by on its own, over the field's label as `{{.label}}`."},
	{Path: "lens.board_group", In: SurfaceViews, Localized: true, Kind: KindTemplate, Required: true, Doc: "The group menu's entry for the field a board groups by when nothing is picked, over the field's label as `{{.label}}`."},
	{Path: "lens.no_group", In: SurfaceViews, Localized: true, Kind: KindText, Required: true, Doc: "The group menu's entry that lists the view in one group."},
	{Path: "lens.copied", In: SurfaceViews, Localized: true, Kind: KindTemplate, Required: true, Doc: "What Copy list says it copied, over `{{.count}}` items and the `{{.view}}` they came from."},
	{Path: "lens.copied_grouped", In: SurfaceViews, Localized: true, Kind: KindTemplate, Required: true, Doc: "What Copy list says it copied from a grouped list, over `{{.count}}`, `{{.view}}` and the `{{.group}}` field's label."},
	{Path: "lens.tag_key", In: SurfaceViews, Localized: true, Kind: KindTemplate, Required: true, Doc: "The name of the field that groups by one key of the tag facet's `key:value` values, over the tag facet's `{{.label}}` and the `{{.key}}`."},
	{Path: "lens.tag_key_empty", In: SurfaceViews, Localized: true, Kind: KindTemplate, Required: true, Doc: "The group of the items with no tag of that key, over the `{{.key}}`."},
	{Path: "lens.grouped", In: SurfaceViews, Localized: true, Kind: KindTemplate, Required: true, Doc: "What the terminal says after a group pick, over the picked entry's `{{.label}}`."},
	{Path: "lens.sorted", In: SurfaceViews, Localized: true, Kind: KindTemplate, Required: true, Doc: "What the terminal says after a sort pick, over the picked entry's `{{.label}}`."},
	{Path: "lens.descending", In: SurfaceViews, Localized: true, Kind: KindTemplate, Required: true, Doc: "The terminal's sort entry for a field in descending order, over the field's `{{.label}}`."},
	{Path: "lens.no_lens", In: SurfaceViews, Localized: true, Kind: KindText, Required: true, Doc: "What the terminal says when a group or sort pick is asked where no list is on screen."},
	{Path: "facets", In: SurfaceViews, Kind: KindMap, Doc: "The task facets, by id: the fields every client filters, groups and sorts tasks by, in the order the file declares them, a user layer's new ones after the bundled. A plugin's row facet of the same id must agree on group, sort, rank, match and buckets."},
	{Path: "facets.*", In: SurfaceViews, Kind: KindTable, Doc: "A facet, by its id. A user layer may set only some keys of an entry declared below it; a new entry sets the required ones."},
	{Path: "facets.*.fact", In: SurfaceViews, Kind: KindFact, Required: true, Doc: "The fact the facet reads: a fact of a task (task.*, task.prop.*), or a field of a record kind, written <kind>.<field> (brief.risk). Required."},
	{Path: "facets.*.label", In: SurfaceViews, Localized: true, Kind: KindText, Required: true, Doc: "The name people read. Required."},
	{Path: "facets.*.group", In: SurfaceViews, Kind: KindBool, Default: "false", Doc: "Whether a list can group by the facet."},
	{Path: "facets.*.sort", In: SurfaceViews, Kind: KindEnum, Values: named("rank", "text", "time"), Doc: "How the facet's values order: by rank, as text, or as instants (time). Absent, as text."},
	{Path: "facets.*.rank", In: SurfaceViews, Kind: KindList, Elem: KindText, Doc: "The facet's values in their order, first first."},
	{Path: "facets.*.values", In: SurfaceViews, Kind: KindMap, Doc: "The text people read for a value, by value."},
	{Path: "facets.*.values.*", In: SurfaceViews, Localized: true, Kind: KindText, Doc: "The text of one value."},
	{Path: "facets.*.label_prefix", In: SurfaceViews, Localized: true, Kind: KindText, Doc: "Text before the label of each value the facet shows on a row, such as the `@` of a context, never in a group's head; a label that has it already keeps it, as a context configured as `@calls` does."},
	{Path: "facets.*.show", In: SurfaceViews, Kind: KindAny, Values: ShowParts, Doc: ShowDoc},
	{Path: "facets.*.show_values", In: SurfaceViews, Kind: KindEnum, Values: ShowValues, Default: "all", Doc: ShowValuesDoc},
	{Path: "facets.*.empty", In: SurfaceViews, Localized: true, Kind: KindText, Doc: "What a group of tasks with no value shows."},
	{Path: "facets.*.match", In: SurfaceViews, Kind: KindEnum, Values: named("exact", "suffix", "prefix", "team"), Default: "exact", Doc: "How a filter's text matches a value: the whole value, its last segment, its first, or a person's team."},
	{Path: "facets.*.menu", In: SurfaceViews, Kind: KindBool, Default: "true", Doc: "Whether the facet is offered in the filter menu."},
	{Path: "facets.*.strip", In: SurfaceViews, Kind: KindBool, Default: "false", Doc: "Whether each row's strip is colored by the facet's first value, its rank the strip's legend."},
	{Path: "facets.*.colors", In: SurfaceViews, Kind: KindMap, Doc: ColorsDoc},
	{Path: "facets.*.colors.*", In: SurfaceViews, Kind: KindEnum, Values: StripColors, Doc: "One value's hue."},
	{Path: "facets.*.buckets", In: SurfaceViews, Kind: KindMap, Doc: "For a facet that sorts by time: the buckets its instants fall in, by id, in the order the file declares them; the first whose within covers the time since the instant wins, and one with no within takes the rest."},
	{Path: "facets.*.buckets.*", In: SurfaceViews, Kind: KindTable, Doc: "A bucket, by its id."},
	{Path: "facets.*.buckets.*.within", In: SurfaceViews, Kind: KindDur, Doc: "How long ago an instant may be and still fall in the bucket."},
	{Path: "facets.*.buckets.*.label", In: SurfaceViews, Localized: true, Kind: KindText, Doc: "The text people read; the id when absent."},
	{Path: "facets.*.merge", In: SurfaceViews, Kind: KindEnum, Values: named("union", "rows-first", "own"), Default: "union", Doc: "How a task's value joins its units' rows' values: union adds it to them, rows-first uses it only when no row has a value, own keeps it alone."},
	{Path: "facets.*.units", In: SurfaceViews, Kind: KindEnum, Values: named("all", "primary"), Default: "all", Doc: "Which of a task's units contribute rows' values: all of them, or the primary unit alone."},
	{Path: "views", In: SurfaceViews, Kind: KindTable, Doc: "The lists of tasks, by view."},
	{Path: "views.*", In: SurfaceViews, Kind: KindTable, Doc: "A view, by its name. A user layer may set only some keys of an entry declared below it; a new entry sets the required ones."},
	{Path: "views.*.label", In: SurfaceViews, Localized: true, Kind: KindText, Required: true, Doc: "The name people read in the list of views. Required."},
	{Path: "views.*.summary", In: SurfaceViews, Localized: true, Kind: KindText, Required: true, Doc: "One line on what the view lists: its tooltip in the list of views, its subtitle, and its key's help line. Required."},
	{Path: "views.*.empty", In: SurfaceViews, Localized: true, Kind: KindText, Doc: "What the view shows when it lists nothing; the top-level empty when absent. A filter that matches nothing says so in its own words."},
	{Path: "views.*.over", In: SurfaceViews, Kind: KindEnum, Values: named("tasks", "gates"), Default: "tasks", Doc: "What the view lists: tasks, or gates."},
	{Path: "views.*.when", In: SurfaceViews, Kind: KindPred, Doc: "Which tasks or gates the view lists, over their facts: a task's, its properties' (task.prop.*), or a gate's. A view with none lists nothing: a place in the clients' list of views that a client draws itself, such as the review."},
	{Path: "views.*.group", In: SurfaceViews, Kind: KindText, Doc: "The fact the view groups by, such as task.owed_by."},
	{Path: "views.*.sort", In: SurfaceViews, Kind: KindText, Doc: "The facts the view sorts by, comma-separated; a leading - sorts one descending. A view's tasks start oldest first, a parent's children in their sibling order."},
	{Path: "views.*.key", In: SurfaceViews, Kind: KindText, Doc: "The key that jumps to the view: `g`, a space and a key that no core key uses, such as `g i`. Its help is the view's label, and its help line the view's summary."},
	{Path: "views.*.hide_disabled", In: SurfaceViews, Kind: KindBool, Default: "false", Doc: "The view hides the tasks whose units are all of a disabled plugin, and the gates on a disabled plugin's units."},
	{Path: "views.*.rail", In: SurfaceViews, Kind: KindBool, Default: "true", Doc: "The rail lists the view; false keeps it for the home, a queue or search."},
	{Path: "views.*.remove_clean", In: SurfaceViews, Kind: KindBool, Default: "false", Doc: "The view offers to remove at once the workspaces its tasks' links hold whose checkouts hold nothing that is not published, as each link's remove action does; the rest are kept, with why."},
	{Path: "badge", In: SurfaceViews, Kind: KindTable, Doc: "The menu bar's badge."},
	{Path: "badge.views", In: SurfaceViews, Kind: KindList, Elem: KindText, Doc: "The views whose counts the badge adds up, each a view of views."},
	{Path: "review", In: SurfaceViews, Kind: KindTable, Doc: "The review's steps, in the order the review walks them."},
	{Path: "review.*", In: SurfaceViews, Kind: KindTable, Doc: "A step of the review, by the view it walks, which views declares. A user layer may set only some keys of an entry declared below it; a new entry sets the required ones, and comes after the steps below it."},
	{Path: "review.*.label", In: SurfaceViews, Localized: true, Kind: KindText, Required: true, Doc: "What the step asks of you, as the review lists it. Required."},
}

// ShowParts are the parts of a row a facet's show may name.
func ShowParts() []Value {
	return []Value{{Name: "", Doc: "Nowhere: the facet is for the lens alone, as when a user layer takes a lower layer's placement off the row."},
		{Name: "ref", Doc: "Beside the title, before the unit's own label: where it lives."},
		{Name: "meta", Doc: "Under the title: who opened it, its contexts, its brief's type, size, risk and tags."},
		{Name: "flag", Doc: "Under the title, after the meta: a state worth a glance, such as paused."},
		{Name: "who", Doc: "Whom it waits on."},
		{Name: "kind", Doc: "Its situation or state, the pill under whom it waits on."},
		{Name: "age", Doc: "How long since the facet's instant, for a facet that sorts by time; only the first such facet shows."},
		{Name: "aside", Doc: "Beside the reason: its workspace and links."}}
}

// StripColors are the hues a strip facet's value may take, the design's colors of whom a row waits on.
func StripColors() []Value {
	return []Value{{Name: "me", Doc: "Yours: the next step is the viewer's."},
		{Name: "author", Doc: "Someone else's, such as a pull request's author."},
		{Name: "reviewer", Doc: "Another person's, apart from the author, such as a reviewer."},
		{Name: "loop", Doc: "The loop's: an agent works on it or is due to."},
		{Name: "nobody", Doc: "Nobody's: a faint strip."}}
}

// ShowValues are how many of an item's values a facet shows.
func ShowValues() []Value {
	return []Value{{Name: "all", Doc: "Every value, in the item's order."}, {Name: "first", Doc: "The first value alone."}}
}

// ShowDoc and ShowValuesDoc are the docs of a facet's show and show_values, in views.toml and in ui.toml.
const (
	ShowDoc       = "The part of a row the facet's values show in, each a button that filters by its value: a part's name, or a table of one part to the values it shows there, such as `{ flag = [\"paused\", \"muted\"] }`, or `\"\"` to take a facet a lower layer places off the row. Absent or `\"\"`, the facet is only for filters, groups and sorts. A value `none` and a time facet's item without a time never show."
	ShowValuesDoc = "How many of an item's values the part shows: all, or the first alone."
	ColorsDoc     = "For the facet that colors the strip: the hue of each value's strip and dot, by value; a value it does not name takes nobody's."
)
