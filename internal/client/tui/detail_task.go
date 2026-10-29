package tui

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/record"
	dtask "github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/termfmt"
)

// askCard is a question a task or a row asks, as its detail shows it and
// the digits answer it.
type askCard struct {
	text      string
	options   []record.Option
	recommend string
	confirm   bool
	// task is the task whose answer verb takes the answer; row the row
	// whose answer action does, when no task does; gate the task's open
	// gate whose intents the digits pick, when no question does.
	task string
	row  *api.Row
	gate *api.GateView
}

// openTask opens the detail of e and asks the daemon for it, marking it
// seen first when it is an Inbox task that changed.
func (m *Model) openTask(e api.TaskView, read uint64) (Command, bool) {
	m.detailID, m.opened, m.detailStale, m.detailRead = e.ID, nil, false, read
	c := Command{Unit: taskUnit(e.ID), Label: taskLabel(e), Action: "detail", task: e.ID, fetch: true}
	if e.Category == string(dtask.CategoryInbox) && e.Changed && !m.seen[e.ID] {
		if m.seen == nil {
			m.seen = map[string]bool{}
		}
		m.seen[e.ID] = true
		c.seen, c.seenVerb = true, m.verbOf(handlerSeen)
	}
	return c, true
}

func (m *Model) closeDetail() { m.detailID, m.opened, m.detailStale = "", nil, false }

func (m *Model) detailOpen(e api.TaskView) bool { return m.detailID != "" && m.detailID == e.ID }

// SetDetail shows the task the daemon answered for the open detail.
func (m *Model) SetDetail(id string, e api.TaskView, err error) {
	if id != m.detailID {
		return
	}
	if err != nil {
		m.status = "detail of " + id + " not loaded: " + err.Error()
		return
	}
	m.opened, m.detailStale = &e, false
}

// Refetch asks again for the open detail once a page changed its task.
func (m *Model) Refetch() (Command, bool) {
	if !m.detailStale || m.detailID == "" {
		return Command{}, false
	}
	m.detailStale = false
	return Command{Unit: taskUnit(m.detailID), Action: "detail", task: m.detailID, fetch: true}, true
}

// shownDetail is the fetched detail of e, else e as the list has it.
func (m *Model) shownDetail(e api.TaskView) api.TaskView {
	if m.opened != nil && m.opened.ID == e.ID {
		return *m.opened
	}
	return e
}

// questionCard is the open question of e: the message of its open gate when it
// offers choices, else the question its first unit's row asks, else its first
// open gate but a draft's, whose intents the digits pick.
func (m *Model) questionCard(e api.TaskView) (askCard, bool) {
	if msg, _, ok := e.MessageOf(); ok && (len(msg.Options) > 0 || msg.Confirm) {
		return askCard{text: msg.Text, options: msg.Options, recommend: msg.Recommend, confirm: msg.Confirm, task: e.ID}, true
	}
	for _, u := range e.Units {
		if r, ok := m.rowOf(u); ok && r.Question != nil {
			c := rowCard(r)
			return c, true
		}
	}
	for _, g := range e.Gates {
		if g.Status == string(gate.Open) && g.Kind != string(gate.KindDraft) && len(g.Intents) > 0 {
			return askCard{text: gateText(g), task: e.ID, gate: &g}, true
		}
	}
	return askCard{}, false
}

// gateText is what a gate asks: its words, else its message's text, else its name.
func gateText(g api.GateView) string {
	var msg api.Message
	if len(g.Subject) > 0 && json.Unmarshal(g.Subject, &msg) == nil && msg.Text != "" && g.Text == "" {
		return msg.Text
	}
	return cmp.Or(g.Text, g.Name, g.Kind)
}

func rowCard(r api.Row) askCard {
	q := r.Question
	return askCard{text: cmp.Or(q.Text, q.LastMessage), options: q.Options, recommend: q.Recommend, confirm: q.Kind == "confirm", row: &r}
}

// activeCard is the question the digits answer: the open task detail's,
// or the selected row's in a rows view.
func (m *Model) activeCard() (askCard, bool) {
	if _, ok := m.viewOnScreen(); ok {
		e, ok := m.selectedTask()
		if !ok || !m.detailOpen(e) {
			return askCard{}, false
		}
		return m.questionCard(m.shownDetail(e))
	}
	if m.view >= m.planView() {
		return askCard{}, false
	}
	if r, ok := m.selected(); ok && r.Question != nil {
		return rowCard(r), true
	}
	return askCard{}, false
}

