package builtin

import (
	"io"
	"text/template"
)

// Tools are the tools of the loop's MCP server, which tools.toml describes,
// one table each.
var Tools = []Value{
	{Name: "turn_end", Doc: "Records how the session's turn ended."},
	{Name: "task_post", Doc: "Posts a question or a note on the session's task during a turn, waiting on a gate."},
	{Name: "task_read", Doc: "Reads the session's task: the task, its brief, its open gates, the chat since the last read and the user's answers, and marks them read."},
}

// ToolData is what every tools.toml text reads.
type ToolData struct {
	// Name is the product's name, Command its command.
	Name, Command string
	// PostsPerTurn is how many task_post messages of a turn wait at once.
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

func answer(tool, name, doc string, fields ...AnswerField) ToolAnswer {
	return ToolAnswer{Tool: tool, Name: name, Doc: doc, Fields: fields}
}

// ToolAnswers are the texts each tool answers with, which tools.toml words
// under [<tool>.answers].
var ToolAnswers = func() []ToolAnswer {
	out := answersOf([]string{"turn_end", "task_post", "task_read"})
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

		answer("task_post", "text_required", "The text is missing."),
		answer("task_post", "capped", "The turn has too many posts waiting.", str("Error")),
		answer("task_post", "not_in_turn", "The session has no started turn.", str("Error")),
		answer("task_post", "post_failed", "The message could not be posted.", str("Error")),
		answer("task_post", "posted", "The message is on the task, waiting on gate Gate.", num("Gate")),

		answer("task_read", "no_turn", "The session has no turn.", str("Error")),
		answer("task_read", "no_gate", "No answer of the session's resolved gate ID.", num("ID")),
		answer("task_read", "read_failed", "The task could not be read.", str("Error")),
		answer("task_read", "nothing", "Nothing to read: the session's turns are on no task, and no answer of gate ID, or of any gate when ID is 0, waits.", num("ID")),
		answer("task_read", "read", "What was read: the task, its brief record's data, its open gates, the chat since the last read and the conversations, each \"\" when empty.",
			str("Task"), str("Brief"), str("Gates"), str("Chat"), str("Conversations")),
		answer("task_read", "task", "A part of read: the task's id, title and state.", str("ID"), str("Title"), str("State")),
		answer("task_read", "gate", "A part of read: one open gate of the task, its kind, its name in gates.toml and the intents that resolve it.",
			num("ID"), str("Kind"), str("Name"), str("Intents")),
		answer("task_read", "chat_message", "A part of read: one message the user wrote on the task's chat, and when.", str("Text"), str("At")),
		answer("task_read", "conversation", "A part of read: a message, the gate its answers resolved (0 for one stored before answers named their gate), its kind (question or agent_post), its text and options, and its answers.",
			num("Gate"), str("Kind"), str("Text"), str("Options"), str("Answers")),
		answer("task_read", "turn_answers", "A part of read: answers to the session's turns stored before each answer named its message.", str("Answers")),
		answer("task_read", "option", "A part of conversation: one option of the message.", str("ID"), str("Label")),
		answer("task_read", "answer", "A part of conversation: one answer, with its option and the option's label, yes or no, its text and when it was given.",
			str("Option"), str("Label"), str("Choice"), str("Text"), str("At")),
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
