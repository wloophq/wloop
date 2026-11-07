package api

// Why codes of an OmniboxDropped the read-only agent call writes; each is a key of the client strings, worded
// with the drop's Params.
const (
	OmniboxDropUnknownOperation    = "unknown_operation"
	OmniboxDropIncompleteOperation = "incomplete_operation"
	OmniboxDropUnknownTask         = "unknown_task"
	OmniboxDropUnknownUnit         = "unknown_unit"
	OmniboxDropOutsideVocabulary   = "outside_vocabulary"
)

// The errors OmniboxAskPath answers, keys of the client strings the daemon words in the loaded locale.
const (
	OmniboxAskEmpty    = "Type what to ask the agent"
	OmniboxAskOverCap  = "Today's spend cap for agent calls is reached; ask again tomorrow"
	OmniboxAskNoAgent  = "No agent here can answer from the omnibox: {error}"
	OmniboxAskFailed   = "The agent could not answer: {error}"
	OmniboxAskTimedOut = "The agent took too long to answer; nothing changed"
	OmniboxAskUnwired  = "This daemon runs no omnibox agent"
)