// answers are the card's answers in the order its digits pick them.
func (c askCard) answers() []string {
	if c.gate != nil {
		out := make([]string, len(c.gate.Intents))
		for i, x := range c.gate.Intents {
			out[i] = cmp.Or(x.Label, x.ID)
			if x.ID == c.gate.Default && !c.gate.Deadline.IsZero() {
				out[i] += " (at " + c.gate.Deadline.Local().Format("15:04") + ")"
			}
		}
		return out
	}
	if c.confirm {
		return []string{"yes", "no"}
	}
	out := make([]string, len(c.options))
	for i, o := range c.options {
		out[i] = o.Label
		if o.ID == c.recommend {
			out[i] += " (recommended)"
		}
	}
	return out
}

// answer picks the card's answer n.
func (m *Model) answer(c askCard, n int, read uint64) (Command, bool) {
	_, inViews := m.viewOnScreen()
	if inViews && read <= m.detailRead {
		return Command{}, false
	}
	if n < 1 || n > len(c.answers()) {
		m.status = fmt.Sprintf("no option %d", n)
		return Command{}, false
	}
	if c.row != nil {
		r := *c.row
		st, ok := actionState(r, "answer")
		switch {
		case !ok:
			m.status = "no answer on " + r.Label
			return Command{}, false
		case !st.Enabled:
			m.status = st.Reason
			return Command{}, false
		}
		req := answerRequest(r.Question, fmt.Sprint(n))
		if c.confirm {
			req = api.ActionRequest{Yes: n == 1, No: n == 2, Source: Source}
		}
		req.Basis = st.Basis
		cmd := Command{Unit: r.Unit, Label: r.Label, Action: "answer", Req: req}
		if !inViews {
			m.confirm(cmd, fmt.Sprintf("answer %s: %d. %s?", r.Label, n, c.answers()[n-1]), read)
			return Command{}, false
		}
		return m.finish(cmd, st, read)
	}
	if c.gate != nil {
		return m.resolveGate(c, c.gate.Intents[n-1], read)
	}
	args := map[string]string{}
	switch {
	case c.confirm && n == 1:
		args["yes"] = "true"
	case c.confirm:
		args["no"] = "true"
	default:
		o := c.options[n-1]
		args["option_id"], args["guidance"] = o.ID, o.Answer()
	}
	e, _ := m.selectedTask()
	verb := m.verbOf(handlerAnswer)
	return m.send(Command{Unit: taskUnit(c.task), Label: taskLabel(e), Action: verb, task: c.task, verb: verb, args: args})
}

// resolveGate resolves the card's gate with intent x, asking first for the
// answer of an intent that takes one.
func (m *Model) resolveGate(c askCard, x api.IntentView, read uint64) (Command, bool) {
	e, _ := m.selectedTask()
	label := cmp.Or(x.Label, x.ID)
	cmd := Command{Unit: taskUnit(c.task), Label: taskLabel(e), Action: label, task: c.task, gate: c.gate.ID, intent: x.ID}
	if !x.Answer {
		return m.send(cmd)
	}
	m.prompt = &prompt{action: x.ID, label: label + ": " + c.text, read: read, editor: true, submit: func(text string) (Command, bool) {
		if text == "" {
			m.status = label + " " + cmd.Label + " cancelled: nothing typed"
			return Command{}, false
		}
		cmd.args = map[string]string{"answer": text}
		return m.send(cmd)
	}}
	return Command{}, false
}

