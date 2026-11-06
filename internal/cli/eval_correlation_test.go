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
	te := newTestEnv(t, nil)
	j, err := te.judgment("task-correlate")
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
	o := correlationEvalOptions{Asker: judge, Cases: cases, Judgment: j, MaxUSD: 2, CallUSD: 0.05}
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
	if _, err := te.judgment("my-judge"); err == nil || !strings.Contains(err.Error(), "no loaded workflow acts on my-judge's judgment") {
		t.Fatalf("a judgment no workflow acts on: %v", err)
	}
	if _, err := te.judgment("my-plain"); err == nil || !strings.Contains(err.Error(), "no classify step with a with") {
		t.Fatalf("a classify with no with: %v", err)
	}
	j, err := te.judgment("task-correlate")
	if err != nil || j.Acts.Name != "task-correlate-merge" || j.Target != "classify.task_correlate.same_as" || j.With != "task.candidates" {
		t.Fatalf("the bundled correlation: %+v %v", j, err)
	}
}

// fieldJudge answers each case with its fields under the names the profile's schema gives them.
type fieldJudge struct{ answers map[string]map[string]any }

func (j fieldJudge) Ask(_ context.Context, q turns.Ask) (turns.Answer, error) {
	m := selfID.FindStringSubmatch(q.Prompt)
	if m == nil {
		return turns.Answer{}, os.ErrInvalid
	}
	b, _ := json.Marshal(j.answers[m[1]])
	return turns.Answer{JSON: b}, nil
}

func TestTheCorrelationEvalScoresWhatTheWorkflowActingOnTheJudgmentProposes(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, nil)
	judge := "summary = \"Judge\"\nprovider = \"task\"\nwhen = 'size(task.candidates) > 0'\n\n[trigger]\non = [\"task_created\"]\n\n[[step]]\nuse = \"classify\"\nwith = \"task.candidates\"\nprompt = \"judge.md.tmpl\"\nschema = \"answer.json\"\n"
	acts := "summary = \"Merge\"\nprovider = \"task\"\nwhen = 'trigger.event.data == \"my-judge\" && classify.my_judge.match && classify.my_judge.sure && classify.my_judge.target in task.candidates'\n\n[trigger]\non = [\"classified\"]\n\n[[step]]\nuse = \"task.verb\"\nverb = \"merge\"\ntask = \"classify.my_judge.target\"\n"
	schema := `{"type":"object","required":["match","sure","target"],"properties":{"match":{"type":"boolean"},"sure":{"type":"boolean"},"target":{"type":"string"}}}`
	prompt, err := os.ReadFile("../../profiles/default/workflows/task-correlate/prompt.md.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	userProfile(t, te, map[string]string{
		"workflows/my-judge/workflow.toml": judge, "workflows/my-judge/judge.md.tmpl": string(prompt), "workflows/my-judge/answer.json": schema,
		"workflows/my-judge-merge/workflow.toml": acts,
	})
	if p := te.loadedIntents().Problems; len(p) > 0 {
		t.Fatal(p.Error())
	}
	j, err := te.judgment("my-judge")
	if err != nil {
		t.Fatal(err)
	}
	c := func(id, want string, cands ...string) correlationCase {
		cc := correlationCase{ID: id, SameAs: want, Task: caseTask{ID: id, Title: id}}
		for _, x := range cands {
			cc.Candidates = append(cc.Candidates, caseTask{ID: x, Title: x})
		}
		return cc
	}
	cases := []correlationCase{
		c("t-0000000000000001", "t-00000000000000a1", "t-00000000000000a1"),
		c("t-0000000000000002", "t-00000000000000a2", "t-00000000000000a2"),
		c("t-0000000000000003", "", "t-00000000000000a3"),
	}
	answers := fieldJudge{answers: map[string]map[string]any{
		"t-0000000000000001": {"match": true, "sure": true, "target": "t-00000000000000a1"},
		"t-0000000000000002": {"match": true, "sure": false, "target": "t-00000000000000a2"},
		"t-0000000000000003": {"match": false, "sure": true, "target": ""},
	}}
	results, score, err := runCorrelationEval(context.Background(), correlationEvalOptions{Asker: answers, Cases: cases, Judgment: j, MaxUSD: 1, CallUSD: 0.01})
	if err != nil {
		t.Fatal(err)
	}
	if score.TP != 1 || score.FN != 1 || score.TN != 1 || score.FP != 0 || score.Stray != 1 {
		t.Fatalf("score %+v, want only the sure match proposed: %+v", score, results)
	}
	if !results[0].Proposed || results[0].Got != "t-00000000000000a1" || results[1].Proposed {
		t.Fatalf("results %+v", results)
	}
}
