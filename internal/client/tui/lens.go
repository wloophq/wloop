package tui

import (
	"cmp"
	"maps"
	"slices"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/lens"
)

// noGroup is the group pick that lists a view in one group.
const noGroup = "none"

// lensPick is what a person chose for a view on top of the profile's defaults: the field to group by ("" the
// view's own, noGroup none) and the field to sort by ("" the view's own order).
type lensPick struct {
	group string
	sort  string
	desc  bool
}

// lensView is a list the lens applies to: its key in the picks, its items before the filter, the fields a
// query of it names and the field it groups by unless a pick says otherwise.
type lensView struct {
	key          string
	items        []lens.Item
	facets       []api.FacetSpec
	facetByID    map[string]api.FacetSpec
	defaultGroup string
	defaultSort  string
}

// refreshFacets indexes the fields of the views: a task view's are the plugins' and the profile's, as the daemon
// counts them; a row view's are its plugins' alone.
func (m *Model) refreshFacets() {
	m.facets = lens.Merge(m.catalog.Facets, m.viewsCat.Facets)
	m.facetByID = lens.ByID(m.facets)
	m.rowFacetByID = lens.ByID(m.catalog.Facets)
}

// indexRows builds the lens items of the rows, which change with the rows and the kinds' labels.
func (m *Model) indexRows() {
	m.rowLens = make([]lens.Item, len(m.rows))
	for i, r := range m.rows {
		m.rowLens[i] = m.rowItem(r)
	}
}

func taskItem(e api.TaskView) lens.Item { return lens.Item{Facets: e.Facets, Texts: e.Texts()} }

func (m *Model) rowItem(r api.Row) lens.Item {
	texts := []string{r.Title, r.Label, r.Link, string(r.Kind), m.kindLabel(r), r.Summary.WaitsOn, r.Summary.Reason, r.Summary.Line}
	for _, id := range slices.Sorted(maps.Keys(r.Facets)) {
		texts = append(texts, r.Facets[id]...)
	}
	return lens.Item{Facets: r.Facets, Texts: append(texts, r.Search...)}
}

// rowsOf are the rows of row view id and their lens items.
func (m *Model) rowsOf(id string) (rows []api.Row, items []lens.Item) {
	for i, r := range m.rows {
		if slices.Contains(r.Views, id) {
			rows, items = append(rows, r), append(items, m.rowLens[i])
		}
	}
	return rows, items
}

// lensViewOnScreen is the list the lens applies to on screen; ok is false on the plan and the overlays.
func (m *Model) lensViewOnScreen() (lensView, bool) {
	if v, ok := m.viewOnScreen(); ok {
		id := m.listView(v)
		return lensView{key: "task:" + id, items: m.taskItems[id], facets: m.facets, facetByID: m.facetByID, defaultGroup: m.defaultGroup(id),
			defaultSort: m.viewsCat.DefaultSort}, true
	}
	if m.view >= len(m.views) {
		return lensView{}, false
	}
	id := m.views[m.view].ID
	_, items := m.rowsOf(id)
	return m.rowLensView(id, items), true
}

// rowLensView is row view id's list of items.
func (m *Model) rowLensView(id string, items []lens.Item) lensView {
	sort := m.viewsCat.DefaultSort
	if i := slices.IndexFunc(m.catalog.Views, func(v api.ViewSpec) bool { return v.ID == id }); i >= 0 {
		sort = cmp.Or(m.catalog.Views[i].DefaultSort, sort)
	}
	return lensView{key: "row:" + id, items: items, facets: m.catalog.Facets, facetByID: m.rowFacetByID, defaultSort: sort}
}

// defaultGroup is the field a task view groups by unless a pick says otherwise: the facet reading its group
// fact.
func (m *Model) defaultGroup(view string) string {
	fact := m.viewSpec(view).Group
	if fact == "" {
		return ""
	}
	if i := slices.IndexFunc(m.facets, func(f api.FacetSpec) bool { return f.Fact == fact }); i >= 0 {
		return m.facets[i].ID
	}
	return ""
}

