package intent

import "github.com/wloophq/wloop/internal/intent/builtin"

// RowWords are the words of a row beside its facets, by builtin.RowWords' keys.
type RowWords struct {
	AgeNow           string
	AgeMinutes       string
	AgeHours         string
	AgeDays          string
	FilterLabel      string
	FilterTitle      string
	FilterTitleGroup string
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
// the values the part shows, all when empty; Values "all" or "first".
type Show struct {
	Part   string
	Only   []string
	Values string
}

// readShow reads a facet's show at path, a part's name or a table of one part to its values, and its
// show_values, over sh.
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
			s.oneOf(path+".show."+part, part, parts)
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
