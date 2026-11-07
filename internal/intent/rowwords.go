package intent

import (
	"maps"
	"slices"

	"github.com/wloophq/wloop/internal/intent/builtin"
)

// RowWords are the words of a row beside its facets, by builtin.RowWords' keys.
type RowWords struct {
	AgeNow           string
	AgeMinutes       string
	AgeHours         string
	AgeDays          string
	FilterLabel      string
	FilterTitle      string
	FilterTitleGroup string
	FilterGroup      string
	ListCount        string
	ListEmpty        string
	CountdownTitle   string
	CountdownSending string
	CountdownSeconds string
	CountdownMinutes string
	TipNamed         string
	TipKind          string
	TipWhy           string
	LegendTitle      string
	SortBy           string
	ColUnit          string
	ColWho           string
	ColAge           string
	ColWhy           string
	ColAside         string
	ColActions       string
	Stale            string
	StaleTitle       string
	Briefing         string
	BriefingTitle    string
	BriefingAttempt  string
	BriefingError    string
	Questions        string
	QuestionsTitle   string
	Changed          string
	Starred          string
	Until            string
	Due              string
	Unread           string
	UnreadTitle      string
	ReviewDue        string
	NoNextAction     string
	OpenChildren     string
	Sequential       string
	Every            string
	Reviewed         string
	More             string
}

// field is the word of key; nil for a key builtin.RowWords does not name.
func (w *RowWords) field(key string) *string {
	switch key {
	case "age_now":
		return &w.AgeNow
	case "age_minutes":
		return &w.AgeMinutes
	case "age_hours":
		return &w.AgeHours
	case "age_days":
		return &w.AgeDays
	case "filter_label":
		return &w.FilterLabel
	case "filter_title":
		return &w.FilterTitle
	case "filter_title_group":
		return &w.FilterTitleGroup
	case "filter_group":
		return &w.FilterGroup
	case "list_count":
		return &w.ListCount
	case "list_empty":
		return &w.ListEmpty
	case "countdown_title":
		return &w.CountdownTitle
	case "countdown_sending":
		return &w.CountdownSending
	case "countdown_seconds":
		return &w.CountdownSeconds
	case "countdown_minutes":
		return &w.CountdownMinutes
	case "tip_named":
		return &w.TipNamed
	case "tip_kind":
		return &w.TipKind
	case "tip_why":
		return &w.TipWhy
	case "legend_title":
		return &w.LegendTitle
	case "sort_by":
		return &w.SortBy
	case "col_unit":
		return &w.ColUnit
	case "col_who":
		return &w.ColWho
	case "col_age":
		return &w.ColAge
	case "col_why":
		return &w.ColWhy
	case "col_aside":
		return &w.ColAside
	case "col_actions":
		return &w.ColActions
	case "stale":
		return &w.Stale
	case "stale_title":
		return &w.StaleTitle
	case "briefing":
		return &w.Briefing
	case "briefing_title":
		return &w.BriefingTitle
	case "briefing_attempt":
		return &w.BriefingAttempt
	case "briefing_error":
		return &w.BriefingError
	case "questions":
		return &w.Questions
	case "questions_title":
		return &w.QuestionsTitle
	case "changed":
		return &w.Changed
	case "starred":
		return &w.Starred
	case "until":
		return &w.Until
	case "due":
		return &w.Due
	case "unread":
		return &w.Unread
	case "unread_title":
		return &w.UnreadTitle
	case "review_due":
		return &w.ReviewDue
	case "no_next_action":
		return &w.NoNextAction
	case "open_children":
		return &w.OpenChildren
	case "sequential":
		return &w.Sequential
	case "every":
		return &w.Every
	case "reviewed":
		return &w.Reviewed
	case "more":
		return &w.More
	}
	return nil
}

// mergeRowWords lays the row words of a [lens] table at path over w, each checked for the actions it may
// hold.
func mergeRowWords(s *sheet, path string, m map[string]string, w *RowWords) {
	for _, rw := range builtin.RowWords {
		v, ok := m[rw.Key]
		if dst := w.field(rw.Key); ok && dst != nil {
			word(s, path+"."+rw.Key, &v, dst, rw.Vars...)
		}
	}
}

