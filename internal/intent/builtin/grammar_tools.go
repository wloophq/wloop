package builtin

import (
	"io"
	"text/template"
)

// Tools are the tools of the loop's MCP server, which tools.toml describes,
// one table each.
var Tools = []Value{
	{Name: "turn_end", Doc: "Records how the session's turn ended."},
	{Name: "inbox_post", Doc: "Posts a question or a note to the user's Inbox during a turn."},
	{Name: "slack_send_message", Doc: "Posts to Slack as the user, now."},
	{Name: "slack_read", Doc: "Reads a Slack thread or conversation."},
	{Name: "slack_search", Doc: "Searches the Slack messages the user can read."},
	{Name: "slack_draft", Doc: "Proposes a Slack message or reaction for the user's approval."},
	{Name: "slack_react", Doc: "Adds or removes a reaction on a Slack message as the user, now."},
	{Name: "slack_edit", Doc: "Replaces the text of one of the user's Slack messages, now."},
}

// ToolData is what every tools.toml text reads.
type ToolData struct {
	// Name is the product's name, Command its command.
	Name, Command string
	// PostsPerTurn is how many inbox_post messages of a turn wait at once.
	PostsPerTurn int
}

// Map is d as the fields of a template.
func (d ToolData) Map() map[string]any {
	return map[string]any{"Name": d.Name, "Command": d.Command, "PostsPerTurn": d.PostsPerTurn}
}

// ToolAnswer is a text a tool answers with, by name, and the fields it
// reads besides the ToolData's.
type ToolAnswer struct {
	Tool, Name, Doc string
	Fields          []AnswerField
}

// AnswerField is a field an answer reads: a string, or a number when Number.
type AnswerField struct {
	Name   string
	Number bool
}

func str(name string) AnswerField { return AnswerField{Name: name} }

func num(name string) AnswerField { return AnswerField{Name: name, Number: true} }

// answersOf are the answers every tool of names shares.
func answersOf(names []string) []ToolAnswer {
	var out []ToolAnswer
	for _, n := range names {
		out = append(out,
			ToolAnswer{Tool: n, Name: "refused", Doc: "The call is refused before the tool runs, such as with no session.", Fields: []AnswerField{str("Error")}},
			ToolAnswer{Tool: n, Name: "bad_arguments", Doc: "The arguments do not decode; Error is the decoder's, \"\" when there is none.", Fields: []AnswerField{str("Error")}},
			ToolAnswer{Tool: n, Name: "fix_these", Doc: "The arguments have problems, one per line.", Fields: []AnswerField{str("Problems")}},
		)
	}
	return out
}

func slackAnswers(names ...string) []ToolAnswer {
	var out []ToolAnswer
	for _, n := range names {
		out = append(out,
			ToolAnswer{Tool: n, Name: "not_logged_in", Doc: "Slack is logged out."},
			ToolAnswer{Tool: n, Name: "failed", Doc: "Slack or the daemon failed the call.", Fields: []AnswerField{str("Error")}},
		)
	}
	return out
}

func answer(tool, name, doc string, fields ...AnswerField) ToolAnswer {
	return ToolAnswer{Tool: tool, Name: name, Doc: doc, Fields: fields}
}

