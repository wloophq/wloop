package intent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/profile"
)

func TestEveryToolHasADescription(t *testing.T) {
	tools, problems := loadTools(nil, os.DirFS("../../profiles/default"))
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	for _, v := range builtin.Tools {
		if tools[v.Name].Description == "" {
			t.Errorf("%s has no description", v.Name)
		}
	}
}

func toolsProblems(t *testing.T, bundled, user string) (map[string]ToolText, string) {
	t.Helper()
	var layers []profile.Layer
	if user != "" {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ToolsFile), []byte(user), 0o600); err != nil {
			t.Fatal(err)
		}
		layers = []profile.Layer{{Name: "user", Dir: dir}}
	}
	tools, problems := loadTools(layers, fstest.MapFS{ToolsFile: {Data: []byte(bundled)}})
	return tools, problems.Error()
}

func everyTool(extra string) string {
	var b strings.Builder
	for _, v := range builtin.Tools {
		b.WriteString("[" + v.Name + "]\ndescription = \"" + v.Name + " does it\"\n")
	}
	return b.String() + extra
}

func TestAUserToolTextReplacesTheBundledKeyByKey(t *testing.T) {
	bundled := everyTool("[slack_read.params]\ntarget = \"what to read\"\nlimit = \"how many\"\n")
	tools, got := toolsProblems(t, bundled, "[slack_read.params]\nlimit = \"at most {{.Name}}\"\n")
	if got != "" {
		t.Fatal(got)
	}
	r := tools["slack_read"]
	if r.Description != "slack_read does it" || r.Params["target"] != "what to read" || r.Params["limit"] != "at most {{.Name}}" {
		t.Fatalf("slack_read %+v", r)
	}
}

func TestAToolTextFailsAtLoad(t *testing.T) {
	for name, c := range map[string]struct{ user, want string }{
		"unknown tool": {"[slack_raed]\ndescription = \"x\"\n", `slack_raed: names no tool; did you mean "slack_read"?`},
		"empty":        {"[slack_read]\ndescription = \"\"\n", "slack_read.description: is required"},
		"bad template": {"[slack_read]\ndescription = \"{{.Nme}}\"\n", "slack_read.description"},
		"bad param":    {"[slack_read.params]\nlimit = \"{{if}}\"\n", "slack_read.params.limit"},
	} {
		t.Run(name, func(t *testing.T) {
			tools, got := toolsProblems(t, everyTool(""), c.user)
			if !strings.Contains(got, c.want) {
				t.Fatalf("no %q in:\n%s", c.want, got)
			}
			if tools["slack_read"].Description != "slack_read does it" {
				t.Fatalf("a failing user text replaced the bundled one: %+v", tools["slack_read"])
			}
		})
	}
}

func TestABundledFileMissingAToolFails(t *testing.T) {
	_, got := toolsProblems(t, strings.Replace(everyTool(""), "[slack_edit]", "[slack_edit_x]", 1), "")
	if !strings.Contains(got, "slack_edit: is missing") {
		t.Fatalf("got:\n%s", got)
	}
}

func TestTheSlackSendToolTellsTheAgentToAskWhenUnsure(t *testing.T) {
	tools, _ := loadTools(nil, os.DirFS("../../profiles/default"))
	d := tools["slack_send_message"].Description
	if !strings.Contains(d, "without their approval") || !strings.Contains(d, "ask them first") {
		t.Fatalf("description %q", d)
	}
}
