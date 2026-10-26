package tui

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/termfmt"
)

// The key contexts of #keys.
const (
	ctxGlobal   = "global"
	ctxList     = "list"
	ctxQuestion = "question"
	ctxPlan     = "plan"
)

// shiftDigits are what Shift+1..9 type on a US layout.
const shiftDigits = "!@#$%^&*("

// coreActions are the served core actions that are not GTD verbs.
var coreActions = []string{"move:", "detail:", "nav:", "area:", "answer:", "step:", "palette", "help", "filter", "open-source", "capture", "quit"}

// verbAsks are the verbs that ask for a value on the bottom line first;
// args turns the answer into the verb's arguments, ok false when the
// answer is not usable.
var verbAsks = map[string]struct {
	label string
	args  func(string) (map[string]string, bool)
}{
	"waiting": {"waiting on whom (optional)", func(v string) (map[string]string, bool) {
		if v == "" {
			return nil, true
		}
		return map[string]string{"on": v}, true
	}},
	"schedule": {"until when (YYYY-MM-DD, an RFC 3339 time or activity)", func(v string) (map[string]string, bool) {
		switch {
		case v == "":
			return nil, false
		case strings.EqualFold(v, "activity"):
			return map[string]string{"on_activity": "true"}, true
		}
		return map[string]string{"until": v}, true
	}},
	"project": {"project title", func(v string) (map[string]string, bool) {
		if v == "" {
			return nil, false
		}
		return map[string]string{"title": v}, true
	}},
}

// SetKeys replaces the bindings of the terminal the keys follow.
func (m *Model) SetKeys(b []api.KeyView) { m.keys = b }

// bindingName is the name the bindings give k: a named key, Shift+n for
// the symbol a US layout types for Shift and a digit, else the rune.
func bindingName(k Key) string {
	if k.Name != "" {
		return namedKeys[k.Name]
	}
	if i := strings.IndexRune(shiftDigits, k.Rune); i >= 0 {
		return "Shift+" + strconv.Itoa(i+1)
	}
	return string(k.Rune)
}

var namedKeys = map[string]string{KeyEnter: "Enter", KeyEsc: "Esc", KeyBackspace: "Backspace", KeyTab: "Tab", KeyCtrlK: "Ctrl+K"}

// served tells a binding of a served action from a core one: one
// uppercase letter, or a plan action that is neither a core action nor a
// verb.
func served(b api.KeyView) bool {
	if len(b.Key) == 1 && b.Key[0] >= 'A' && b.Key[0] <= 'Z' {
		return true
	}
	return b.Context == ctxPlan && !coreAction(b.Action) && !isVerb(b.Action)
}

func coreAction(action string) bool {
	return slices.ContainsFunc(coreActions, func(a string) bool {
		return action == a || (strings.HasSuffix(a, ":") && strings.HasPrefix(action, a))
	})
}

func isVerb(action string) bool {
	return slices.Contains([]string{"next", "waiting", "schedule", "someday", "reference", "focus", "project", "approve", "delegate", "done", "dismiss", "reply", "edit"}, action)
}

// binding is the first binding of action in context.
func (m *Model) binding(action, context string) (api.KeyView, bool) {
	i := slices.IndexFunc(m.keys, func(b api.KeyView) bool { return b.Action == action && b.Context == context && !served(b) })
	if i < 0 {
		return api.KeyView{}, false
	}
	return m.keys[i], true
}

// prefix tells whether name starts a chord.
func (m *Model) prefix(name string) bool {
	return slices.ContainsFunc(m.keys, func(b api.KeyView) bool { return strings.HasPrefix(b.Key, name+" ") })
}

// dispatch runs the bindings of k on the view on screen, the most specific
// context first.
func (m *Model) dispatch(k Key) (Command, bool) {
	name := bindingName(k)
	if m.chord != "" {
		full := m.chord + " " + name
		m.chord = ""
		m.status = ""
		i := slices.IndexFunc(m.keys, func(b api.KeyView) bool { return b.Key == full })
		if name == "" {
			i = -1
			full = strings.TrimSpace(full)
		}
		if i < 0 {
			m.status = full + ": no such key"
			return Command{}, false
		}
		return m.run(m.keys[i], k)
	}
	if name == "" {
		return Command{}, false
	}
	contexts := []string{ctxList, ctxGlobal}
	if _, ok := m.activeCard(); ok && k.Rune >= '1' && k.Rune <= '9' {
		contexts = slices.Insert(contexts, 0, ctxQuestion)
	}
	if m.view == m.planView() {
		contexts = slices.Insert(contexts, 0, ctxPlan)
	}
	if m.inReview() {
		contexts = slices.Insert(contexts, 0, ctxReview)
	}
	var found []api.KeyView
	for _, b := range m.keys {
		if b.Key == name && slices.Contains(contexts, b.Context) {
			found = append(found, b)
		}
	}
	slices.SortStableFunc(found, func(a, b api.KeyView) int {
		return cmp.Or(cmp.Compare(slices.Index(contexts, a.Context), slices.Index(contexts, b.Context)), boolRank(served(b))-boolRank(served(a)))
	})
	if len(found) == 0 && m.prefix(name) {
		m.chord = name
		m.status = name + " …"
		return Command{}, false
	}
	for _, b := range found {
		if c, ok, took := m.runBinding(b, k); took {
			return c, ok
		}
	}
	return Command{}, false
}

