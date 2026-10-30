package builtin

// Notices are what the loop tells an agent's session, and the person about what they wrote, which
// notices.toml words, and the fields each reads besides Name and Command.
var Notices = []ToolAnswer{
	{Name: "answered", Doc: "Typed into an idle session, or the reason that blocks its stop while it works: the user answered Gate of the session's turn, 0 for an answer stored before answers named their gate, of the unit named as the loop's commands read it.",
		Fields: []AnswerField{num("Gate"), str("Unit")}},
	{Name: NoticeChat, Doc: "Typed into a live session of a task when the user writes on the task's chat, or the reason that blocks its next stop while its agent works: the task's id, its own unit as the loop's commands read it, and Capable, whether the session loaded the MCP server. The notice carries no message: the agent reads it with task_read, or with the turn answer command.",
		Fields: []AnswerField{str("Task"), str("Unit"), {Name: "Capable"}}},
	{Name: NoticeChatNotYours, Doc: "Why turn answer refuses a session that has no turn on the task it names, or on a unit the task holds: the unit as the command reads it.",
		Fields: []AnswerField{str("Unit")}},
	{Name: NoticeChatTyped, Doc: "Tells the person that their chat message reached a live session of the task."},
	{Name: NoticeChatQueued, Doc: "Tells the person that their chat message waits for the busy agent of a live session of the task, which is told of it when it stops."},
	{Name: NoticeChatStarted, Doc: "Tells the person that their chat message started a turn of Intent, Resumed when it resumes the task's last session.",
		Fields: []AnswerField{str("Intent"), {Name: "Resumed"}}},
	{Name: NoticeDraftSent, Doc: "Tells the person that the reply they sent at once, Draft, is approved and goes out.", Fields: []AnswerField{num("Draft")}},
	{Name: NoticeDraftSaved, Doc: "Tells the person that the reply they wrote, Draft, waits for their approval.", Fields: []AnswerField{num("Draft")}},
	{Name: NoticeReplyPlaceholder, Doc: "The placeholder of a notification's input that saves a reply for approval."},
	{Name: NoticeReplyButton, Doc: "The button of a notification's input that saves a reply for approval."},
	{Name: NoticeSendReplyPlaceholder, Doc: "The placeholder of a notification's input that sends a reply at once."},
	{Name: NoticeSendReplyButton, Doc: "The button of a notification's input that sends a reply at once."},
	{Name: NoticeAnswerPlaceholder, Doc: "The placeholder of a notification's input that answers an agent."},
	{Name: NoticeAnswerButton, Doc: "The button of a notification's input that answers an agent."},
	{Name: NoticeDigestTitle, Doc: "The title of the daily digest notification."},
	{Name: NoticeDigestNew, Doc: "The digest's first line: Count, the tasks of the badge's views that moved since the last digest.",
		Fields: []AnswerField{num("Count")}},
	{Name: NoticeDigestView, Doc: "The digest's line for one view the badge counts: View, its label, and Count, its tasks.",
		Fields: []AnswerField{str("View"), num("Count")}},
	{Name: NoticeNotifyInbox, Doc: "The title of the notification of a task in the Inbox that says what it is itself."},
	{Name: NoticeNotifyDraftForMe, Doc: "The button of an Inbox task's notification that delegates it to an agent headless."},
	{Name: NoticeNotifyReply, Doc: "The button of a notification whose text input saves a reply for approval, or answers an agent."},
	{Name: NoticeNotifySendReply, Doc: "The button of a notification whose text input sends a reply at once."},
	{Name: NoticeNotifyQuestion, Doc: "The title of the notification of an agent's open question."},
	{Name: NoticeNotifyProposal, Doc: "The title of the notification of an open proposal."},
	{Name: NoticeNotifyPost, Doc: "The title of the notification of an agent's open post."},
	{Name: NoticeNotifyYes, Doc: "The button that answers yes to an agent's confirmation."},
	{Name: NoticeNotifyNo, Doc: "The button that answers no to an agent's confirmation."},
	{Name: NoticeNotifyRecommended, Doc: "The button of the option an agent recommends: Label, the option's label.", Fields: []AnswerField{str("Label")}},
	{Name: NoticeNotifyDraft, Doc: "The title of the notification of a draft that waits for your approval."},
	{Name: NoticeNotifyDraftFailed, Doc: "The title of the notification of a draft whose delivery failed."},
	{Name: NoticeNotifyOpen, Doc: "The button that opens the notification's task in the cockpit."},
	{Name: NoticeNotifyDiscard, Doc: "The button that discards a draft from its notification."},
	{Name: "record_owed", Doc: "The reason that blocks the stop of a session whose turn has no record: the unit, the turn's id and the directory a record file goes in.",
		Fields: []AnswerField{str("Unit"), str("Turn"), str("ScratchDir")}},
	{Name: NoticeAttachNeedsSession, Doc: "Why task attach refuses to run outside an agent session a workspace manager started: the task named as the command reads it, and the enabled workspace managers as displayed.",
		Fields: []AnswerField{str("Task"), str("Managers")}},
}

// The notices Go names.
const (
	// NoticeAttachNeedsSession is the notice of task attach run outside a workspace manager's session.
	NoticeAttachNeedsSession   = "attach_needs_session"
	NoticeChat                 = "chat"
	NoticeChatTyped            = "chat_typed"
	NoticeChatNotYours         = "chat_not_yours"
	NoticeChatQueued           = "chat_queued"
	NoticeChatStarted          = "chat_started"
	NoticeDraftSent            = "draft_sent"
	NoticeDraftSaved           = "draft_saved"
	NoticeReplyPlaceholder     = "reply_placeholder"
	NoticeReplyButton          = "reply_button"
	NoticeSendReplyPlaceholder = "send_reply_placeholder"
	NoticeSendReplyButton      = "send_reply_button"
	NoticeAnswerPlaceholder    = "answer_placeholder"
	NoticeAnswerButton         = "answer_button"
	NoticeDigestTitle          = "digest_title"
	NoticeDigestNew            = "digest_new"
	NoticeDigestView           = "digest_view"
	NoticeNotifyInbox          = "notify_inbox"
	NoticeNotifyDraftForMe     = "notify_draft_for_me"
	NoticeNotifyReply          = "notify_reply"
	NoticeNotifySendReply      = "notify_send_reply"
	NoticeNotifyQuestion       = "notify_question"
	NoticeNotifyProposal       = "notify_proposal"
	NoticeNotifyPost           = "notify_post"
	NoticeNotifyYes            = "notify_yes"
	NoticeNotifyNo             = "notify_no"
	NoticeNotifyRecommended    = "notify_recommended"
	NoticeNotifyDraft          = "notify_draft"
	NoticeNotifyDraftFailed    = "notify_draft_failed"
	NoticeNotifyOpen           = "notify_open"
	NoticeNotifyDiscard        = "notify_discard"
)

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