// taskDetail is the task, its facts, the question card, the draft, its
// asks, its open gates, its runs, one card per unit and the keys it offers.
func (m *Model) taskDetail(e api.TaskView, width int, now time.Time) []string {
	t := m.t
	lines := []string{t.Bold(termfmt.Cut(oneLine(cmp.Or(e.Title, e.ID)), width))}
	col := 2 + factLabel + 2
	room := width - col
	fact := func(label, text string, style func(string) string) {
		for i, l := range termfmt.Wrap(oneLine(text), room, room) {
			if i == 0 {
				lines = append(lines, "  "+t.Dim(termfmt.Fit(label, factLabel))+"  "+style(l))
				continue
			}
			lines = append(lines, strings.Repeat(" ", col)+style(l))
		}
	}
	para := func(label, text string) {
		first := true
		for _, p := range strings.Split(termfmt.Clean(strings.ReplaceAll(text, "\r", "")), "\n") {
			if first {
				fact(label, p, plain)
				first = false
				continue
			}
			fact("", p, plain)
		}
	}
	state := []string{m.stateLabel(e.State)}
	star, _ := m.viewsCat.PropOf(string(dtask.RoleStar))
	if m.viewsCat.Starred(e) {
		state = append(state, star.Name)
	}
	if e.Changed {
		state = append(state, "changed")
	}
	if w := e.Wake; w != nil && !w.Until.IsZero() {
		state = append(state, "until "+w.Until.In(now.Location()).Format("2006-01-02 15:04"))
	}
	if w := e.Wake; w != nil && w.When != "" {
		state = append(state, "until "+w.When)
	}
	fact("state", strings.Join(state, " · "), plain)
	for _, name := range slices.Sorted(maps.Keys(e.Props)) {
		if text := propText(e, name, now); text != "" && name != star.Name {
			fact(name, text, plain)
		}
	}
	if e.Parent != "" {
		fact("parent", e.Parent, plain)
	}
	if e.OwedBy != "" {
		fact("owed by", e.OwedBy, plain)
	}
	if e.Notes != "" {
		para("notes", e.Notes)
	}
	card, carded := m.questionCard(e)
	if carded && card.gate == nil {
		fact("asks", cmp.Or(card.text, "the agent stopped"), t.Yellow)
		for i, a := range card.answers() {
			fact("", fmt.Sprintf("%d. %s", i+1, a), plain)
		}
	} else if msg, _, ok := e.MessageOf(); ok && !carded {
		fact("message", msg.Text, plain)
	}
	if d := draftOn(e); d != nil {
		m.draftFacts(d, fact, para)
	}
	m.draftLines(fact, para)
	for _, a := range e.Asks {
		fact("ask", strings.TrimSuffix(a.Summary+" · "+a.Asker, " · "), plain)
		for _, q := range a.Quotes {
			fact("", "> "+q, t.Dim)
		}
		if a.Permalink != "" {
			fact("", a.Permalink, t.Dim)
		}
	}
	m.gateFacts(e, card, carded, fact)
	for _, r := range e.Runs {
		m.runFacts(r, fact)
	}
	for _, s := range e.Cards {
		m.unitCard(s, fact, now.Location())
	}
	if len(e.Cards) == 0 && m.opened == nil {
		fact("", "loading…", t.Dim)
	}
	return append(lines, m.taskKeys(e, width, col)...)
}

// gateFacts adds e's open gates but its draft's and the question the card
// shows: what each asks, and its intents, numbered for the digits on the
// gate the card holds.
func (m *Model) gateFacts(e api.TaskView, card askCard, carded bool, fact func(label, text string, style func(string) string)) {
	t := m.t
	_, asked, isMsg := e.MessageOf()
	for _, g := range e.Gates {
		if g.Status != string(gate.Open) || g.Kind == string(gate.KindDraft) || (isMsg && carded && card.gate == nil && g.ID == asked.ID) {
			continue
		}
		fact("gate", gateText(g), t.Yellow)
		if carded && card.gate != nil && card.gate.ID == g.ID {
			for i, a := range card.answers() {
				fact("", fmt.Sprintf("%d. %s", i+1, a), plain)
			}
			continue
		}
		var intents []string
		for _, x := range g.Intents {
			intents = append(intents, cmp.Or(x.Label, x.ID))
		}
		if len(intents) > 0 {
			fact("", strings.Join(intents, " · "), t.Dim)
		}
	}
}

// runFacts adds a run of the task: its intent and state, where it runs and
// the command that resumes its session.
func (m *Model) runFacts(r api.RunView, fact func(label, text string, style func(string) string)) {
	t := m.t
	head := []string{r.Intent, r.State}
	if r.SessionState != "" {
		head = append(head, "session "+r.SessionState)
	}
	fact("run", strings.Join(slices.DeleteFunc(head, func(s string) bool { return s == "" }), " · "), plain)
	if where := strings.Join(slices.DeleteFunc([]string{r.Manager, r.Workspace, r.Session}, func(s string) bool { return s == "" }), " · "); where != "" {
		fact("", where, t.Dim)
	}
	if r.Resume != "" {
		fact("", "resume: "+r.Resume, t.Dim)
	}
}

