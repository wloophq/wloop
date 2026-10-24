package claude

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/usage"
)

const answerJSON = `{"goal":"g"}`

func envelopeJSON(structured, result string, isError bool) string {
	s := `{"type":"result","is_error":` + map[bool]string{true: "true", false: "false"}[isError] + `,"duration_ms":4200,"total_cost_usd":0.011,"result":` + result
	if structured != "" {
		s += `,"structured_output":` + structured
	}
	return s + "}"
}

func TestTiersRunOnTheFamilyAliasesUnlessPinned(t *testing.T) {
	for tier, want := range map[turns.Tier]string{turns.TierFast: "haiku", turns.TierBalanced: "sonnet", turns.TierFrontier: "opus", "": ""} {
		if got := Model(tier, nil); got != want {
			t.Errorf("%q runs on %q, want %q", tier, got, want)
		}
	}
	if got := Model(turns.TierFast, map[turns.Tier]string{turns.TierFast: "claude-haiku-5-5"}); got != "claude-haiku-5-5" {
		t.Errorf("pinned fast runs on %q", got)
	}
}

func TestAskRunsWithoutToolsSettingsOrSessionAndPromptOnStdin(t *testing.T) {
	var gotArgs, gotEnv []string
	var gotStdin, gotDir, gotBin string
	work := filepath.Join(t.TempDir(), "brief")
	sh := func(_ context.Context, dir string, env []string, stdin, name string, args ...string) ([]byte, error) {
		gotDir, gotEnv, gotStdin, gotBin, gotArgs = dir, env, stdin, name, args
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("working directory missing: %v", err)
		}
		return []byte(envelopeJSON(answerJSON, `""`, false)), nil
	}
	a, err := Agent{AskSh: sh, UserHome: "/u"}.Asker(work, map[turns.Tier]string{turns.TierBalanced: "pinned"})
	if err != nil {
		t.Fatal(err)
	}
	ans, err := a.Ask(context.Background(), turns.Ask{Home: "/p", Tier: turns.TierFast, Prompt: "the prompt", Schema: `{"type":"object"}`, MaxCostUSD: 0.25})
	if err != nil || string(ans.JSON) != answerJSON || ans.Model != "haiku" || ans.CostUSD == nil || *ans.CostUSD != 0.011 || ans.Duration.Milliseconds() != 4200 {
		t.Fatalf("%+v %v", ans, err)
	}
	joined := strings.Join(gotArgs, " ")
	for _, want := range []string{"-p", "--model haiku", "--output-format json", "--no-session-persistence", "--strict-mcp-config", "--setting-sources project", "--max-budget-usd 0.25"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args lack %q: %v", want, gotArgs)
		}
	}
	if i := slices.Index(gotArgs, "--tools"); i < 0 || gotArgs[i+1] != "" {
		t.Errorf("tools not disabled: %v", gotArgs)
	}
	if i := slices.Index(gotArgs, "--json-schema"); i < 0 || gotArgs[i+1] != `{"type":"object"}` {
		t.Errorf("schema not passed: %v", gotArgs)
	}
	if gotBin != "claude" || gotStdin != "the prompt" || slices.Contains(gotArgs, "the prompt") || !slices.Equal(gotEnv, []string{"CLAUDE_CONFIG_DIR=/p"}) {
		t.Errorf("bin %q stdin %q env %v", gotBin, gotStdin, gotEnv)
	}
	if gotDir != work {
		t.Errorf("ran in %s, want the fixed %s", gotDir, work)
	}
	if _, err := a.Ask(context.Background(), turns.Ask{Home: "/u/.claude", Tier: turns.TierBalanced}); err != nil || !slices.Equal(gotEnv, []string{"CLAUDE_CONFIG_DIR"}) || !strings.Contains(strings.Join(gotArgs, " "), "--model pinned") {
		t.Errorf("default home and pinned tier: env %v args %v err %v", gotEnv, gotArgs, err)
	}
}

func TestAKindWithoutAShellRefusesToMakeAnAsker(t *testing.T) {
	if _, err := (Agent{}).Asker(t.TempDir(), nil); err == nil || !strings.Contains(err.Error(), "AskSh") {
		t.Fatalf("err %v", err)
	}
}

func TestParseFallsBackToTheResultText(t *testing.T) {
	quoted := strings.ReplaceAll(answerJSON, `"`, `\"`)
	ans, err := parse([]byte(envelopeJSON("", `"`+quoted+`"`, false)))
	if err != nil || string(ans.JSON) != answerJSON {
		t.Fatalf("%+v %v", ans, err)
	}
}

func TestParseTheRecordedEnvelope(t *testing.T) {
	raw, err := os.ReadFile("testdata/envelope.json")
	if err != nil {
		t.Fatal(err)
	}
	if ans, err := parse(raw); err != nil || !strings.Contains(string(ans.JSON), `"goal"`) {
		t.Fatalf("%s %v", ans.JSON, err)
	}
}

