package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/plugins/bundled"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
)

func listedTools(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	var got struct {
		Result struct {
			Tools []struct {
				Name        string          `json:"name"`
				InputSchema json.RawMessage `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("%v %s", err, rec.Body)
	}
	var names []string
	for _, tl := range got.Result.Tools {
		if len(tl.InputSchema) == 0 {
			t.Errorf("%s has no input schema", tl.Name)
		}
		names = append(names, tl.Name)
	}
	return names
}

func toolCallBody(name, args string) string {
	return `{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
}

// manifestTool is the bundled slack manifest's tool name.
func manifestTool(t *testing.T, name string) manifest.Tool {
	t.Helper()
	srcs, err := bundled.Sources()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range srcs {
		if i := slices.IndexFunc(s.Tools, func(m manifest.Tool) bool { return m.Name == name }); i >= 0 {
			return s.Tools[i]
		}
	}
	t.Fatalf("no bundled manifest declares %s", name)
	return manifest.Tool{}
}

type pluginCall struct {
	args    string
	session string
}

// fakePlugin answers every call of its tool with answer, or fails with err.
type fakePlugin struct {
	answer psource.Answer
	err    error
	calls  []pluginCall
}

func (f *fakePlugin) tool(t *testing.T, name string) PluginTool {
	return PluginTool{Name: name, Input: json.RawMessage(manifestTool(t, name).Input),
		Call: func(_ context.Context, args json.RawMessage, session string) (psource.Answer, error) {
			f.calls = append(f.calls, pluginCall{args: string(args), session: session})
			return f.answer, f.err
		}}
}

func TestSlackToolsComeFromTheManifest(t *testing.T) {
	for _, name := range []string{"slack_send_message", "slack_read", "slack_search", "slack_draft", "slack_react", "slack_edit"} {
		m := manifestTool(t, name)
		if !slices.ContainsFunc(builtin.Tools, func(v builtin.Value) bool { return v.Name == name && v.Doc == m.Doc }) {
			t.Errorf("%s is not the manifest's tool in builtin.Tools", name)
		}
		for _, a := range m.Answers {
			if got, ok := builtin.AnswerOf(name, a.Name); !ok || got.Doc != a.Doc || len(got.Fields) != len(a.Fields) {
				t.Errorf("%s answer %s: %+v %t, manifest %+v", name, a.Name, got, ok, a)
			}
		}
	}

	f := &fakePlugin{answer: psource.Answer{Name: "read", Fields: map[string]any{"Text": "[m1 C0000000001/1700000000.000100] hello"}}}
	s := &Server{Turns: &fakeTurns{}, Port: 47800, Texts: bundledTexts, Plugins: []PluginTool{f.tool(t, "slack_read")}}
	read := describedTools(t, s)["slack_read"]
	if !strings.HasPrefix(read.Description, "Read a Slack thread") || read.InputSchema.Properties["target"].Description == "" ||
		read.InputSchema.Properties["limit"].Minimum == nil || !slices.Equal(read.InputSchema.Required, []string{"target"}) {
		t.Fatalf("slack_read listed %+v", read)
	}
	sid := call(t, s, "", "term_1", initialize).Header().Get("Mcp-Session-Id")
	if names := listedTools(t, call(t, s, sid, "term_1", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)); !slices.Contains(names, "slack_read") {
		t.Fatalf("listed %v", names)
	}
	res := decodeResult(t, call(t, s, sid, "term_1", toolCallBody("slack_read", `{"target":"C0000000001/1700000000.000100"}`)))
	if got := res.Result.Content[0].Text; res.Result.IsError || got != "[m1 C0000000001/1700000000.000100] hello" {
		t.Fatalf("slack_read = %q (error %v)", got, res.Result.IsError)
	}
	if len(f.calls) != 1 || f.calls[0].session != "term_1" || f.calls[0].args != `{"target":"C0000000001/1700000000.000100"}` {
		t.Fatalf("calls %+v", f.calls)
	}
}

func TestAPluginsProblemsAreWordedOnePerLine(t *testing.T) {
	f := &fakePlugin{answer: psource.Answer{Name: "fix_these", IsError: true, Fields: map[string]any{"Problems": []any{
		map[string]any{"answer": "problem_target_required"},
		map[string]any{"answer": "problem_limit", "fields": map[string]any{"Max": 200}},
	}}}}
	s := &Server{Turns: &fakeTurns{}, Port: 47800, Texts: bundledTexts, Plugins: []PluginTool{f.tool(t, "slack_read")}}
	sid := call(t, s, "", "term_1", initialize).Header().Get("Mcp-Session-Id")
	res := decodeResult(t, call(t, s, sid, "term_1", toolCallBody("slack_read", `{"limit":0}`)))
	want := "Nothing was read; fix these and call slack_read again:\ntarget: required\nlimit: an integer from 1 to 200"
	if got := res.Result.Content[0].Text; !res.Result.IsError || got != want {
		t.Fatalf("fix_these = %q (error %v)", got, res.Result.IsError)
	}
}

func TestAPluginsFailureIsTheFailedAnswerRedacted(t *testing.T) {
	f := &fakePlugin{err: errors.New("slack: token xoxp-1111-fake refused")}
	s := &Server{Turns: &fakeTurns{}, Port: 47800, Texts: bundledTexts, Plugins: []PluginTool{f.tool(t, "slack_send_message")}}
	sid := call(t, s, "", "", initialize).Header().Get("Mcp-Session-Id")
	res := decodeResult(t, call(t, s, sid, "", toolCallBody("slack_send_message", `{"target":"#releases","text":"hi"}`)))
	if got := res.Result.Content[0].Text; !res.Result.IsError || got != "Slack send failed: slack: token [redacted] refused" {
		t.Fatalf("failed = %q (error %v)", got, res.Result.IsError)
	}
	if len(f.calls) != 1 || f.calls[0].session != "" {
		t.Fatalf("calls %+v", f.calls)
	}
}

func TestRedactSecretsCoversEveryKind(t *testing.T) {
	for _, tok := range []string{"xoxp-1111-fake", "xoxb-2222-fake", "xapp-1-A000-3333-fake", "xoxe-1-fake", "xoxr-fake", "xoxe.xoxp-1-fake"} {
		if got := redactSecrets("token=" + tok + " end"); got != "token=[redacted] end" {
			t.Errorf("%s: %q", tok, got)
		}
	}
}

func TestAnswersComeFromTheProfile(t *testing.T) {
	f := &fakePlugin{answer: psource.Answer{Name: "posted", Fields: map[string]any{"Conversation": "#releases", "Parent": "", "Channel": "C1",
		"TS": "1.2", "Permalink": "https://acme.slack.com/archives/C1/p1", "Text": "hi"}}}
	texts := map[string]ToolText{"slack_send_message": {Answers: map[string]string{
		"posted": "Sent to {{.Conversation}} by {{.Name}}; check it shows: {{.Permalink}}",
	}}}
	s := &Server{Turns: &fakeTurns{}, Port: 47800, Plugins: []PluginTool{f.tool(t, "slack_send_message")},
		Texts: func() map[string]ToolText { return texts }, Bundled: bundledTexts}
	sid := call(t, s, "", "term_1", initialize).Header().Get("Mcp-Session-Id")
	res := decodeResult(t, call(t, s, sid, "term_1", toolCallBody("slack_send_message", `{"target":"#releases","text":"hi"}`)))
	if got := res.Result.Content[0].Text; got != "Sent to #releases by "+brand.Name+"; check it shows: https://acme.slack.com/archives/C1/p1" {
		t.Fatalf("%q", got)
	}
	f.answer = psource.Answer{Name: "fix_these", IsError: true, Fields: map[string]any{"Problems": []any{map[string]any{"answer": "problem_text_required"}}}}
	res = decodeResult(t, call(t, s, sid, "term_1", toolCallBody("slack_send_message", `{"target":"#releases","text":" "}`)))
	if got := res.Result.Content[0].Text; !strings.HasPrefix(got, "Nothing was sent; fix these and call slack_send_message again:\ntext: required") {
		t.Fatalf("the bundled answer: %q", got)
	}
}

const unexpanded = "${ORCA_TERMINAL_HANDLE}"
