// Package tui is the terminal view of the daemon: the task views of its
// tasks, the plugins' views of their units and the workflows' plan, with
// the keys of #keys.
package tui

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/client/rowset"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/termfmt"
)

// Source is what every request of the TUI names, an answer included.
const Source = string(turn.TUI)

type View struct{ ID, Label, Provider string }

// surface is the catalog's name of the terminal.
const surface = "tui"

// Command is an action for the daemon on one unit; Label names it on the
// status line. A plan action carries its item's PlanID.
type Command struct {
	Unit   unit.Unit
	Label  string
	Action string
	Req    api.ActionRequest
	PlanID string

	// task is the task the command is about: verb runs on it with args,
	// fetch reads its detail, after a seen when seen is set.
	task  string
	verb  string
	args  map[string]string
	fetch bool
	seen  bool
	// capture is the title of a task to capture.
	capture string
	// open is a link to open; openFind looks it up from the task's cards.
	open     string
	openFind bool
	// reviewStep is a Review step to mark done.
	reviewStep string
}

// prompt is the bottom line asking for text, or, with confirm set, for a
// y that sends cmd.
type prompt struct {
	action  string
	label   string
	text    []rune
	confirm bool
	cmd     Command
	spec    api.ActionSpec
	state   api.ActionState
	// question is what an answer prompt answers.
	question *api.QuestionView
	// read is the terminal read of the key that opened the prompt: an
	// answer typed ahead in that same read (a paste without bracketed
	// paste) does not count.
	read uint64
	// submit takes the text of a prompt that is not an action's input.
	submit func(text string) (Command, bool)
	// tab switches the prompt to its other choice, such as a delegate's
	// mode; nil when it has none.
	tab func(p *prompt)
	// editor lets Ctrl+E hand the text to the editor, whose text submit
	// then takes.
	editor bool
}

// EditRequest is a prompt's text handed to the editor; Submit takes what
// the editor returns.
type EditRequest struct {
	Label  string
	Text   string
	submit func(text string) (Command, bool)
}

type Model struct {
	view int
	// views are the catalog's row views for the terminal; catalog names
	// the kinds.
	views   []View
	catalog api.CatalogResponse
	rows    []api.Row
	health  api.Health
	loaded  bool
	sel     unit.Unit
	// at is the selection's index, kept when the selected pull request
	// leaves the view.
	at     int
	prompt *prompt
	// filter narrows every view; filtering is true while it is typed.
	filter    []rune
	filtering bool
	// unfiltered is the selection before the filter, restored when it
	// matches again or the filter is cleared.
	unfiltered unit.Unit
	// filterRead is the read whose Enter or Esc closed the filter: keys
	// typed ahead in it are not actions.
	filterRead uint64
	specs      []api.ActionSpec
	status     string
	err        string
	busy       map[unit.Unit]string
	quit       bool
	t          termfmt.Term

	// plan are the pending and blocked plan items, pending first; planSel
	// and planAt are the Plan view's selection as sel and at are a view's.
	plan       []api.PlanItem
	planSpecs  []api.ActionSpec
	planSel    string
	planAt     int
	planLoaded bool
	planErr    string
	// closed are the sessions closed in the last day, newest first.
	closed []api.ClosedSession

	// plugins are the enabled plugins' views, after the plan; set is the
	// rows as they read them, nil until one asks.
	plugins []PluginView
	set     *rowset.Set
	// served are the plugins the daemon serves, enabled or not.
	served []api.Plugin

	// taskViews are the core views of GET /api/views, after the plan; tasks are
	// each view's tasks as last pulled, counters the tabs' counts.
	taskViews   []View
	viewsCat    api.ViewsCatalog
	tasks       map[string][]api.TaskView
	tasksLoaded bool
	counters    api.Counters
	// filtered are the counts of the tasks the filter's words keep, as
	// last read.
	filtered *api.Counters
	area     string
	taskSel  string
	taskAt   int
	// detailID is the task whose detail is open; opened is what the
	// daemon answered for it, stale once a page changed it.
	detailID    string
	opened      *api.TaskView
	detailStale bool
	// detailRead is the read that opened the detail: a digit typed ahead
	// in it does not answer.
	detailRead uint64
	seen       map[string]bool
	// navigated is set once a key chose a view, so the task views arriving
	// late do not move the screen to Focus.
	navigated bool

	keys    []api.KeyView
	chord   string
	overlay *overlay

	// edit is a prompt's text waiting for the editor.
	edit *EditRequest
	// drafts are the drafts made for the open detail's task, draftsFor
	// that task, picked the one the draft keys act on, 0 none.
	drafts    []api.Draft
	draftsFor string
	picked    int64

	rv reviewView
}

