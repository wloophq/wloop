package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/wloophq/wloop/internal/core/turns"
)

const correlationCases = "../../profiles/default/workflows/task-correlate/eval"

// fakeJudge answers each case's prompt with the id answers names for its task, costing cost per call.
type fakeJudge struct {
	mu      sync.Mutex
	answers map[string]string
	// stray are answers with same_work that name no candidate, by task id.
	stray map[string]string
	// unsure are answers without same_work that name a candidate, by task id.
	unsure map[string]string
	cost   float64
	calls  int
}

var selfID = regexp.MustCompile(`(?m)^- id: (t-[0-9a-f]+)`)

func (j *fakeJudge) Ask(_ context.Context, q turns.Ask) (turns.Answer, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.calls++
	m := selfID.FindStringSubmatch(q.Prompt)
	if m == nil {
		return turns.Answer{}, os.ErrInvalid
	}
	got := j.answers[m[1]]
	sameWork := got != ""
	if a, ok := j.stray[m[1]]; ok {
		got, sameWork = a, true
	}
	if a, ok := j.unsure[m[1]]; ok {
		got, sameWork = a, false
	}
	b, _ := json.Marshal(map[string]any{"same_work": sameWork, "same_as": got, "reason": "fake"})
	c := j.cost
	return turns.Answer{JSON: b, CostUSD: &c}, nil
}

func TestTheCorrelationEvalCommandScoresCasesWithAFakeJudge(t *testing.T) {
	t.Parallel()
	cases, err := loadCorrelationCases(correlationCases)
	if err != nil {
		t.Fatal(err)
	}
	same, notFirst, kinds := 0, 0, map[string]int{}
	for _, c := range cases {
		kinds[c.Kind]++
		if c.SameAs != "" {
			same++
			if c.Candidates[0].ID != c.SameAs {
				notFirst++
			}
		}
	}
	if notFirst < 3 {
		t.Errorf("the same-work candidate is first in all but %d positive cases: a judge picking the first would score", notFirst)
	}
	if len(cases) < 12 || same*2 != len(cases) {
		t.Fatalf("%d cases, %d same work: want at least 12 with balanced labels", len(cases), same)
	}
	for _, k := range []string{"dm-channel", "slack-pr", "look-alike", "sub-step"} {
		if kinds[k] == 0 {
			t.Errorf("no %s case", k)
		}
	}
	prompt, err := os.ReadFile("../../profiles/default/workflows/task-correlate/prompt.md.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	schema, err := os.ReadFile("../../profiles/default/workflows/task-correlate/schema.json")
	if err != nil {
		t.Fatal(err)
	}
	judge := &fakeJudge{answers: map[string]string{}, stray: map[string]string{}, unsure: map[string]string{}, cost: 0.0004}
	var wrongPositive, wrongNegative string
	var strays []string
	for _, c := range cases {
		judge.answers[c.Task.ID] = c.SameAs
		switch {
		case c.SameAs != "" && wrongPositive == "":
			wrongPositive, judge.answers[c.Task.ID] = c.ID, ""
		case c.SameAs == "" && wrongNegative == "":
			wrongNegative, judge.answers[c.Task.ID] = c.ID, c.Candidates[0].ID
		case c.SameAs == "" && len(strays) == 0:
			strays, judge.stray[c.Task.ID] = append(strays, c.ID), `""`
		case c.SameAs == "" && len(strays) == 1:
			strays, judge.stray[c.Task.ID] = append(strays, c.ID), "t-00000000deadbeef"
		case c.SameAs == "" && len(strays) == 2:
			strays, judge.unsure[c.Task.ID] = append(strays, c.ID), c.Candidates[0].ID
		}
	}
	o := correlationEvalOptions{Asker: judge, Cases: cases, Prompt: string(prompt), Schema: string(schema), MaxUSD: 2, CallUSD: 0.05}
	results, score, err := runCorrelationEval(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	pos := same
	if score.TP != pos-1 || score.FP != 1 || score.FN != 1 || score.TN != len(cases)-pos-1 || score.Stray != 3 || score.Answered != len(cases) {
		t.Fatalf("score %+v", score)
	}
	if want := float64(pos-1) / float64(pos); score.Precision != want || score.Recall != want {
		t.Fatalf("precision %v recall %v, want %v", score.Precision, score.Recall, want)
	}
	for _, r := range results {
		if r.Correct == (r.ID == wrongPositive || r.ID == wrongNegative) {
			t.Errorf("result %+v", r)
		}
		if r.Stray != slices.Contains(strays, r.ID) || r.Stray && r.Proposed {
			t.Errorf("a stray answer is shown apart and proposes nothing: %+v", r)
		}
	}
	if !strings.Contains(score.String(), "3 stray") || !strings.Contains(score.String(), "$0.0056") {
		t.Errorf("score %s", score)
	}

	judge.calls, judge.cost = 0, 0.05
	o.MaxUSD = 0.12
	_, score, err = runCorrelationEval(context.Background(), o)
	if err != nil || judge.calls != 2 || score.Skipped != len(cases)-2 {
		t.Fatalf("under $0.12 at $0.05 a call: %d calls, %+v, %v", judge.calls, score, err)
	}

	out := t.TempDir()
	if err := writeCorrelationEval(out, results, score); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(out, "score.txt")); err != nil || !strings.Contains(string(b), "precision") {
		t.Fatalf("score.txt %q %v", b, err)
	}
	te := newTestEnv(t, nil)
	err = runEvalCorrelation(context.Background(), te.Env, []string{"--cases", correlationCases, "--out", "results"})
	if err == nil || !strings.Contains(err.Error(), "inside the git checkout") {
		t.Fatalf("an --out inside the checkout: %v", err)
	}
}

func TestEveryCorrelationCasePromptMarksItsTexts(t *testing.T) {
	t.Parallel()
	cases, err := loadCorrelationCases(correlationCases)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := os.ReadFile("../../profiles/default/workflows/task-correlate/prompt.md.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		p, err := casePrompt(string(prompt), c)
		if err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		}
		n := regexp.MustCompile(`<<<DATA ([0-9a-f]{16})>>>`).FindStringSubmatch(p)[1]
		for _, tk := range append(c.Candidates, c.Task) {
			if !strings.Contains(p, "- id: "+tk.ID) || !strings.Contains(p, "<<<DATA "+n+">>>"+tk.Title+"<<<END DATA "+n+">>>") {
				t.Errorf("%s: %s is not listed with its title marked:\n%s", c.ID, tk.ID, p)
			}
		}
	}
}

