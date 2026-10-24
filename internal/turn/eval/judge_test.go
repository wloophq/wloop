package eval

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/core/turns"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/turn"
)

type cannedRunner struct {
	out, got string
	session  turns.AgentSession
}

func (c *cannedRunner) Headless(_ context.Context, s turns.AgentSession, prompt string) ([]byte, error) {
	c.got, c.session = prompt, s
	return []byte("thinking...\n" + c.out), nil
}

func TestJudgeSendsTheBriefAndTheTextAndParsesTheVerdict(t *testing.T) {
	s := turn.Scenarios()[0]
	r := &cannedRunner{out: `{"missing":[],"superfluous":["dry run rules"],"ambiguous":[],"score":4}`}
	res, err := Judge(context.Background(), r, t.TempDir(), s, bundled(t))
	if err != nil || res.Verdict.Score != 4 || res.Verdict.Superfluous[0] != "dry run rules" || res.Words == 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if !strings.Contains(r.got, s.Expect) || !strings.Contains(r.got, "## Steps") {
		t.Fatal("the judge did not get the brief and the text")
	}
}

func TestReportListsEachScenario(t *testing.T) {
	out := Report([]Result{{Scenario: "review/first", Words: 900, Verdict: Verdict{Score: 5}}})
	if !strings.Contains(out, "| review/first | 900 | 5 |") {
		t.Fatal(out)
	}
}

func TestParseVerdictSkipsBracesInsideStrings(t *testing.T) {
	v, err := parseVerdict([]byte("ok\n```json\n{\"missing\":[\"the {{.Round}} value\"],\"superfluous\":[],\"ambiguous\":[],\"score\":3}\n```"))
	if err != nil || v.Score != 3 || len(v.Missing) != 1 {
		t.Fatalf("%+v %v", v, err)
	}
}

func TestParseVerdictRejectsNestedObjectsAndStrayFragments(t *testing.T) {
	v, err := parseVerdict([]byte(`{"missing":[{"score":5}],"superfluous":[],"ambiguous":[],"score":2} {"score":9} {"x":1}`))
	if err == nil {
		t.Fatalf("a nested or stray object won: %+v", v)
	}
	v, err = parseVerdict([]byte(`{"score":1} {"x":1}`))
	if err != nil || v.Score != 1 {
		t.Fatalf("%+v %v", v, err)
	}
}

func TestJudgeRunsWithNoToolsInDir(t *testing.T) {
	r := &cannedRunner{out: `{"missing":[],"superfluous":[],"ambiguous":[],"score":5}`}
	dir := t.TempDir()
	if _, err := Judge(context.Background(), r, dir, turn.Scenarios()[0], bundled(t)); err != nil {
		t.Fatal(err)
	}
	s := r.session
	if s.Worktree != dir || s.Home != "" {
		t.Fatalf("session %+v", s)
	}
	if want := (turns.Policy{Strict: true, NoGitHubWrites: true, NoMCP: true}); !reflect.DeepEqual(s.Policy, want) {
		t.Fatalf("policy %+v, want %+v", s.Policy, want)
	}
}

func bundled(t *testing.T) *intentload.Loaded {
	t.Helper()
	l, err := turn.Bundled()
	if err != nil {
		t.Fatal(err)
	}
	return l
}