func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (m *Model) run(b api.KeyView, k Key) (Command, bool) {
	c, ok, _ := m.runBinding(b, k)
	return c, ok
}

// runBinding runs b; took is false when b does not apply here, so the next
// candidate may.
func (m *Model) runBinding(b api.KeyView, k Key) (c Command, ok, took bool) {
	a := b.Action
	_, inGTD := m.gtdView()
	switch {
	case b.Context == ctxReview:
		return m.reviewAction(a)
	case b.Context == ctxPlan:
		if m.view != m.planView() {
			return Command{}, false, false
		}
		c, ok = m.planAction(Key{Rune: k.Rune, Name: k.Name, Read: k.Read})
		return c, ok, true
	case served(b):
		if b.Context != ctxList {
			return Command{}, false, false
		}
		if inGTD {
			c, ok = m.entryUnitAction(k)
			return c, ok, true
		}
		if m.view < m.planView() && k.Rune != 0 {
			c, ok = m.action(k)
			return c, ok, true
		}
		return Command{}, false, false
	case a == "move:down":
		m.move(1)
	case a == "move:up":
		m.move(-1)
	case a == "detail:open":
		if !inGTD {
			return Command{}, false, false
		}
		if e, ok := m.selectedEntry(); ok && !m.detailOpen(e) {
			c, ok := m.openEntry(e, k.Read)
			return c, ok, true
		}
	case a == "detail:close":
		if m.detailID == "" {
			return Command{}, false, false
		}
		m.closeDetail()
	case strings.HasPrefix(a, "nav:"):
		m.navigate(strings.TrimPrefix(a, "nav:"))
	case strings.HasPrefix(a, "area:"):
		n, _ := strconv.Atoi(strings.TrimPrefix(a, "area:"))
		m.pickArea(n)
	case strings.HasPrefix(a, "answer:"):
		q, ok := m.activeCard()
		if !ok {
			return Command{}, false, false
		}
		n, _ := strconv.Atoi(strings.TrimPrefix(a, "answer:"))
		c, ok = m.answer(q, n, k.Read)
		return c, ok, true
	case a == "palette":
		m.openPalette()
	case a == "help":
		m.openHelp()
	case a == "filter":
		m.startFilter()
	case a == "quit":
		m.quit = true
	case a == "capture":
		m.prompt = &prompt{action: "capture", label: "capture", read: k.Read, submit: func(text string) (Command, bool) {
			if text == "" {
				m.status = "capture cancelled: nothing typed"
				return Command{}, false
			}
			return m.send(Command{Unit: entryUnit("capture"), Label: termfmt.Cut(text, 48), Action: "capture", capture: text})
		}}
	case a == "open-source":
		c, ok = m.openSource()
		return c, ok, true
	case isVerb(a):
		if !inGTD {
			return Command{}, false, false
		}
		c, ok = m.entryVerb(a, k.Read)
		return c, ok, true
	default:
		return Command{}, false, false
	}
	return Command{}, false, true
}

// navigate shows a GTD view, or the first view of a plugin named by its
// provider id.
func (m *Model) navigate(to string) {
	if to == "cycle" {
		m.cycle()
		return
	}
	if i := slices.IndexFunc(m.gtd, func(v View) bool { return v.ID == to }); i >= 0 {
		m.setView(m.gtdBase() + i)
		return
	}
	if i := slices.IndexFunc(m.views, func(v View) bool { return v.Provider == to }); i >= 0 {
		m.setView(i)
		return
	}
	if p := slices.IndexFunc(m.served, func(p api.Plugin) bool { return string(p.ID) == to }); p >= 0 {
		m.status = "no " + cmp.Or(m.served[p].Name, to) + " views: the plugin is disabled or not loaded"
		return
	}
	m.status = "no " + to + " view here"
}

