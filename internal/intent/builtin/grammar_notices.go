package builtin

// Notices are what the loop tells an agent's session, which notices.toml words, and the fields each reads
// besides Name and Command.
var Notices = []ToolAnswer{
	{Name: "answered", Doc: "Typed into an idle session, or the reason that blocks its stop while it works: the user answered an Inbox message, Message, 0 for an answer stored before answers named their message, of the unit named as the loop's commands read it.",
		Fields: []AnswerField{num("Message"), str("Unit")}},
	{Name: "record_owed", Doc: "The reason that blocks the stop of a session whose turn has no record: the unit, the turn's id and the directory a record file goes in.",
		Fields: []AnswerField{str("Unit"), str("Turn"), str("ScratchDir")}},
	{Name: NoticeAttachNeedsSession, Doc: "Why task attach refuses to run outside an agent session a workspace manager started: the task named as the command reads it, and the enabled workspace managers as displayed.",
		Fields: []AnswerField{str("Task"), str("Managers")}},
}

// NoticeAttachNeedsSession is the notice of task attach run outside a workspace manager's session.
const NoticeAttachNeedsSession = "attach_needs_session"

// NoticeOf is the notice name, false when there is none.
func NoticeOf(name string) (ToolAnswer, bool) {
	for _, n := range Notices {
		if n.Name == name {
			return n, true
		}
	}
	return ToolAnswer{}, false
}

// noticesGrammar is the keys of notices.toml.
var noticesGrammar = []Key{
	{Path: "*", In: SurfaceNotices, Kind: KindTemplate, Values: func() []Value {
		out := make([]Value, len(Notices))
		for i, n := range Notices {
			out[i] = Value{Name: n.Name, Doc: n.Doc}
		}
		return out
	}, Doc: "A notice, by its name: a template over its fields, `Name` and `Command`."},
}