// ToolAnswers are the texts each tool answers with, which tools.toml words
// under [<tool>.answers].
var ToolAnswers = func() []ToolAnswer {
	slackTools := []string{"slack_send_message", "slack_read", "slack_search", "slack_draft", "slack_react", "slack_edit"}
	out := answersOf(append([]string{"turn_end", "inbox_post"}, slackTools...))
	out = append(out, slackAnswers(slackTools...)...)
	return append(out,
		answer("turn_end", "problem_extra_arguments", "A problem: arguments besides turn and record.", str("Keys")),
		answer("turn_end", "problem_turn_type", "A problem: turn is not a string."),
		answer("turn_end", "problem_turn_required", "A problem: turn is missing or empty."),
		answer("turn_end", "problem_record_required", "A problem: record is missing."),
		answer("turn_end", "problem_record", "A problem: record does not decode.", str("Error")),
		answer("turn_end", "refused_record", "The turn refused the record.", str("Reason")),
		answer("turn_end", "store_failed", "The record could not be stored.", str("Error")),
		answer("turn_end", "recorded", "The record is stored: its state and unit, a note and what was dropped when any, and the ledger's outcome, \"\" when none.",
			str("State"), str("Unit"), str("Note"), str("Dropped"), str("Ledger")),
		answer("turn_end", "ledger_applied", "A part of the ledger's outcome: ops applied.", num("Count")),
		answer("turn_end", "ledger_unchanged", "A part of the ledger's outcome: ops that changed nothing.", num("Count")),
		answer("turn_end", "ledger_proposed", "A part of the ledger's outcome: ops proposed to the user.", num("Count")),
		answer("turn_end", "ledger_refused", "A part of the ledger's outcome: ops refused, and why.", num("Count"), str("Why")),
		answer("turn_end", "ledger_refusal", "Why one op was refused.", num("Index"), str("Reason")),

		answer("inbox_post", "text_required", "The text is missing."),
		answer("inbox_post", "capped", "The turn has too many messages waiting.", str("Error")),
		answer("inbox_post", "not_in_turn", "The session has no started turn.", str("Error")),
		answer("inbox_post", "post_failed", "The message could not be posted.", str("Error")),
		answer("inbox_post", "posted", "The message is in the Inbox.", num("ID")),

		answer("slack_send_message", "problem_target_required", "A problem: target is missing."),
		answer("slack_send_message", "problem_text_required", "A problem: text is missing or empty."),
		answer("slack_send_message", "broadcast", "The text notifies a whole channel or group."),
		answer("slack_send_message", "posted", "The message is posted: where, its permalink, \"\" when Slack gave none, and its text as posted.",
			str("Conversation"), str("Parent"), str("Permalink"), str("Channel"), str("TS"), str("Text")),

		answer("slack_read", "problem_target_required", "A problem: target is missing."),
		answer("slack_read", "problem_limit", "A problem: limit is out of range.", num("Max")),

		answer("slack_search", "problem_query_required", "A problem: query is missing or empty."),
		answer("slack_search", "problem_after", "A problem: after is not a date."),
		answer("slack_search", "problem_before", "A problem: before is not a date."),
		answer("slack_search", "problem_before_after", "A problem: before is not after after."),
		answer("slack_search", "problem_limit", "A problem: limit is out of range.", num("Max")),

		answer("slack_draft", "problem_kind", "A problem: kind is none of the kinds.", str("Kinds")),
		answer("slack_draft", "problem_reaction_required", "A problem: a react draft has no reaction."),
		answer("slack_draft", "problem_text_required", "A problem: text is missing or empty."),
		answer("slack_draft", "proposed", "The draft waits for the user.", num("ID"), str("Unit"), str("Where")),
		answer("slack_draft", "approved", "The draft's policy approved it.", num("ID"), str("Unit"), str("Where")),
		answer("slack_draft", "dry_run", "The draft's policy only stores it.", num("ID"), str("Unit"), str("Where")),

		answer("slack_react", "problem_permalink", "A problem: permalink is not a message permalink."),
		answer("slack_react", "problem_emoji_required", "A problem: emoji is missing or empty."),
		answer("slack_react", "reacted", "The reaction is added.", str("Emoji"), str("Conversation"), str("Permalink")),
		answer("slack_react", "removed", "The reaction is removed.", str("Emoji"), str("Conversation"), str("Permalink")),
		answer("slack_react", "already", "The message already had the user's reaction.", str("Emoji"), str("Conversation"), str("Permalink")),
		answer("slack_react", "absent", "The message had no such reaction of the user.", str("Emoji"), str("Conversation"), str("Permalink")),

		answer("slack_edit", "problem_permalink", "A problem: permalink is not a message permalink."),
		answer("slack_edit", "problem_text_required", "A problem: text is missing or empty."),
		answer("slack_edit", "broadcast", "The text notifies a whole channel or group."),
		answer("slack_edit", "edited", "The message is edited: where, its new text as posted and the text it replaced.", str("Conversation"), str("Permalink"), str("Text"), str("Replaced")),
	)
}()

// AnswerOf is tool's answer name, false when the tool has none.
func AnswerOf(tool, name string) (ToolAnswer, bool) {
	for _, a := range ToolAnswers {
		if a.Tool == tool && a.Name == name {
			return a, true
		}
	}
	return ToolAnswer{}, false
}

// toolsGrammar is the keys of tools.toml.
var toolsGrammar = []Key{
	{Path: "*", In: SurfaceTools, Kind: KindTable, Values: func() []Value { return Tools }, Doc: "An MCP tool, by its name."},
	{Path: "*.description", In: SurfaceTools, Kind: KindTemplate, Required: true, Doc: "What the tool does and when to call it, over the tool data. Required."},
	{Path: "*.params", In: SurfaceTools, Kind: KindMap, Doc: "A parameter's description, by its path: a nested one as \"record.state\", an array's element as \"items[]\"."},
	{Path: "*.params.*", In: SurfaceTools, Kind: KindTemplate, Doc: "The parameter's description, over the tool data."},
	{Path: "*.answers", In: SurfaceTools, Kind: KindMap, Doc: "What the tool answers the agent, by answer name; the reference lists each tool's answers and the fields they read."},
	{Path: "*.answers.*", In: SurfaceTools, Kind: KindTemplate, Doc: "The answer, over the tool data and its fields."},
}

// AnswerSamples are the data a check renders an answer over, the tool
// data's included: every field set, then every field empty, so that both
// branches of a condition on a field run.
func AnswerSamples(fields []AnswerField) []map[string]any {
	full, empty := ToolData{Name: "x", Command: "x", PostsPerTurn: 1}.Map(), ToolData{}.Map()
	for _, f := range fields {
		if f.Number {
			full[f.Name], empty[f.Name] = 1, 0
		} else {
			full[f.Name], empty[f.Name] = "x", ""
		}
	}
	return []map[string]any{full, empty}
}

// RenderToolText writes the tools.toml text src, named name, over data.
func RenderToolText(w io.Writer, name, src string, data map[string]any) error {
	t, err := template.New(name).Option("missingkey=error").Parse(src)
	if err != nil {
		return err
	}
	return t.Execute(w, data)
}
