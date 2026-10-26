package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/wloophq/wloop/internal/api"
)

// draftVerbs are the entry verbs of a draft's actions.
var draftVerbs = map[string]string{"draft.approve": "approve", "draft.edit": "edit", "draft.discard": "dismiss", "draft.retry": "retry"}

// SetDrafts shows the drafts made for entry id while its detail is open.
func (m *Model) SetDrafts(id string, ds []api.Draft) {
	if id != m.detailID {
		return
	}
	m.drafts, m.draftsFor = ds, id
	if !slices.ContainsFunc(m.listedDrafts(), func(d api.Draft) bool { return d.ID == m.picked }) {
		m.picked = 0
	}
}

// listedDrafts are the drafts the open detail lists: those made for its entry but the one it shows itself.
func (m *Model) listedDrafts() []api.Draft {
	if m.detailID == "" || m.draftsFor != m.detailID {
		return nil
	}
	shown := int64(0)
	if m.opened != nil && m.opened.Draft != nil {
		shown = m.opened.Draft.ID
	}
	return slices.DeleteFunc(slices.Clone(m.drafts), func(d api.Draft) bool { return d.ID == shown })
}

// pickDraft takes a digit on an open detail that lists drafts and asks no question as the pick of that
// draft, which the draft keys then act on.
func (m *Model) pickDraft(k Key) bool {
	if k.Rune < '1' || k.Rune > '9' || m.detailID == "" || k.Read <= m.detailRead {
		return false
	}
	if _, ok := m.activeCard(); ok {
		return false
	}
	ds := m.listedDrafts()
	n := int(k.Rune - '0')
	if len(ds) == 0 {
		return false
	}
	if n > len(ds) {
		m.status = fmt.Sprintf("no draft %d", n)
		return true
	}
	m.picked = ds[n-1].ID
	m.status = fmt.Sprintf("draft %d picked: a approves, i edits, Backspace discards, Esc lets go", n)
	return true
}

func (m *Model) pickedDraft() (api.Draft, bool) {
	i := slices.IndexFunc(m.listedDrafts(), func(d api.Draft) bool { return d.ID == m.picked })
	if m.picked == 0 || i < 0 {
		return api.Draft{}, false
	}
	return m.listedDrafts()[i], true
}

// draftEntry is the Outbox entry of d, its verbs read from the draft's actions.
func draftEntry(d api.Draft, title string) api.EntryView {
	e := api.EntryView{ID: "draft:" + strconv.FormatInt(d.ID, 10), Kind: "draft", Title: title, Draft: &d}
	for _, a := range d.Actions {
		if v, ok := draftVerbs[a.ID]; ok {
			e.Verbs = append(e.Verbs, api.VerbState{Verb: v, Enabled: a.Enabled, Reason: a.Reason})
		}
	}
	return e
}

// pickedVerb runs verb on the picked draft as on an Outbox entry: approve and retry after a y with the
// revision on screen, discard after a y, edit through the text prompt.
func (m *Model) pickedVerb(e api.EntryView, d api.Draft, verb string, read uint64) (Command, bool) {
	de := draftEntry(d, e.Title)
	if i := slices.IndexFunc(de.Verbs, func(v api.VerbState) bool { return v.Verb == verb }); i < 0 || !de.Verbs[i].Enabled {
		reason := "not offered on a draft"
		if i >= 0 {
			reason = de.Verbs[i].Reason
		}
		m.status = verb + ": " + reason
		return Command{}, false
	}
	c := Command{Unit: entryUnit(de.ID), Label: entryLabel(e), Action: verb, entry: de.ID, verb: verb}
	target := draftTarget(de)
	switch verb {
	case "edit":
		return m.editPrompt(c, d, read)
	case "dismiss":
		m.confirm(c, "discard the draft to "+target+"?", read)
	case "retry":
		c.args = map[string]string{"rev": strconv.FormatInt(d.Rev, 10)}
		m.confirm(c, "post again to "+target+"?", read)
	default:
		c.args = map[string]string{"rev": strconv.FormatInt(d.Rev, 10), "text": d.Text}
		m.confirm(c, "post to "+target+"?", read)
	}
	return Command{}, false
}

// editPrompt asks for a draft's new text on the bottom line, filled with its text; Ctrl+E edits it in the
// editor, as a text of several lines always is.
func (m *Model) editPrompt(c Command, d api.Draft, read uint64) (Command, bool) {
	submit := func(text string) (Command, bool) {
		if text == "" {
			m.status = "edit " + c.Label + " cancelled: nothing typed"
			return Command{}, false
		}
		c.args = map[string]string{"text": text}
		return m.send(c)
	}
	if strings.Contains(d.Text, "\n") {
		m.edit = &EditRequest{Label: "edit", Text: d.Text, submit: submit}
		return Command{}, false
	}
	m.prompt = &prompt{action: "edit", label: "edit the draft to " + draftTarget(api.EntryView{Draft: &d}) + " (Ctrl+E: editor)", text: []rune(d.Text),
		read: read, submit: submit, editor: true}
	return Command{}, false
}