func TestTheCorrelationEvalJudgesWithTheFilesTheWorkflowsStepNames(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, nil)
	judge := "summary = \"Judge\"\nprovider = \"task\"\nwhen = 'size(task.candidates) > 0'\n\n[trigger]\non = [\"task_created\"]\n\n[[step]]\nuse = \"classify\"\nwith = \"task.candidates\"\nprompt = \"judge.md.tmpl\"\nschema = \"answer.json\"\n"
	plain := strings.Replace(strings.Replace(judge, "with = \"task.candidates\"\n", "", 1), "judge.md.tmpl", "plain.md", 1)
	userProfile(t, te, map[string]string{
		"workflows/my-judge/workflow.toml": judge, "workflows/my-judge/judge.md.tmpl": "my prompt", "workflows/my-judge/answer.json": sameAsSchema,
		"workflows/my-plain/workflow.toml": plain, "workflows/my-plain/plain.md": "plain", "workflows/my-plain/answer.json": sameAsSchema,
	})
	if p := te.loadedIntents().Problems; len(p) > 0 {
		t.Fatal(p.Error())
	}
	prompt, schema, err := te.judgmentFiles("my-judge")
	if err != nil || prompt != "my prompt" || schema != sameAsSchema {
		t.Fatalf("%q %q %v", prompt, schema, err)
	}
	if _, _, err := te.judgmentFiles("my-plain"); err == nil || !strings.Contains(err.Error(), "no classify step with a with") {
		t.Fatalf("a classify with no with: %v", err)
	}
	if _, _, err := te.judgmentFiles("task-correlate"); err != nil {
		t.Fatalf("the bundled correlation: %v", err)
	}
}