// Over is w with the words of over that are set.
func (w RowWords) Over(over RowWords) RowWords {
	for _, rw := range builtin.RowWords {
		if v := *over.field(rw.Key); v != "" {
			*w.field(rw.Key) = v
		}
	}
	return w
}

// Show is where a facet's values show on a row: Part, one of builtin.ShowParts, "" for the lens alone; Only
// the values the part shows, all when empty; Values "all" or "first"; When the values, by facet id, a row
// must hold for the facet to show; Max how many values show before the rest count into one; Label an age
// part's label over {{.age}} and {{.at}}.
type Show struct {
	Part   string
	Only   []string
	Values string
	When   map[string][]string
	Max    int
	Label  string
}

// placement is a facet's show_when, show_max and show_label as a file writes them.
type placement struct {
	When  map[string][]string `toml:"show_when"`
	Max   *int                `toml:"show_max"`
	Label *string             `toml:"show_label"`
}

// readPlacement lays p, the placement keys of the facet at path, over sh; whether each show_when facet is
// declared is checked once every layer is read.
func readPlacement(s *sheet, path string, p placement, sh *Show) {
	if p.When != nil {
		n := len(s.problems)
		for _, facet := range slices.Sorted(maps.Keys(p.When)) {
			if len(p.When[facet]) == 0 || slices.Contains(p.When[facet], "") {
				s.add(path+".show_when."+facet, "must be a list of values")
			}
		}
		if len(s.problems) == n {
			sh.When = maps.Clone(p.When)
		}
	}
	if p.Max != nil {
		if *p.Max < 0 {
			s.add(path+".show_max", "must be 0 or more")
		} else {
			sh.Max = *p.Max
		}
	}
	word(s, path+".show_label", p.Label, &sh.Label, "age", "at")
}

// over lays the placement keys o sets over sh.
func (sh Show) over(o placement, from Show) Show {
	if o.When != nil {
		sh.When = from.When
	}
	if o.Max != nil {
		sh.Max = from.Max
	}
	if o.Label != nil {
		sh.Label = from.Label
	}
	return sh
}

// readShow reads a facet's show at path, a part's name, "" for the lens alone, or a table of one part to its
// values, and its show_values, over sh.
func readShow(s *sheet, path string, v any, values *string, sh *Show) {
	parts := builtin.ShowParts()
	switch x := v.(type) {
	case nil:
	case string:
		n := len(s.problems)
		s.oneOf(path+".show", x, parts)
		if len(s.problems) == n {
			sh.Part, sh.Only = x, nil
		}
	case map[string]any:
		if len(x) != 1 {
			s.add(path+".show", "names one part, not %d", len(x))
			break
		}
		for part, vs := range x {
			n := len(s.problems)
			if part == "" {
				s.add(path+".show", "names no part; a table names the part its values show in")
			} else {
				s.oneOf(path+".show."+part, part, parts)
			}
			list, ok := vs.([]any)
			if !ok {
				s.add(path+".show."+part, "must be a list of values")
			}
			var only []string
			for _, e := range list {
				if str, ok := e.(string); ok && str != "" {
					only = append(only, str)
				} else {
					s.add(path+".show."+part, "must be a list of values")
				}
			}
			if len(s.problems) == n {
				sh.Part, sh.Only = part, only
			}
		}
	default:
		s.add(path+".show", "must be a part or a table of a part to its values")
	}
	if values != nil {
		n := len(s.problems)
		s.oneOf(path+".show_values", *values, builtin.ShowValues())
		if len(s.problems) == n {
			sh.Values = *values
		}
	}
}

// readColors says whether every hue of a facet's colors at path is one of builtin.StripColors.
func readColors(s *sheet, path string, colors map[string]string) bool {
	n := len(s.problems)
	for _, v := range slices.Sorted(maps.Keys(colors)) {
		s.oneOf(path+".colors."+v, colors[v], builtin.StripColors())
	}
	return len(s.problems) == n
}

// Set are the words w sets, by key.
func (w RowWords) Set() map[string]string {
	out := map[string]string{}
	for _, rw := range builtin.RowWords {
		if v := *w.field(rw.Key); v != "" {
			out[rw.Key] = v
		}
	}
	return out
}