// pickArea makes the lists follow area n of the served areas.
func (m *Model) pickArea(n int) {
	if n < 1 || n > len(m.gtdCat.Areas) {
		m.status = fmt.Sprintf("no area %d", n)
		return
	}
	m.setArea(m.gtdCat.Areas[n-1])
}

func (m *Model) setArea(a string) {
	m.area = a
	m.counters, m.filtered = api.Counters{}, nil
	m.status = "area: " + cmp.Or(a, "every area")
}

func (m *Model) startFilter() {
	if len(m.filter) == 0 {
		m.unfiltered = m.sel
	}
	m.filtering = true
}

// entryVerb runs verb on the selected entry, asking first for the value
// the verb needs.
func (m *Model) entryVerb(verb string, read uint64) (Command, bool) {
	e, ok := m.selectedEntry()
	if !ok {
		return Command{}, false
	}
	if d, ok := m.pickedDraft(); ok && m.detailOpen(e) && slices.Contains([]string{"approve", "dismiss", "retry", "edit"}, verb) {
		return m.pickedVerb(e, d, verb, read)
	}
	if i := slices.IndexFunc(e.Verbs, func(v api.VerbState) bool { return v.Verb == verb }); i >= 0 && !e.Verbs[i].Enabled {
		m.status = verb + ": " + cmp.Or(e.Verbs[i].Reason, "not offered here")
		return Command{}, false
	}
	if _, busy := m.busy[entryUnit(e.ID)]; busy {
		m.status = fmt.Sprintf("%s %s still running", m.busy[entryUnit(e.ID)], entryLabel(e))
		return Command{}, false
	}
	c := Command{Unit: entryUnit(e.ID), Label: entryLabel(e), Action: verb, entry: e.ID, verb: verb}
	switch {
	case e.Draft != nil && (verb == "approve" || verb == "dismiss" || verb == "retry"):
		return m.draftVerb(c, e, read)
	case verb == "edit":
		shown := m.shownDetail(e)
		if shown.Draft == nil || !m.detailOpen(e) {
			m.status = "open a draft, or pick one with its digit, to edit it"
			return Command{}, false
		}
		c.entry = "draft:" + strconv.FormatInt(shown.Draft.ID, 10)
		return m.editPrompt(c, *shown.Draft, read)
	case verb == "delegate":
		return m.delegatePrompt(e, c, m.bridgedConfirm(e, verb), read)
	case verb == "reply":
		return m.replyPrompt(e, c, read)
	}
	if confirm := m.bridgedConfirm(e, verb); confirm != "" {
		m.confirm(c, confirm, read)
		return Command{}, false
	}
	ask, asks := verbAsks[verb]
	if !asks {
		return m.send(c)
	}
	m.prompt = &prompt{action: verb, label: ask.label + " for " + entryLabel(e), read: read, submit: func(text string) (Command, bool) {
		args, ok := ask.args(text)
		if !ok {
			m.status = verb + " " + c.Label + " cancelled: nothing usable typed"
			return Command{}, false
		}
		c.args = args
		return m.send(c)
	}}
	return Command{}, false
}

// draftVerb approves or retries a draft only from its open detail, with
// the revision on screen (and its text, to approve) after a y; a discard
// asks for a y too.
func (m *Model) draftVerb(c Command, e api.EntryView, read uint64) (Command, bool) {
	target := draftTarget(e)
	if c.verb == "dismiss" {
		m.confirm(c, "discard the draft to "+target+"?", read)
		return Command{}, false
	}
	if !m.detailOpen(e) {
		m.status = "open the draft to " + c.verb + " it (Enter)"
		return Command{}, false
	}
	shown := m.shownDetail(e)
	if shown.Draft == nil {
		m.status = "no draft on screen to " + c.verb
		return Command{}, false
	}
	c.args = map[string]string{"rev": strconv.FormatInt(shown.Draft.Rev, 10)}
	if c.verb == "retry" {
		m.confirm(c, "post again to "+target+"?", read)
		return Command{}, false
	}
	c.args["text"] = shown.Draft.Text
	m.confirm(c, "post to "+target+"?", read)
	return Command{}, false
}

// bridgedConfirm is the confirm of the unit action verb runs on e: the
// first of its units whose row offers an enabled action of that verb.
func (m *Model) bridgedConfirm(e api.EntryView, verb string) string {
	if !slices.Contains([]string{"approve", "delegate", "dismiss"}, verb) {
		return ""
	}
	for _, u := range e.Units {
		r, ok := m.rowOf(u)
		if !ok {
			continue
		}
		for _, s := range m.specs {
			if st, ok := actionState(r, s.ID); ok && s.Verb == verb && st.Enabled {
				return st.Confirm
			}
		}
	}
	return ""
}

