package builtin

// viewsGrammar is the keys of views.toml.
var viewsGrammar = []Key{
	{Path: "views", In: SurfaceViews, Kind: KindTable, Doc: "The lists of tasks, by view."},
	{Path: "views.*", In: SurfaceViews, Kind: KindTable, Doc: "A view, by its name. A user layer may set only some keys of an entry declared below it; a new entry sets the required ones."},
	{Path: "views.*.label", In: SurfaceViews, Kind: KindText, Required: true, Doc: "The name people read in the list of views. Required."},
	{Path: "views.*.over", In: SurfaceViews, Kind: KindEnum, Values: named("tasks", "gates"), Default: "tasks", Doc: "What the view lists: tasks, or gates."},
	{Path: "views.*.when", In: SurfaceViews, Kind: KindPred, Doc: "Which tasks or gates the view lists, over their facts: a task's, its properties' (task.prop.*), or a gate's. A view with none lists nothing: a place in the clients' list of views that a client draws itself, such as the review."},
	{Path: "views.*.group", In: SurfaceViews, Kind: KindText, Doc: "The fact the view groups by, such as task.owed_by."},
	{Path: "views.*.sort", In: SurfaceViews, Kind: KindText, Doc: "The facts the view sorts by, comma-separated; a leading - sorts one descending. A view's tasks start oldest first, a parent's children in their sibling order."},
	{Path: "views.*.key", In: SurfaceViews, Kind: KindText, Doc: "The key that jumps to the view: `g`, a space and a key that no core key uses, such as `g i`. Its help is the view's label."},
	{Path: "views.*.hide_disabled", In: SurfaceViews, Kind: KindBool, Default: "false", Doc: "The view hides the tasks whose units are all of a disabled plugin, and the gates on a disabled plugin's units."},
	{Path: "badge", In: SurfaceViews, Kind: KindTable, Doc: "The menu bar's badge."},
	{Path: "badge.views", In: SurfaceViews, Kind: KindList, Elem: KindText, Doc: "The views whose counts the badge adds up, each a view of views."},
	{Path: "review", In: SurfaceViews, Kind: KindTable, Doc: "The review's steps, in the order the review walks them."},
	{Path: "review.*", In: SurfaceViews, Kind: KindTable, Doc: "A step of the review, by the view it walks, which views declares. A user layer may set only some keys of an entry declared below it; a new entry sets the required ones, and comes after the steps below it."},
	{Path: "review.*.label", In: SurfaceViews, Kind: KindText, Required: true, Doc: "What the step asks of you, as the review lists it. Required."},
}