// stateLabel is the catalog's label of a state, else its name.
func (m *Model) stateLabel(name string) string {
	if i := slices.IndexFunc(m.viewsCat.States, func(st api.StateSpec) bool { return st.Name == name }); i >= 0 {
		return cmp.Or(m.viewsCat.States[i].Label, name)
	}
	return name
}

// unitCard adds one unit's stored card.
func (m *Model) unitCard(s api.CardView, fact func(label, text string, style func(string) string), zone *time.Location) {
	t := m.t
	c := s.Card
	head := cmp.Or(c.Label, c.Unit.Label())
	if c.Title != "" {
		head += "  " + c.Title
	}
	fact("unit", head, t.Bold)
	state := []string{c.State}
	if c.Outcome != "" {
		state = append(state, string(c.Outcome))
	}
	if s.Stale {
		state = append(state, "plugin disabled, read-only")
	}
	fact("", strings.Join(slices.DeleteFunc(state, func(s string) bool { return s == "" }), " · "), t.Dim)
	if c.Why != "" {
		fact("", c.Why, plain)
	}
	var sig []string
	for _, x := range c.Signals {
		v := x.Value
		if !x.At.IsZero() {
			v = x.At.In(zone).Format("2006-01-02 15:04")
		}
		sig = append(sig, x.Key+" "+v)
	}
	if len(sig) > 0 {
		fact("", strings.Join(sig, " · "), t.Dim)
	}
	if h := c.LastHuman; h != nil {
		fact("", "last: "+h.Who.Name+" "+h.What, t.Dim)
	}
}

// taskKeys lists the task's verbs with their keys: the enabled ones, then
// the disabled ones dimmed.
func (m *Model) taskKeys(e api.TaskView, width, col int) []string {
	t := m.t
	on, off := m.verbHelp(e)
	var lines []string
	head := "  " + t.Dim(termfmt.Fit("keys", factLabel)) + "  "
	for _, l := range m.pack(on, width-col) {
		lines = append(lines, head+l)
		head = strings.Repeat(" ", col)
	}
	if len(off) > 0 {
		lines = append(lines, "  "+t.Dim(termfmt.Fit("not now", factLabel))+"  "+t.Dim(termfmt.Cut(strings.Join(off, " · "), width-col)))
	}
	return lines
}

// verbHelp are the task's verbs that have a key, enabled and not, then
// the enabled actions of its units' rows.
func (m *Model) verbHelp(e api.TaskView) (on []helpItem, off []string) {
	for _, v := range e.Verbs {
		b, ok := m.binding(v.Verb, ctxList)
		if !ok {
			continue
		}
		h := helpItem{b.Key, strings.ToLower(cmp.Or(m.verbLabel(v.Verb), b.Help))}
		if !v.Enabled && m.viewsCat.HandlerOf(v.Verb) == handlerEdit && draftOn(e) == nil && len(m.listedDrafts()) == 0 {
			continue
		}
		if v.Enabled {
			on = append(on, h)
		} else {
			off = append(off, h.String())
		}
	}
	for _, u := range e.Units {
		r, ok := m.rowOf(u)
		if !ok {
			continue
		}
		for _, s := range m.specs {
			if st, ok := actionState(r, s.ID); ok && st.Enabled && s.Key != "" && slices.Contains(s.Surfaces, surface) {
				on = append(on, helpItem{s.Key, strings.ToLower(cmp.Or(st.Label, s.Label))})
			}
		}
	}
	return on, off
}

// verbQuestion is what a verb that asks puts first: the catalog's input.
func (m *Model) verbQuestion(verb string) string {
	for _, v := range m.viewsCat.Verbs {
		if v.Verb == verb {
			return cmp.Or(v.Input, v.Label, verb)
		}
	}
	return verb
}

// verbSend is what a reply of verb does by default, as the daemon serves it.
func (m *Model) verbSend(verb string) string {
	for _, v := range m.viewsCat.Verbs {
		if v.Verb == verb {
			return v.Send
		}
	}
	return ""
}

func (m *Model) verbLabel(verb string) string {
	for _, v := range m.viewsCat.Verbs {
		if v.Verb == verb {
			return v.Label
		}
	}
	return ""
}

// propText is a property as a person reads it: a time in now's location, a list joined.
func propText(e api.TaskView, name string, now time.Time) string {
	if at := e.PropTime(name); !at.IsZero() {
		return at.In(now.Location()).Format("2006-01-02 15:04")
	}
	if l := e.PropList(name); len(l) > 0 {
		return strings.Join(l, " ")
	}
	return e.PropText(name)
}