func NewModel() *Model { return &Model{busy: map[unit.Unit]string{}} }

// SetColor styles the screen with escapes; off, it is plain text.
func (m *Model) SetColor(on bool) { m.t.Color = on }

func (m *Model) Quit() bool { return m.quit }

// Stay undoes a quit the caller cannot honor yet, and says why.
func (m *Model) Stay(why string) { m.quit, m.status = false, why }

// SetData replaces the rows and keeps the selected pull request while it is
// still in the view, else the same place in the list.
func (m *Model) SetData(rows []api.Row, h api.Health) {
	m.rows, m.health, m.loaded, m.err, m.set = rows, h, true, "", nil
	m.keepSelection()
}

// SetCatalog takes the terminal's row views and the kinds' labels from the
// served catalog, keeping the current view by id.
func (m *Model) SetCatalog(c api.CatalogResponse) {
	current := m.viewKey()
	m.catalog, m.views = c, nil
	for _, v := range c.ViewsOn(surface) {
		if v.Rows {
			m.views = append(m.views, View{ID: v.ID, Label: v.Label, Provider: v.Provider})
		}
	}
	m.goTo(current)
}

// planView is the index of the Plan view, after the row views.
func (m *Model) planView() int { return len(m.views) }

// kindLabel is the catalog's label of the row's kind.
func (m *Model) kindLabel(r api.Row) string { return m.catalog.KindLabel(r) }

// SetSpecs replaces the action specs the keys follow.
func (m *Model) SetSpecs(specs []api.ActionSpec) { m.specs = specs }

// SetError keeps the last rows on screen while the daemon does not answer.
func (m *Model) SetError(err error) { m.err = "daemon not answering: " + err.Error() }

func (m *Model) in(id string) []api.Row {
	var out []api.Row
	q := string(m.filter)
	for _, r := range m.rows {
		if slices.Contains(r.Views, id) && Matches(r, q, m.kindLabel(r), prOf(r).haystack()...) {
			out = append(out, r)
		}
	}
	return out
}

func (m *Model) visible() []api.Row {
	if m.view >= m.planView() || m.view >= len(m.views) {
		return nil
	}
	return m.in(m.views[m.view].ID)
}

func (m *Model) cursor(rows []api.Row) int {
	if i := slices.IndexFunc(rows, func(r api.Row) bool { return r.Unit == m.sel }); i >= 0 {
		return i
	}
	return min(max(m.at, 0), max(len(rows)-1, 0))
}

func (m *Model) keepSelection() {
	if m.view >= m.planView() {
		return
	}
	rows := m.visible()
	if len(rows) == 0 {
		m.sel = unit.Unit{}
		return
	}
	m.at = m.cursor(rows)
	m.sel = rows[m.at].Unit
}

func (m *Model) selected() (api.Row, bool) {
	rows := m.visible()
	if len(rows) == 0 {
		return api.Row{}, false
	}
	return rows[m.cursor(rows)], true
}

func (m *Model) move(delta int) {
	if m.view == m.planView() {
		m.planMove(delta)
		return
	}
	if _, ok := m.viewOnScreen(); ok {
		if !m.reviewMove(delta) {
			m.taskMove(delta)
		}
		return
	}
	rows := m.visible()
	if len(rows) == 0 {
		return
	}
	m.at = min(max(m.cursor(rows)+delta, 0), len(rows)-1)
	m.sel = rows[m.at].Unit
}

func (m *Model) setView(i int) {
	m.view, m.at, m.taskAt, m.taskSel, m.navigated = i, 0, 0, "", true
	m.rv.step, m.rv.head = "", true
	if m.rv.review != nil {
		m.rv.step = firstOpenStep(m.rv.review.Steps, -1)
	}
	m.closeDetail()
	m.keepSelection()
	m.keepTaskSelection()
}