func TestAskReadsTheTokens(t *testing.T) {
	raw, err := os.ReadFile("testdata/envelope.json")
	if err != nil {
		t.Fatal(err)
	}
	ans, err := parse(raw)
	if want := (usage.Tokens{Input: 2, Output: 1033, CacheRead: 0, CacheWrite: 10679}); err != nil || ans.Tokens == nil || *ans.Tokens != want || ans.CostUSD == nil || *ans.CostUSD != 0.0026525 {
		t.Fatalf("%+v %v", ans, err)
	}
	if ans, err := parse([]byte(envelopeJSON(answerJSON, `""`, false))); err != nil || ans.Tokens != nil {
		t.Fatalf("an envelope without usage: %+v %v", ans.Tokens, err)
	}
	failed := `{"type":"result","subtype":"error_max_budget_usd","is_error":true,"duration_ms":10,"total_cost_usd":0.25,"errors":["Exceeded USD budget (0.25)"],"usage":{"input_tokens":5,"output_tokens":7}}`
	sh := func(context.Context, string, []string, string, string, ...string) ([]byte, error) {
		return []byte(failed), nil
	}
	ans, err = Asker{Bin: "claude", Dir: t.TempDir(), Sh: sh}.Ask(context.Background(), turns.Ask{Tier: turns.TierFast})
	if !errors.Is(err, turns.ErrOverBudget) || ans.CostUSD == nil || *ans.CostUSD != 0.25 || ans.Tokens == nil || *ans.Tokens != (usage.Tokens{Input: 5, Output: 7}) || ans.Model != "haiku" {
		t.Fatalf("a failed call: %+v %v", ans, err)
	}
}

func TestAskRecordsTheReportedModel(t *testing.T) {
	raw, err := os.ReadFile("testdata/envelope.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ out, want string }{
		{string(raw), "claude-haiku-5-5"},
		{`{"type":"result","is_error":false,"result":"{}","modelUsage":{"claude-haiku-5-5":{"outputTokens":40},"claude-sonnet-5":{"outputTokens":900}}}`, "claude-sonnet-5"},
		{`{"type":"result","is_error":false,"result":"{}","modelUsage":{"b-model":{"outputTokens":5},"a-model":{"outputTokens":5}}}`, "a-model"},
		{envelopeJSON(answerJSON, `""`, false), "haiku"},
	} {
		sh := func(context.Context, string, []string, string, string, ...string) ([]byte, error) {
			return []byte(c.out), nil
		}
		ans, err := Asker{Bin: "claude", Dir: t.TempDir(), Sh: sh}.Ask(context.Background(), turns.Ask{Tier: turns.TierFast})
		if err != nil || ans.Model != c.want {
			t.Errorf("model %q, want %q (%v)", ans.Model, c.want, err)
		}
	}
}

func TestAnUnreportedCostIsNil(t *testing.T) {
	ans, err := parse([]byte(`{"type":"result","is_error":false,"result":"{}"}`))
	if err != nil || ans.CostUSD != nil {
		t.Fatalf("cost %v %v", ans.CostUSD, err)
	}
	if ans, err := parse([]byte(`{"type":"result","is_error":false,"result":"{}","total_cost_usd":0}`)); err != nil || ans.CostUSD == nil || *ans.CostUSD != 0 {
		t.Fatalf("a reported zero: %v %v", ans.CostUSD, err)
	}
}

func TestParseReadsClaudesErrorResults(t *testing.T) {
	result := func(subtype, msg string) []byte {
		return []byte(`{"type":"result","subtype":"` + subtype + `","is_error":true,"duration_ms":10,"total_cost_usd":0.25,"errors":["` + msg + `"]}`)
	}
	for _, c := range []struct {
		subtype, msg string
		want         error
	}{
		{"error_max_budget_usd", "Exceeded USD budget (0.25)", turns.ErrOverBudget},
		{"error_max_structured_output_retries", "Failed to provide valid structured output after maximum retries", turns.ErrUnfit},
		{"error_max_turns", "Reached maximum number of turns (2)", turns.ErrUnfit},
		{"error_during_execution", "API Error: 401", turns.ErrUnavailable},
		{"error_during_execution", "API Error: 500 Internal server error", nil},
		{"success", "Prompt is too long", turns.ErrTooLong},
	} {
		_, err := parse(result(c.subtype, c.msg))
		if err == nil || !strings.Contains(err.Error(), c.msg) || (c.want != nil && !errors.Is(err, c.want)) ||
			(c.want == nil && (errors.Is(err, turns.ErrUnfit) || errors.Is(err, turns.ErrOverBudget) || errors.Is(err, turns.ErrTooLong))) {
			t.Errorf("%s: err %v", c.subtype, err)
		}
	}
	if _, err := parse([]byte(`{"type":"result","subtype":"error_during_execution","is_error":true,"errors":[]}`)); err == nil || !strings.Contains(err.Error(), "error_during_execution") {
		t.Errorf("no message: %v", err)
	}
	if _, err := parse([]byte("garbage")); !errors.Is(err, turns.ErrUnfit) {
		t.Errorf("garbage envelope: %v", err)
	}
}