// delegatePrompt asks for the delegate's guidance, empty allowed, then runs it as the action chooses
// (headless when it can); Tab hands it to an agent in a session instead, unless the entry has nowhere
// to run one, which the label says.
func (m *Model) delegatePrompt(e api.EntryView, c Command, confirm string, read uint64) (Command, bool) {
	name := entryLabel(e)
	labels := map[string]string{
		"":        "delegate " + name + ": guidance (optional; Tab: hand to an agent)",
		"session": "hand " + name + " to an agent: guidance (optional; Tab: draft for me)",
	}
	off := sessionOff(e)
	if off != "" {
		labels[""] = "delegate " + name + ": guidance (optional; no agent session: " + off + ")"
	}
	p := &prompt{action: "delegate", label: labels[""], read: read, editor: true}
	mode := ""
	p.tab = func(p *prompt) {
		if off != "" {
			return
		}
		mode = map[string]string{"": "session", "session": ""}[mode]
		p.label = labels[mode]
	}
	p.submit = func(text string) (Command, bool) {
		c.args = nil
		for k, v := range map[string]string{"guidance": text, "mode": mode} {
			if v == "" {
				continue
			}
			if c.args == nil {
				c.args = map[string]string{}
			}
			c.args[k] = v
		}
		if confirm != "" {
			m.confirm(c, confirm, read)
			return Command{}, false
		}
		return m.send(c)
	}
	m.prompt = p
	return Command{}, false
}

// sessionOff is why the entry's delegate cannot hand it to an agent in a session, "" when it can or the
// daemon says nothing of its modes.
func sessionOff(e api.EntryView) string {
	for _, v := range e.Verbs {
		if v.Verb != "delegate" {
			continue
		}
		for _, md := range v.Modes {
			if md.Mode == "session" && !md.Enabled {
				return md.Reason
			}
		}
		if len(v.Modes) > 0 && !slices.ContainsFunc(v.Modes, func(md api.ModeState) bool { return md.Mode == "session" }) {
			return "the action does not run a session"
		}
	}
	return ""
}

// replyPrompt asks for a reply in the entry's conversation, saved to the Outbox; Tab sends it at once,
// after a y, and Ctrl+E writes it in the editor.
func (m *Model) replyPrompt(e api.EntryView, c Command, read uint64) (Command, bool) {
	name := entryLabel(e)
	labels := map[bool]string{
		false: "reply on " + name + ", saved to the Outbox (Tab: send now; Ctrl+E: editor)",
		true:  "reply on " + name + ", sent now (Tab: save to the Outbox; Ctrl+E: editor)",
	}
	send := false
	p := &prompt{action: "reply", label: labels[false], read: read, editor: true}
	p.tab = func(p *prompt) {
		send = !send
		p.label = labels[send]
	}
	p.submit = func(text string) (Command, bool) {
		if text == "" {
			m.status = "reply " + c.Label + " cancelled: nothing typed"
			return Command{}, false
		}
		c.args = map[string]string{"text": text}
		if !send {
			return m.send(c)
		}
		c.args["send"] = "now"
		m.confirm(c, "post to "+replyTarget(e)+" now?", read)
		return Command{}, false
	}
	m.prompt = p
	return Command{}, false
}

// replyTarget names where an entry's reply goes: its first unit.
func replyTarget(e api.EntryView) string {
	if len(e.Units) > 0 {
		return e.UnitLabel(0)
	}
	return entryLabel(e)
}

// draftLines adds the drafts listed under the open detail, numbered for the digits that pick them.
func (m *Model) draftLines(fact func(label, text string, style func(string) string), para func(label, text string)) {
	t := m.t
	for i, d := range m.listedDrafts() {
		head := fmt.Sprintf("%d. %s · %s", i+1, d.Purpose, d.State)
		if d.Origin.Kind != "" {
			head += " · from " + d.Origin.Kind
		}
		style := t.Dim
		if d.ID == m.picked {
			head, style = "› "+head, t.Bold
		}
		label := ""
		if i == 0 {
			label = "drafts"
		}
		fact(label, head, style)
		text := d.Text
		if d.Reaction != "" {
			text = ":" + d.Reaction + ":"
		}
		para("", text)
		if d.Error != "" {
			fact("", d.Error, t.Red)
		}
	}
}
