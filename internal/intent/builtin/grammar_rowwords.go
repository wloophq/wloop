package builtin

// RowWord is a word of a row in views.toml's [lens], which a plugin's ui.toml may replace for its rows:
// its key, the {{.name}} actions it may hold and what it is.
type RowWord struct {
	Key  string
	Vars []string
	Doc  string
}

// RowWords are the words of a row, in the order the reference lists them.
var RowWords = []RowWord{
	{Key: "age_now", Vars: []string{}, Doc: "A row's age under a minute."},
	{Key: "age_minutes", Vars: []string{"m"}, Doc: "A row's age under an hour, over its minutes as `{{.m}}`."},
	{Key: "age_hours", Vars: []string{"h", "m"}, Doc: "A row's age under a day, over its hours `{{.h}}` and the minutes past them `{{.m}}`."},
	{Key: "age_days", Vars: []string{"d", "h"}, Doc: "A row's age from a day, over its days `{{.d}}` and the hours past them `{{.h}}`."},
	{Key: "filter_label", Vars: []string{"label", "value"}, Doc: "What a facet button on a row says to a screen reader, over the facet's `{{.label}}` and the `{{.value}}`."},
	{Key: "filter_title", Vars: []string{"label", "value"}, Doc: "A facet button's tooltip, over the facet's `{{.label}}` and the `{{.value}}`."},
	{Key: "filter_title_group", Vars: []string{"label", "value", "group"}, Doc: "A facet button's tooltip when its facet groups, over `{{.label}}`, `{{.value}}` and the `{{.group}}` field's label."},
	{Key: "filter_group", Vars: []string{}, Doc: "What a facet button calls whom a row waits on when the value is a group of people, such as a Slack user group, in place of the facet's label."},
	{Key: "list_count", Vars: []string{"unit", "shown", "total"}, Doc: "A row view's summary, over its `{{.unit}}`, how many rows the filter `{{.shown}}` and how many the view holds, `{{.total}}`."},
	{Key: "list_empty", Vars: []string{}, Doc: "What a plugin view's list says when it holds no row and the plugin gives no words of its own."},
	{Key: "countdown_title", Vars: []string{"time"}, Doc: "The tooltip of a pending round's countdown, over the `{{.time}}` it sends at."},
	{Key: "countdown_sending", Vars: []string{}, Doc: "A pending round's countdown once its deadline passed."},
	{Key: "countdown_seconds", Vars: []string{"s"}, Doc: "A pending round's countdown under 100 seconds, over the seconds `{{.s}}`."},
	{Key: "countdown_minutes", Vars: []string{"m", "s"}, Doc: "A pending round's countdown from 100 seconds, over its minutes `{{.m}}` and the seconds past them, two digits, `{{.s}}`."},
	{Key: "tip_named", Vars: []string{"strip", "who"}, Doc: "A row's tooltip's start when it names whom the row waits on, over the `{{.strip}}` value's label and `{{.who}}`."},
	{Key: "tip_kind", Vars: []string{"tip", "kind"}, Doc: "A row's tooltip with its situation, over the `{{.tip}}` so far and the `{{.kind}}` labels."},
	{Key: "tip_why", Vars: []string{"tip", "why"}, Doc: "A row's tooltip with why it waits, over the `{{.tip}}` so far and the `{{.why}}`."},
	{Key: "legend_title", Vars: []string{}, Doc: "The tooltip of the legend of a view's strip, the colors of its strip facet's values."},
	{Key: "sort_by", Vars: []string{"label"}, Doc: "The tooltip of the list toolbar's Sort chip, over the `{{.label}}` of the facet it sorts by."},
	{Key: "col_unit", Vars: []string{}, Doc: "What a row view whose rows mix kinds calls its unit, as `list_count`'s `{{.unit}}`."},
	{Key: "stale", Vars: []string{}, Doc: "The flag of a row whose plugin is not reporting."},
	{Key: "stale_title", Vars: []string{}, Doc: "The stale flag's tooltip."},
	{Key: "briefing", Vars: []string{}, Doc: "The flag of a row whose intake waits for its brief."},
	{Key: "briefing_title", Vars: []string{"seq"}, Doc: "The briefing flag's tooltip, over the intake's `{{.seq}}`."},
	{Key: "briefing_attempt", Vars: []string{"n"}, Doc: "What the briefing flag's tooltip adds of a retried intake, over the attempt `{{.n}}`."},
	{Key: "briefing_error", Vars: []string{"error"}, Doc: "What the briefing flag's tooltip adds of an intake's last error, over the `{{.error}}`."},
	{Key: "questions", Vars: []string{"n"}, Doc: "The pill of a brief's open questions, over their number `{{.n}}`."},
	{Key: "questions_title", Vars: []string{"n"}, Doc: "That pill's tooltip, over `{{.n}}`."},
	{Key: "starred", Vars: []string{}, Doc: "The tooltip of a starred task's star."},
	{Key: "until", Vars: []string{"date"}, Doc: "The flag of a task asleep until a date, over the `{{.date}}`."},
	{Key: "due", Vars: []string{"date"}, Doc: "The flag of a task's deadline, over the `{{.date}}`."},
	{Key: "unread", Vars: []string{"lag"}, Doc: "The flag of a task whose links hold messages the extraction has not read, over how long the oldest waited, `{{.lag}}`."},
	{Key: "unread_title", Vars: []string{}, Doc: "The unread flag's tooltip."},
	{Key: "review_due", Vars: []string{}, Doc: "The flag of a project past its review."},
	{Key: "no_next_action", Vars: []string{}, Doc: "The flag of a project with no open child."},
	{Key: "open_children", Vars: []string{"n"}, Doc: "A project's open children, over their number `{{.n}}`."},
	{Key: "sequential", Vars: []string{}, Doc: "The flag of a project that takes its children one at a time."},
	{Key: "every", Vars: []string{"every"}, Doc: "How often a project is reviewed, over the `{{.every}}`."},
	{Key: "reviewed", Vars: []string{"date"}, Doc: "When a project was last reviewed, over the `{{.date}}`."},
	{Key: "more", Vars: []string{"n"}, Doc: "The piece that counts the values a facet's `show_max` leaves off a row, over their number `{{.n}}`."},
}

func rowWordRows(in Surface, required bool) []Key {
	out := make([]Key, len(RowWords))
	for i, w := range RowWords {
		out[i] = Key{Path: "lens." + w.Key, In: in, Kind: KindTemplate, Required: required, Localized: true, Doc: w.Doc}
	}
	return out
}

// showRows are the keys of a facet's show written as a table, one per part.
func showRows(in Surface) []Key {
	var out []Key
	for _, p := range ShowParts() {
		if p.Name == "" {
			continue
		}
		out = append(out, Key{Path: "facets.*.show." + p.Name, In: in, Kind: KindList, Elem: KindText, Doc: "The values the facet shows in the " + p.Name + " part."})
	}
	return out
}