func TestUnavailableErrors(t *testing.T) {
	for _, msg := range []string{
		`API Error: Request rejected (429) · {"type":"error","error":{"type":"rate_limit_error"}}`,
		`API Error: 529 {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`,
		"Credit balance is too low",
		"Not logged in · Please run /login",
		"API Error: 401 OAuth token has expired",
		"API Error: 429 Too Many Requests",
		"HTTP 529 from the API",
		"status code: 401",
	} {
		out := []byte(`{"type":"result","subtype":"error_during_execution","is_error":true,"errors":[` + jsonString(msg) + `]}`)
		if _, err := parse(out); !errors.Is(err, turns.ErrUnavailable) || !strings.Contains(err.Error(), msg) {
			t.Errorf("%q: %v", msg, err)
		}
	}
	sh := func(context.Context, string, []string, string, string, ...string) ([]byte, error) {
		return []byte("Not logged in"), errors.New("Not logged in · Please run /login: exit status 1")
	}
	if _, err := (Asker{Bin: "claude", Dir: t.TempDir(), Sh: sh}).Ask(context.Background(), turns.Ask{}); !errors.Is(err, turns.ErrUnavailable) {
		t.Errorf("a failed exit that is not JSON: %v", err)
	}
	for _, msg := range []string{"API Error: 500", "Prompt is too long", "Failed to parse", "the tool failed at line 401", "429 files changed"} {
		out := []byte(`{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["` + msg + `"]}`)
		if _, err := parse(out); errors.Is(err, turns.ErrUnavailable) {
			t.Errorf("%q reads as unavailable", msg)
		}
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestAskWithoutClaude(t *testing.T) {
	a := Asker{Bin: "claude-that-does-not-exist", Dir: t.TempDir(), Sh: Exec}
	if _, err := a.Ask(context.Background(), turns.Ask{Prompt: "x"}); !errors.Is(err, turns.ErrNoAgent) {
		t.Fatalf("err %v", err)
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	a.Bin = "sh"
	if _, err := a.Ask(context.Background(), turns.Ask{Prompt: "x"}); err == nil || errors.Is(err, turns.ErrNoAgent) {
		t.Fatalf("a failing binary: %v", err)
	}
}

func TestAskTimeoutNamesTheDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	a := Asker{Bin: "claude", Dir: t.TempDir(), Sh: func(context.Context, string, []string, string, string, ...string) ([]byte, error) {
		return nil, errors.New("signal: killed")
	}}
	if _, err := a.Ask(ctx, turns.Ask{Prompt: "x"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err %v", err)
	}
}

func TestAskReadsTheEnvelopeOfAFailedExit(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	out := `{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["Credit balance is too low"]}`
	a := Asker{Bin: "claude", Dir: t.TempDir(), Sh: func(ctx context.Context, dir string, env []string, stdin, _ string, _ ...string) ([]byte, error) {
		return Exec(ctx, dir, env, stdin, "sh", "-c", "echo '"+out+"'; exit 1")
	}}
	if _, err := a.Ask(context.Background(), turns.Ask{Prompt: "x"}); err == nil || !strings.Contains(err.Error(), "Credit balance is too low") {
		t.Fatalf("err %v", err)
	}
	a.Sh = func(ctx context.Context, dir string, env []string, stdin, _ string, _ ...string) ([]byte, error) {
		return Exec(ctx, dir, env, stdin, "sh", "-c", "echo not json; echo boom >&2; exit 3")
	}
	if _, err := a.Ask(context.Background(), turns.Ask{Prompt: "x"}); err == nil || errors.Is(err, turns.ErrUnfit) || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("a failed exit with no JSON: %v", err)
	}
}

func TestExecLeavesTheCallingClaudeSessionOut(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "s")
	t.Setenv("CLAUDE_EFFORT", "max")
	t.Setenv("CLAUDE_CONFIG_DIR", "/inherited")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "kept")
	t.Setenv("LOOP_GONE", "x")
	out, err := Exec(context.Background(), t.TempDir(), []string{"CLAUDE_CONFIG_DIR=/p", "LOOP_GONE"}, "", "sh", "-c", "env")
	if err != nil {
		t.Fatal(err)
	}
	env := string(out)
	if strings.Contains(env, "CLAUDECODE=") || strings.Contains(env, "CLAUDE_CODE_SESSION_ID=") || strings.Contains(env, "CLAUDE_EFFORT=") || strings.Contains(env, "LOOP_GONE=") ||
		!strings.Contains(env, "CLAUDE_CONFIG_DIR=/p\n") || strings.Contains(env, "/inherited") || !strings.Contains(env, "CLAUDE_CODE_OAUTH_TOKEN=kept") {
		t.Fatalf("env:\n%s", env)
	}
}