// arrange keeps the items the filter holds and puts them in the order the lens says: sorted by the pick, grouped
// by the pick or the view's field. order indexes items; heads is the label of the group each position opens, ""
// inside a group.
func (m *Model) arrange(v lensView, items []lens.Item) (order []int, heads []string) {
	specs := v.facetByID
	query := lens.Parse(m.FilterQuery(), specs)
	var idx []int
	var kept []lens.Item
	for i, it := range items {
		if query.Match(it, specs, m.now) {
			idx, kept = append(idx, i), append(kept, it)
		}
	}
	pick := m.picks[v.key]
	if spec, ok := specs[pick.sort]; ok {
		sorted := lens.SortBy(kept, spec, pick.desc, m.now)
		idx, kept = reorder(idx, sorted), reorder(kept, sorted)
	}
	field := cmp.Or(pick.group, v.defaultGroup)
	spec, keys, ok := lens.GroupField(field, specs, m.viewsCat.Lens)
	if field == noGroup || !ok {
		return idx, make([]string, len(idx))
	}
	var groups []lens.Group
	if keys != nil {
		groups = lens.GroupByKeys(kept, spec, keys)
	} else {
		groups = lens.GroupBy(kept, spec, m.now)
	}
	for _, g := range groups {
		head := g.Label
		for _, j := range g.Items {
			order, heads = append(order, idx[j]), append(heads, head)
			head = ""
		}
	}
	return order, heads
}

func reorder[T any](s []T, order []int) []T {
	out := make([]T, len(order))
	for i, j := range order {
		out[i] = s[j]
	}
	return out
}

// menuFields are the fields the lens menus of v offer: those some item of the view holds a value of.
func (m *Model) menuFields(v lensView) []api.FacetSpec {
	return lens.Offered(v.items, v.facets, m.viewsCat.Lens)
}

// cycleGroup moves the group pick of the view on screen to the next of: the view's own, the other fields the
// view offers, none.
func (m *Model) cycleGroup() {
	v, ok := m.lensViewOnScreen()
	if !ok {
		m.status = "nothing to group here"
		return
	}
	var steps []string
	if v.defaultGroup != "" {
		steps = append(steps, "")
	}
	fields := m.menuFields(v)
	for _, f := range fields {
		if f.Group && f.Menu && f.ID != v.defaultGroup {
			steps = append(steps, f.ID)
		}
	}
	if v.defaultGroup == "" {
		steps = append(steps, "")
	} else {
		steps = append(steps, noGroup)
	}
	pick := m.picks[v.key]
	next := steps[(slices.Index(steps, pick.group)+1)%len(steps)]
	m.setPick(v.key, func(p *lensPick) { p.group = next })
	byID := lens.ByID(lens.Merge(v.facets, fields))
	var text string
	switch {
	case next == "" && v.defaultGroup == "", next == noGroup:
		text = m.viewsCat.Lens.NoGroup
	case next == "":
		text = lensWord(m.viewsCat.Lens.DefaultGroup, byID[v.defaultGroup].Label)
	default:
		text = byID[next].Label
	}
	m.status = "group: " + cmp.Or(text, "none")
	m.keepSelections()
}

// cycleSort moves the sort pick of the view on screen through the view's own order, then each sortable field
// the view offers, ascending and descending.
func (m *Model) cycleSort() {
	v, ok := m.lensViewOnScreen()
	if !ok {
		m.status = "nothing to sort here"
		return
	}
	type step struct {
		field string
		desc  bool
	}
	steps := []step{{}}
	labels := map[string]string{}
	for _, f := range m.menuFields(v) {
		if f.Sort != "" && f.Menu {
			steps = append(steps, step{f.ID, false}, step{f.ID, true})
			labels[f.ID] = f.Label
		}
	}
	pick := m.picks[v.key]
	next := steps[(slices.Index(steps, step{pick.sort, pick.desc})+1)%len(steps)]
	m.setPick(v.key, func(p *lensPick) { p.sort, p.desc = next.field, next.desc })
	text := cmp.Or(v.defaultSort, "view order")
	if next.field != "" {
		text = labels[next.field]
		if next.desc {
			text += " (descending)"
		}
	}
	m.status = "sort: " + text
	m.keepSelections()
}

func (m *Model) setPick(key string, change func(*lensPick)) {
	if m.picks == nil {
		m.picks = map[string]lensPick{}
	}
	p := m.picks[key]
	change(&p)
	m.picks[key] = p
}

func (m *Model) keepSelections() {
	m.keepSelection()
	m.keepTaskSelection()
}

func lensWord(template, label string) string {
	return lens.Word(template, map[string]string{"label": label})
}
