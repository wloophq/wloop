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
	tools, problems := loadTools(profile.BundledChain(), os.DirFS("../../profiles/default"))
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
		b.WriteString("[" + v.Name + "]\ndescription = \"" + v.Name + " does it\"\n[" + v.Name + ".answers]\n")
		for _, a := range builtin.ToolAnswers {
			if a.Tool == v.Name {
				b.WriteString(a.Name + " = \"" + a.Name + "\"\n")
			}
		}
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
		"unknown tool":   {"[slack_raed]\ndescription = \"x\"\n", `slack_raed: names no tool; did you mean "slack_read"?`},
		"empty":          {"[slack_read]\ndescription = \"\"\n", "slack_read.description: is required"},
		"bad template":   {"[slack_read]\ndescription = \"{{.Nme}}\"\n", "slack_read.description"},
		"bad param":      {"[slack_read.params]\nlimit = \"{{if}}\"\n", "slack_read.params.limit"},
		"unknown answer": {"[slack_read.answers]\nfailled = \"x\"\n", `slack_read.answers.failled: names no answer of slack_read; did you mean "failed"?`},
		"unknown field":  {"[slack_read.answers]\nfailed = \"{{.Eror}}\"\n", "slack_read.answers.failed"},
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
	_, got := toolsProblems(t, strings.ReplaceAll(everyTool(""), "[inbox_read", "[inbox_read_x"), "")
	if !strings.Contains(got, "inbox_read: is missing") {
		t.Fatalf("got:\n%s", got)
	}
}

func TestTheSlackSendToolTellsTheAgentToAskWhenUnsure(t *testing.T) {
	tools, _ := loadTools(profile.BundledChain(), os.DirFS("../../profiles/default"))
	d := tools["slack_send_message"].Description
	if !strings.Contains(d, "without their approval") || !strings.Contains(d, "ask them first") {
		t.Fatalf("description %q", d)
	}
}

func TestAnAnswerReadsItsFields(t *testing.T) {
	tools, got := toolsProblems(t, everyTool(""), "[slack_read.answers]\nfailed = \"read failed: {{.Error}} ({{.Name}})\"\n")
	if got != "" || tools["slack_read"].Answers["failed"] != "read failed: {{.Error}} ({{.Name}})" {
		t.Fatalf("%q %+v", got, tools["slack_read"].Answers)
	}
	_, got = toolsProblems(t, strings.Replace(everyTool(""), "posted = \"posted\"\n", "", 1), "")
	if !strings.Contains(got, ".answers.posted: is missing") {
		t.Fatalf("a missing answer: %s", got)
	}
}

func TestAnAnswerIsCheckedWithItsFieldsTypesAndBothBranches(t *testing.T) {
	for name, c := range map[string]struct {
		user string
		ok   bool
	}{
		"a number compared":    {"[turn_end.answers]\nledger_applied = \"{{.Count}} op{{if ne .Count 1}}s{{end}}\"\n", true},
		"a typo in an else":    {"[slack_send_message.answers]\nposted = \"{{if .Permalink}}{{.Permalink}}{{else}}{{.Chanel}}{{end}}\"\n", false},
		"a len of a number":    {"[turn_end.answers]\nledger_applied = \"{{len .Count}}\"\n", false},
		"an empty-text branch": {"[slack_send_message.answers]\nposted = \"{{with .Parent}}in {{.}}{{else}}top level{{end}}\"\n", true},
	} {
		t.Run(name, func(t *testing.T) {
			if _, got := toolsProblems(t, everyTool(""), c.user); (got == "") != c.ok {
				t.Fatalf("problems %q, want ok %v", got, c.ok)
			}
		})
	}
}

func TestAPluginsToolsAreWordedByItsLayer(t *testing.T) {
	core := everyTool("")
	core = core[:strings.Index(core, "[slack_")]
	plugin := t.TempDir()
	if err := os.WriteFile(filepath.Join(plugin, ToolsFile), []byte("[slack_read]\ndescription = \"read it\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bundled := fstest.MapFS{ToolsFile: {Data: []byte(core)}}
	if _, problems := loadTools(nil, bundled); len(problems) > 0 {
		t.Errorf("a plugin left out of the chain needs no tables: %s", problems.Error())
	}
	tools, problems := loadTools([]profile.Layer{{Name: profile.PluginLayer("slack"), Dir: plugin}}, bundled)
	if tools["slack_read"].Description != "read it" {
		t.Errorf("slack_read %+v", tools["slack_read"])
	}
	if got := problems.Error(); !strings.Contains(got, "slack_edit: is missing") || !strings.Contains(got, "slack_read.answers.refused: is missing") {
		t.Errorf("the plugin's layer leaves its tools unworded:\n%s", got)
	}
}