// Key applies one key press; ok means the command goes to the daemon.
func (m *Model) Key(k Key) (c Command, ok bool) {
	if k.Name == KeyCtrlC {
		m.quit = true
		return Command{}, false
	}
	if m.prompt != nil {
		return m.promptKey(k)
	}
	if m.filtering {
		return m.filterKey(k)
	}
	if m.overlay != nil {
		return m.overlayKey(k)
	}
	if k.Read <= m.filterRead && k.Name != KeyUp && k.Name != KeyDown && k.Rune != '/' {
		return Command{}, false
	}
	if m.pickDraft(k) {
		return Command{}, false
	}
	if m.chord != "" {
		return m.dispatch(k)
	}
	switch {
	case k.Rune == 'q':
		m.quit = true
	case k.Name == KeyTab:
		m.cycle()
	case m.view >= m.pluginBase():
		if v, ok := m.plugin(); ok {
			if name := bindingName(k); name == "?" || name == "Ctrl+K" || m.prefix(name) {
				return m.dispatch(k)
			}
			return m.pluginKey(v, k)
		}
	case k.Name == KeyUp:
		m.move(-1)
	case k.Name == KeyDown:
		m.move(1)
	case k.Rune == '/':
		m.startFilter()
	case k.Name == KeyEsc && m.picked != 0:
		m.picked = 0
	case k.Name == KeyEsc && m.detailID != "":
		m.closeDetail()
	case k.Name == KeyEsc && len(m.filter) > 0:
		m.filter = nil
		m.restoreSelection()
	default:
		return m.dispatch(k)
	}
	return Command{}, false
}

func (m *Model) action(k Key) (Command, bool) {
	r, ok := m.selected()
	if !ok {
		return Command{}, false
	}
	spec, st, offered := m.offer(r, k.Rune)
	if spec == nil {
		return Command{}, false
	}
	if running, busy := m.busy[r.Unit]; busy {
		m.status = fmt.Sprintf("%s %s still running", running, r.Label)
		return Command{}, false
	}
	switch {
	case !offered:
		m.status = fmt.Sprintf("no %s on %s", strings.ToLower(spec.Label), r.Label)
	case !st.Enabled:
		m.status = st.Reason
	default:
		return m.begin(r, *spec, st, k.Read)
	}
	return Command{}, false
}

// offer is the TUI action the key names on the row: the side of a toggle
// the row offers, else the first spec with the key, with offered false.
func (m *Model) offer(r api.Row, key rune) (spec *api.ActionSpec, st api.ActionState, offered bool) {
	for i := range m.specs {
		s := &m.specs[i]
		if s.Key != string(key) || !slices.Contains(s.Surfaces, "tui") {
			continue
		}
		if st, ok := actionState(r, s.ID); ok {
			return s, st, true
		}
		if spec == nil {
			spec = s
		}
	}
	return spec, api.ActionState{}, false
}

func (m *Model) begin(r api.Row, spec api.ActionSpec, st api.ActionState, read uint64) (Command, bool) {
	c := Command{Unit: r.Unit, Label: r.Label, Action: spec.ID, Req: api.ActionRequest{Source: Source, Basis: st.Basis}}
	if spec.Input == nil {
		return m.finish(c, st, read)
	}
	label := inputLabel(r.Label, spec, st)
	if spec.Input.Kind == "answer" {
		if r.Question == nil {
			m.status = "no open question on " + r.Label
			return Command{}, false
		}
		label = questionLabel(r.Label, r.Question)
	}
	m.ask(spec, st, c, label, read)
	m.prompt.question = r.Question
	return Command{}, false
}

// finish sends the command, after a y when the state carries a confirm.
func (m *Model) finish(c Command, st api.ActionState, read uint64) (Command, bool) {
	if st.Confirm != "" {
		m.confirm(c, st.Confirm, read)
		return Command{}, false
	}
	return m.send(c)
}

func inputLabel(name string, spec api.ActionSpec, st api.ActionState) string {
	in := spec.Input
	var hints []string
	if in.Placeholder != "" {
		hints = append(hints, in.Placeholder)
	}
	if in.UntilState {
		hints = append(hints, "empty: until it changes")
	}
	hint := strings.Join(hints, "; ")
	label := strings.ToLower(cmp.Or(st.Label, spec.Label)) + " " + name
	if in.Label != "" {
		label += ": " + in.Label
	}
	if hint != "" {
		label += " (" + hint + ")"
	}
	return label
}

