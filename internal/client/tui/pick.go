package tui

import (
	"cmp"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	dtask "github.com/wloophq/wloop/internal/domain/task"
)

// The picks of api.VerbSpec.Pick: the open tasks, which the daemon lists for the text typed, or the
// tasks merged into the task, or those related to it.
const (
	pickOpen    = "open"
	pickMerged  = "merged"
	pickRelated = "related"
)

// taskPick is an overlay picking the task a verb takes on task: the text typed narrows the tasks its pick
// offers, which the daemon lists for the open tasks.
type taskPick struct {
	task  api.TaskView
	verb  string
	pick  string
	query []rune
	// pending is set while the daemon has not answered for query: the items are an older text's.
	pending bool
}

// verbPick is the pick of the served verb, "" for a verb that takes no other task.
func (m *Model) verbPick(verb string) string {
	i := slices.IndexFunc(m.viewsCat.Verbs, func(v api.VerbSpec) bool { return v.Verb == verb })
	if i < 0 {
		return ""
	}
	return m.viewsCat.Verbs[i].Pick
}

// pickedRefs are the tasks of e that pick lists, nil for the open tasks, which the daemon lists.
func pickedRefs(e api.TaskView, pick string) []api.TaskRef {
	switch pick {
	case pickMerged:
		return e.MergedFrom
	case pickRelated:
		return e.Related
	}
	return nil
}

// openPick opens the picker of verb on e; for the open tasks, the command asks the daemon for them.
func (m *Model) openPick(e api.TaskView, verb, pick string) (Command, bool) {
	e = m.shownDetail(e)
	m.overlay = &overlay{title: m.verbQuestion(verb) + " for " + taskLabel(e), pick: &taskPick{task: e, verb: verb, pick: pick}}
	return m.repick()
}

// repick lists the tasks the picker's text holds: e's own at once, the open ones once the daemon answers.
func (m *Model) repick() (Command, bool) {
	p := m.overlay.pick
	m.overlay.at = 0
	if p.pick == pickOpen {
		p.pending = true
		return Command{Action: "match", match: string(p.query), matching: true, task: p.task.ID}, true
	}
	want := strings.ToLower(strings.TrimSpace(string(p.query)))
	var items []paletteItem
	for _, r := range pickedRefs(p.task, p.pick) {
		if title, state := m.refShown(r); strings.Contains(strings.ToLower(cmp.Or(title, r.ID)), want) {
			items = append(items, m.pickItem(p, r.ID, title, state))
		}
	}
	m.overlay.items = items
	return Command{}, false
}

// SetMatches lists the open tasks the daemon answered for text, while the picker still holds that text.
func (m *Model) SetMatches(text string, tasks []api.TaskView, err error) {
	if m.overlay == nil || m.overlay.pick == nil || m.overlay.pick.pick != pickOpen || string(m.overlay.pick.query) != text {
		return
	}
	p := m.overlay.pick
	p.pending = false
	if err != nil {
		m.overlay.items = nil
		m.status = m.w().T("tasks not listed: {error}", map[string]any{"error": err.Error()})
		return
	}
	var items []paletteItem
	for _, e := range tasks {
		items = append(items, m.pickItem(p, e.ID, e.Title, e.State))
	}
	m.overlay.items, m.overlay.at = items, 0
}

func (m *Model) pickItem(p *taskPick, id, title, state string) paletteItem {
	return paletteItem{label: oneLine(cmp.Or(title, id)), key: m.stateLabel(state), run: func(uint64) (Command, bool) {
		c := Command{Unit: taskUnit(p.task.ID), Label: taskLabel(p.task), Action: p.verb, task: p.task.ID, verb: p.verb, args: map[string]string{dtask.ArgTask: id}}
		return m.send(c)
	}}
}

// pickKey types into the picker: a rune or Backspace changes its text, Up and Down move, Enter runs the
// task picked, Esc closes.
func (m *Model) pickKey(k Key) (Command, bool) {
	o := m.overlay
	p := o.pick
	switch {
	case k.Name == KeyUp:
		o.at = max(o.at-1, 0)
	case k.Name == KeyDown:
		o.at = min(o.at+1, max(len(o.items)-1, 0))
	case k.Name == KeyEnter && (p.pending || len(o.items) == 0):
	case k.Name == KeyEnter:
		m.overlay = nil
		return o.items[o.at].run(k.Read)
	case k.Name == KeyEsc:
		m.overlay = nil
	case k.Name == KeyBackspace && len(p.query) > 0:
		p.query = p.query[:len(p.query)-1]
		return m.repick()
	case k.Name == "" && k.Rune != 0:
		p.query = append(p.query, k.Rune)
		return m.repick()
	}
	return Command{}, false
}

// refShown is another task a task names, as the model's own copy of it reads when it holds one, which a
// later page keeps fresh, else as the ref.
func (m *Model) refShown(r api.TaskRef) (title, state string) {
	if !r.Merged {
		if m.opened != nil && m.opened.ID == r.ID {
			return m.opened.Title, m.opened.State
		}
		for _, es := range m.tasks {
			if i := slices.IndexFunc(es, func(e api.TaskView) bool { return e.ID == r.ID }); i >= 0 {
				return es[i].Title, es[i].State
			}
		}
	}
	return r.Title, r.State
}