// entryUnitAction runs the served action of k on the first of the
// selected entry's units whose row offers it.
func (m *Model) entryUnitAction(k Key) (Command, bool) {
	e, ok := m.selectedEntry()
	if !ok || k.Rune == 0 {
		return Command{}, false
	}
	var fallback *api.ActionSpec
	for _, u := range e.Units {
		r, ok := m.rowOf(u)
		if !ok {
			continue
		}
		spec, st, offered := m.offer(r, k.Rune)
		if spec == nil {
			continue
		}
		if !offered {
			fallback = spec
			continue
		}
		if _, busy := m.busy[r.Unit]; busy {
			m.status = fmt.Sprintf("%s %s still running", m.busy[r.Unit], r.Label)
			return Command{}, false
		}
		if !st.Enabled {
			m.status = st.Reason
			return Command{}, false
		}
		return m.begin(r, *spec, st, k.Read)
	}
	if fallback != nil {
		m.status = fmt.Sprintf("no %s on %s", strings.ToLower(fallback.Label), entryLabel(e))
	}
	return Command{}, false
}

// openSource opens the link of the selection: an entry's first unit, or
// the row's.
func (m *Model) openSource() (Command, bool) {
	if _, ok := m.gtdView(); ok {
		e, ok := m.selectedEntry()
		if !ok {
			return Command{}, false
		}
		link := m.sourceLink(e)
		if link == "" && len(e.Units) == 0 {
			m.status = entryLabel(e) + " has no source to open"
			return Command{}, false
		}
		return m.send(Command{Unit: entryUnit(e.ID), Label: entryLabel(e), Action: "open-source", entry: e.ID, open: link, openFind: link == ""})
	}
	r, ok := m.selected()
	if !ok || m.view >= m.planView() || r.Link == "" {
		return Command{}, false
	}
	return m.send(Command{Unit: r.Unit, Label: r.Label, Action: "open-source", open: r.Link})
}

// overlay is a list over the screen: the palette, whose items run, or the
// help, whose items only show.
type overlay struct {
	title string
	items []paletteItem
	at    int
	help  bool
}

type paletteItem struct {
	label, key string
	run        func(read uint64) (Command, bool)
}

func (m *Model) openPalette() {
	m.overlay = &overlay{title: "Actions", items: m.paletteItems()}
}

// paletteItems are the selection's actions with their keys, the global
// keys, then Area....
func (m *Model) paletteItems() []paletteItem {
	var items []paletteItem
	runKey := func(b api.KeyView) func(uint64) (Command, bool) {
		return func(read uint64) (Command, bool) {
			k := keyOf(b.Key)
			k.Read = read
			return m.run(b, k)
		}
	}
	if _, ok := m.gtdView(); ok {
		if e, ok := m.selectedEntry(); ok {
			for _, v := range e.Verbs {
				b, ok := m.binding(v.Verb, ctxList)
				switch {
				case v.Enabled && ok:
					items = append(items, paletteItem{cmp.Or(m.verbLabel(v.Verb), b.Help), b.Key, runKey(b)})
				case v.Enabled && slices.Contains(projectVerbs, v.Verb):
					verb := v.Verb
					items = append(items, paletteItem{cmp.Or(m.verbLabel(verb), verb), "", func(read uint64) (Command, bool) { return m.entryVerb(verb, read) }})
				}
			}
			for _, u := range e.Units {
				if r, ok := m.rowOf(u); ok {
					items = append(items, m.rowItems(r)...)
				}
			}
			if len(e.Units) > 0 {
				if b, ok := m.binding("open-source", ctxList); ok {
					items = append(items, paletteItem{b.Help, b.Key, runKey(b)})
				}
			}
		}
	} else if r, ok := m.selected(); ok && m.view < m.planView() {
		items = append(items, m.rowItems(r)...)
	}
	for _, b := range m.keys {
		if b.Context == ctxGlobal && b.Action != "palette" && !strings.HasPrefix(b.Action, "area:") {
			items = append(items, paletteItem{b.Help, b.Key, runKey(b)})
		}
	}
	return append(items, paletteItem{label: "Area...", run: func(uint64) (Command, bool) {
		m.openAreas()
		return Command{}, false
	}})
}