// questionLabel is the question's summary, with the drafted verdict and
// the numbered options of a choice.
func questionLabel(name string, q *api.QuestionView) string {
	text := q.Text
	if text == "" {
		text = q.LastMessage
	}
	if text == "" {
		text = "the agent stopped"
	}
	label := name + ": " + text
	if q.Verdict != "" {
		label += fmt.Sprintf(" (drafted %s, %d blocking, %d non-blocking)", q.Verdict, q.Blocking, q.NonBlocking)
	}
	if q.Kind == "confirm" {
		return label + " y/n"
	}
	var opts []string
	for i, o := range q.Options {
		opt := fmt.Sprintf("%d. %s", i+1, o.Label)
		if o.ID == q.Recommend {
			opt += " (recommended)"
		}
		opts = append(opts, opt)
	}
	if len(opts) > 0 {
		label += " " + strings.Join(opts, "  ")
	}
	return label
}

// answerRequest reads a number alone as a choice's option and y, yes, n or
// no alone, in any case, as a confirm's answer; anything else is a free
// answer.
func answerRequest(q *api.QuestionView, text string) api.ActionRequest {
	req := api.ActionRequest{Guidance: text, Source: Source}
	if q.Kind == "confirm" {
		switch strings.ToLower(text) {
		case "y", "yes":
			return api.ActionRequest{Yes: true, Source: req.Source}
		case "n", "no":
			return api.ActionRequest{No: true, Source: req.Source}
		}
	}
	if n, err := strconv.Atoi(text); err == nil && n >= 1 && n <= len(q.Options) {
		o := q.Options[n-1]
		req.Guidance, req.OptionID = o.Answer(), o.ID
	}
	return req
}

// actionState is the row's state of the action id; false when the row does
// not offer it.
func actionState(r api.Row, id string) (api.ActionState, bool) {
	for _, st := range r.Actions {
		if st.ID == id {
			return st, true
		}
	}
	return api.ActionState{}, false
}

func (m *Model) ask(spec api.ActionSpec, st api.ActionState, c Command, label string, read uint64) {
	m.prompt = &prompt{action: c.Action, label: label, spec: spec, state: st, cmd: c, read: read}
}

// confirm asks for a y before a command that is public or hard to undo.
func (m *Model) confirm(c Command, label string, read uint64) {
	m.prompt = &prompt{action: c.Action, label: label, confirm: true, cmd: c, read: read}
}

func (m *Model) promptKey(k Key) (Command, bool) {
	p := m.prompt
	if p.confirm {
		m.prompt = nil
		if k.Read > p.read && (k.Rune == 'y' || k.Rune == 'Y') {
			return m.send(p.cmd)
		}
		m.status = p.action + " " + p.cmd.Label + " cancelled"
		if k.Read <= p.read {
			m.status += ": a y typed together with the key before it does not confirm"
		}
		return Command{}, false
	}
	switch {
	case k.Name == KeyEsc:
		m.prompt = nil
	case k.Name == KeyTab && p.tab != nil:
		p.tab(p)
	case k.Name == KeyCtrlE && p.editor && p.submit != nil:
		m.prompt = nil
		m.edit = &EditRequest{Label: p.action, Text: string(p.text), submit: p.submit}
	case k.Name == KeyBackspace:
		if len(p.text) > 0 {
			p.text = p.text[:len(p.text)-1]
		}
	case k.Name == KeyEnter && k.Read <= p.read:
	case k.Name == KeyEnter:
		m.prompt = nil
		text := strings.TrimSpace(string(p.text))
		if p.submit != nil {
			return p.submit(text)
		}
		c, in := p.cmd, p.spec.Input
		switch {
		case in.Kind == "answer":
			if text == "" {
				m.status = "answer " + p.cmd.Label + " cancelled: nothing typed"
				return Command{}, false
			}
			c.Req = answerRequest(p.question, text)
		case in.Kind == "duration":
			c.Req.Until, c.Req.UntilState = text, text == "" && in.UntilState
		case text == "" && in.Required:
			m.status = p.action + " " + p.cmd.Label + " cancelled: nothing typed"
			return Command{}, false
		default:
			c.Req.Guidance = text
		}
		return m.finish(c, p.state, k.Read)
	case k.Name == KeyPaste:
		p.add(strings.Join(strings.Fields(termfmt.Clean(k.Text)), " "))
	case k.Rune != 0:
		p.add(string(k.Rune))
	}
	return Command{}, false
}

// TakeEdit is the text a prompt handed to the editor, nil when none waits.
func (m *Model) TakeEdit() *EditRequest {
	e := m.edit
	m.edit = nil
	return e
}

