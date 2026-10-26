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
	{Name: "slack_react", Doc: "Proposes a reaction on a Slack message for the user's approval."},
	{Name: "slack_edit", Doc: "Proposes an edit of one of the user's Slack messages for the user's approval."},
}

// ToolData is what a tools.toml text reads.
type ToolData struct {
	// Name is the product's name, Command its command.
	Name, Command string
	// PostsPerTurn is how many inbox_post messages of a turn wait at once.
	PostsPerTurn int
}

// toolsGrammar is the keys of tools.toml.
var toolsGrammar = []Key{
	{Path: "*", In: SurfaceTools, Kind: KindTable, Values: func() []Value { return Tools }, Doc: "An MCP tool, by its name."},
	{Path: "*.description", In: SurfaceTools, Kind: KindTemplate, Required: true, Doc: "What the tool does and when to call it, over the tool data. Required."},
	{Path: "*.params", In: SurfaceTools, Kind: KindMap, Doc: "A parameter's description, by its path: a nested one as \"record.state\", an array's element as \"items[]\"."},
	{Path: "*.params.*", In: SurfaceTools, Kind: KindTemplate, Doc: "The parameter's description, over the tool data."},
}

// RenderToolText writes the tools.toml text src, named name, over d.
func RenderToolText(w io.Writer, name, src string, d ToolData) error {
	t, err := template.New(name).Option("missingkey=error").Parse(src)
	if err != nil {
		return err
	}
	return t.Execute(w, d)
}