// rowItems are the row's enabled keyed actions.
func (m *Model) rowItems(r api.Row) []paletteItem {
	var items []paletteItem
	for _, s := range m.specs {
		st, ok := actionState(r, s.ID)
		if !ok || !st.Enabled || s.Key == "" || !slices.Contains(s.Surfaces, surface) {
			continue
		}
		spec := s
		items = append(items, paletteItem{cmp.Or(st.Label, s.Label) + " " + r.Label, s.Key, func(read uint64) (Command, bool) {
			if _, busy := m.busy[r.Unit]; busy {
				m.status = fmt.Sprintf("%s %s still running", m.busy[r.Unit], r.Label)
				return Command{}, false
			}
			return m.begin(r, spec, st, read)
		}})
	}
	return items
}

// openAreas lists No area and the served areas, each with its Shift key.
func (m *Model) openAreas() {
	items := []paletteItem{{label: "No area", run: func(uint64) (Command, bool) { m.setArea(""); return Command{}, false }}}
	for i, a := range m.gtdCat.Areas {
		key := ""
		if i < 9 {
			key = fmt.Sprintf("Shift+%d", i+1)
		}
		items = append(items, paletteItem{a, key, func(uint64) (Command, bool) { m.setArea(a); return Command{}, false }})
	}
	m.overlay = &overlay{title: "Area", items: items}
}

var helpHeads = []struct{ context, head string }{
	{ctxGlobal, "Everywhere"}, {ctxList, "On the selection"}, {ctxQuestion, "In a question"}, {ctxPlan, "In the plan"}, {"review", "In the review"},
}

// openHelp lists every binding of the terminal by context.
func (m *Model) openHelp() {
	var items []paletteItem
	for _, h := range helpHeads {
		head := false
		for _, b := range m.keys {
			if b.Context != h.context {
				continue
			}
			if !head {
				items, head = append(items, paletteItem{label: h.head}), true
			}
			items = append(items, paletteItem{label: "  " + b.Help, key: b.Key})
		}
	}
	m.overlay = &overlay{title: "Keys", items: items, help: true}
}

// keyOf is the key press a binding's name stands for.
func keyOf(name string) Key {
	switch name {
	case "Enter":
		return Key{Name: KeyEnter}
	case "Esc":
		return Key{Name: KeyEsc}
	case "Backspace":
		return Key{Name: KeyBackspace}
	case "Tab":
		return Key{Name: KeyTab}
	case "Ctrl+K":
		return Key{Name: KeyCtrlK}
	}
	if n, ok := strings.CutPrefix(name, "Shift+"); ok && len(n) == 1 && n[0] >= '1' && n[0] <= '9' {
		return Key{Rune: rune(shiftDigits[n[0]-'1'])}
	}
	r := []rune(name)
	if len(r) == 1 {
		return Key{Rune: r[0]}
	}
	return Key{}
}

// overlayKey moves through the overlay, runs its item on Enter and closes
// it on Esc; the help closes on any other key too.
func (m *Model) overlayKey(k Key) (Command, bool) {
	o := m.overlay
	switch {
	case k.Name == KeyUp || k.Rune == 'k':
		o.at = max(o.at-1, 0)
	case k.Name == KeyDown || k.Rune == 'j':
		o.at = min(o.at+1, max(len(o.items)-1, 0))
	case k.Name == KeyEnter && !o.help && len(o.items) > 0:
		m.overlay = nil
		if it := o.items[o.at]; it.run != nil {
			return it.run(k.Read)
		}
	case k.Name == KeyEsc || o.help:
		m.overlay = nil
	}
	return Command{}, false
}

// renderOverlay draws the overlay's title and its items around the cursor.
func (m *Model) renderOverlay(lines []string, width, body int) []string {
	t := m.t
	o := m.overlay
	lines = append(lines, " "+t.Bold(termfmt.Cut(o.title, width-1)))
	hint := "Enter runs, Esc closes"
	if o.help {
		hint = "any key closes"
	}
	keyW := 0
	for _, it := range o.items {
		keyW = max(keyW, termfmt.Cells(it.key))
	}
	var out []string
	for i, it := range o.items {
		mark := "  "
		if i == o.at && !o.help {
			mark = "> "
		}
		line := mark + termfmt.Fit(it.key, keyW) + "  " + it.label
		line = termfmt.Cut(line, width)
		if i == o.at && !o.help && t.Color {
			line = t.Reverse(line + strings.Repeat(" ", max(width-termfmt.Cells(line), 0)))
		}
		out = append(out, line)
	}
	lines = append(lines, window(out, o.at, body-2)...)
	return append(lines, "  "+t.Dim(hint))
}