// Edited submits what the editor returned for e; an empty text cancels.
func (m *Model) Edited(e *EditRequest, text string, err error) (Command, bool) {
	if err != nil {
		m.status = e.Label + ": the editor failed: " + err.Error()
		return Command{}, false
	}
	return e.submit(editedText(text))
}

// editedText is the editor's text with its lines kept and every other
// control character dropped.
func editedText(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || (r >= 0x20 && r != 0x7f) {
			return r
		}
		return -1
	}, s))
}

func (p *prompt) add(s string) {
	p.text = typeable(p.text, s)
	if max := p.spec.Input; max != nil && max.Max > 0 && len(p.text) > max.Max {
		p.text = p.text[:max.Max]
	}
}

// typeable appends the runes a prompt or the filter accepts, up to
// pasteMax: printable ones and the joiners clean keeps, so no control or
// invisible format character reaches the agent's guidance or the screen.
func typeable(text []rune, s string) []rune {
	for _, r := range s {
		if len(text) == pasteMax {
			break
		}
		if unicode.IsPrint(r) || termfmt.Joiner(r) {
			text = append(text, r)
		}
	}
	return text
}

func (m *Model) filterKey(k Key) (Command, bool) {
	switch {
	case k.Name == KeyEsc:
		m.filter, m.filtering, m.filterRead = nil, false, k.Read
	case k.Name == KeyEnter:
		m.filtering, m.filterRead = false, k.Read
		m.jump()
		return Command{}, false
	case k.Name == KeyUp:
		m.move(-1)
		return Command{}, false
	case k.Name == KeyDown:
		m.move(1)
		return Command{}, false
	case k.Name == KeyBackspace:
		if len(m.filter) > 0 {
			m.filter = m.filter[:len(m.filter)-1]
		}
	case k.Name == KeyPaste:
		m.filter = typeable(m.filter, strings.Join(strings.Fields(termfmt.Clean(k.Text)), " "))
	case k.Rune != 0:
		m.filter = typeable(m.filter, string(k.Rune))
	}
	m.restoreSelection()
	return Command{}, false
}

// restoreSelection goes back to the selection from before the filter when
// its row is in sight, else keeps the current one.
func (m *Model) restoreSelection() {
	if slices.ContainsFunc(m.visible(), func(r api.Row) bool { return r.Unit == m.unfiltered }) {
		m.sel = m.unfiltered
	}
	m.keepSelection()
	m.keepTaskSelection()
}

// jump turns a pasted web address into the selection of the row it links
// to, or a page under it, in the first view that holds it.
func (m *Model) jump() {
	addr := strings.TrimSpace(string(m.filter))
	if !strings.HasPrefix(addr, "https://") && !strings.HasPrefix(addr, "http://") {
		m.restoreSelection()
		return
	}
	m.filter = nil
	for i, v := range m.views {
		for _, r := range m.in(v.ID) {
			if links(r.Link, addr) {
				m.view, m.sel = i, r.Unit
				m.keepSelection()
				return
			}
		}
	}
	m.restoreSelection()
	labels := make([]string, len(m.views))
	for i, v := range m.views {
		labels[i] = v.Label
	}
	m.status = addr + " is not in " + orList(labels) + "; " + brand.Command + " open " + addr + " shows it"
}

// links says whether addr is link or a page under it.
func links(link, addr string) bool {
	if link == "" || len(addr) < len(link) || !strings.EqualFold(addr[:len(link)], link) {
		return false
	}
	return len(addr) == len(link) || strings.ContainsRune("/?#", rune(addr[len(link)]))
}

func (m *Model) send(c Command) (Command, bool) {
	m.busy[c.Unit] = c.Action
	m.status = c.Action + " " + c.Label + "…"
	return c, true
}

// Done reports a finished action on the status line.
func (m *Model) Done(c Command, lines []string, err error) {
	delete(m.busy, c.Unit)
	switch {
	case err != nil:
		m.status = fmt.Sprintf("%s %s failed: %v", c.Action, c.Label, err)
	case len(lines) > 0:
		m.status = fmt.Sprintf("%s %s: %s", c.Action, c.Label, lines[len(lines)-1])
	default:
		m.status = fmt.Sprintf("%s %s: done", c.Action, c.Label)
	}
}

// orList joins names as "a, b or c".
func orList(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}
