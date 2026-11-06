package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
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
	cost    float64
	calls   int
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
	b, _ := json.Marshal(map[string]string{"same_as": j.answers[m[1]], "reason": "fake"})
	c := j.cost
	return turns.Answer{JSON: b, CostUSD: &c}, nil
}

func TestTheCorrelationEvalCommandScoresCasesWithAFakeJudge(t *testing.T) {
	t.Parallel()
	cases, err := loadCorrelationCases(correlationCases)
	if err != nil {
		t.Fatal(err)
	}
	same, kinds := 0, map[string]int{}
	for _, c := range cases {
		kinds[c.Kind]++
		if c.SameAs != "" {
			same++
		}
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
	judge := &fakeJudge{answers: map[string]string{}, cost: 0.01}
	var wrongPositive, wrongNegative string
	for _, c := range cases {
		judge.answers[c.Task.ID] = c.SameAs
		if c.SameAs != "" && wrongPositive == "" {
			wrongPositive, judge.answers[c.Task.ID] = c.ID, ""
		}
		if c.SameAs == "" && wrongNegative == "" {
			wrongNegative, judge.answers[c.Task.ID] = c.ID, c.Candidates[0].ID
		}
	}
	o := correlationEvalOptions{Asker: judge, Cases: cases, Prompt: string(prompt), Schema: string(schema), MaxUSD: 2, CallUSD: 0.05}
	results, score, err := runCorrelationEval(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	pos := same
	if score.TP != pos-1 || score.FP != 1 || score.FN != 1 || score.TN != len(cases)-pos-1 || score.Answered != len(cases) {
		t.Fatalf("score %+v", score)
	}
	if want := float64(pos-1) / float64(pos); score.Precision != want || score.Recall != want {
		t.Fatalf("precision %v recall %v, want %v", score.Precision, score.Recall, want)
	}
	for _, r := range results {
		if r.Correct == (r.ID == wrongPositive || r.ID == wrongNegative) {
			t.Errorf("result %+v", r)
		}
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
	if err := outsideCheckout(filepath.Join(".", "out")); err == nil || !strings.Contains(err.Error(), "inside the git checkout") {
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
