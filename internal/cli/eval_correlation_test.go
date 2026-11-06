package cli

import (
	"context"
	"encoding/json"
	"maps"
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

// fakeJudge answers each case's prompt with the verdict and task answers names for its task, costing cost per call.
type fakeJudge struct {
	mu      sync.Mutex
	answers map[string][2]string
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
	a := j.answers[m[1]]
	b, _ := json.Marshal(map[string]any{"verdict": a[0], "task": a[1], "reason": "fake"})
	c := j.cost
	return turns.Answer{JSON: b, CostUSD: &c}, nil
}

// verdictOf is the bundled schema's verdict for a case's act.
var verdictOf = map[string]string{"merge": "same", "relate": "related", "": "none"}

func TestTheCorrelationEvalCommandScoresCasesWithAFakeJudge(t *testing.T) {
	t.Parallel()
	cases, err := loadCorrelationCases(correlationCases)
	if err != nil {
		t.Fatal(err)
	}
	acts, notFirst, kinds := map[string]int{}, 0, map[string]int{}
	for _, c := range cases {
		kinds[c.Kind]++
		acts[c.Act]++
		if c.Act != "" && c.Candidates[0].ID != c.On {
			notFirst++
		}
	}
	if notFirst < 3 {
		t.Errorf("the wanted candidate is first in all but %d cases: a judge picking the first would score", notFirst)
	}
	if acts["merge"] < 6 || acts["relate"] < 6 || acts[""] < 6 {
		t.Fatalf("cases by act %v: want at least 6 merges, 6 relations and 6 of neither", acts)
	}
	for _, k := range []string{"dm-channel", "slack-pr", "look-alike", "sub-step", "follow-up", "same-launch"} {
		if kinds[k] == 0 {
			t.Errorf("no %s case", k)
		}
	}
	te := newTestEnv(t, nil)
	j, err := te.judgment("task-correlate")
	if err != nil {
		t.Fatal(err)
	}
	judge := &fakeJudge{answers: map[string][2]string{}, cost: 0.0004}
	planted := map[string]string{}
	strays := 0
	for _, c := range cases {
		judge.answers[c.Task.ID] = [2]string{verdictOf[c.Act], c.On}
		switch {
		case c.Act == "merge" && planted["missed merge"] == "":
			planted["missed merge"], judge.answers[c.Task.ID] = c.ID, [2]string{"none", ""}
		case c.Act == "relate" && planted["relate as merge"] == "":
			planted["relate as merge"], judge.answers[c.Task.ID] = c.ID, [2]string{"same", c.On}
		case c.Act == "" && planted["false relate"] == "":
			planted["false relate"], judge.answers[c.Task.ID] = c.ID, [2]string{"related", c.Candidates[0].ID}
		case c.Act == "" && strays == 0:
			strays, judge.answers[c.Task.ID] = 1, [2]string{"same", "t-00000000deadbeef"}
		case c.Act == "" && strays == 1:
			strays, judge.answers[c.Task.ID] = 2, [2]string{"none", c.Candidates[0].ID}
		}
	}
	o := correlationEvalOptions{Asker: judge, Cases: cases, Judgment: j, MaxUSD: 2, CallUSD: 0.05}
	results, score, err := runCorrelationEval(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	merge, relate := score.Acts["merge"], score.Acts["relate"]
	if merge == nil || relate == nil {
		t.Fatalf("score %+v, want merges and relations apart", score)
	}
	if merge.TP != acts["merge"]-1 || merge.FP != 1 || merge.FN != 1 || relate.TP != acts["relate"]-1 || relate.FP != 1 || relate.FN != 1 {
		t.Fatalf("merge %+v relate %+v", *merge, *relate)
	}
	if score.TN != acts[""]-1 || score.Stray != 2 || score.Answered != len(cases) || score.TP != merge.TP+relate.TP || score.FP != 2 || score.FN != 2 {
		t.Fatalf("score %+v", score)
	}
	for _, r := range results {
		wrong := slices.Contains(slices.Collect(maps.Values(planted)), r.ID)
		if r.Correct == wrong {
			t.Errorf("result %+v", r)
		}
		if r.Stray && len(r.Acted) > 0 {
			t.Errorf("a stray answer is shown apart and acts on nothing: %+v", r)
		}
	}
	if !strings.Contains(score.String(), "2 stray") || !strings.Contains(score.String(), "merge precision") || !strings.Contains(score.String(), "relate precision") {
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
	if err != nil || j.With != "task.candidates" {
		t.Fatalf("the bundled correlation: %+v %v", j, err)
	}
	var acting []string
	for _, a := range j.Acts {
		acting = append(acting, a.Workflow.Name+" "+a.Verb+" "+a.Target)
	}
	slices.Sort(acting)
	if want := []string{"task-correlate-merge merge classify.task_correlate.task", "task-correlate-merge-linked merge classify.task_correlate.task",
		"task-correlate-relate relate classify.task_correlate.task"}; !slices.Equal(acting, want) {
		t.Fatalf("the workflows acting on the bundled correlation %v, want %v", acting, want)
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
		cc := correlationCase{ID: id, On: want, Task: caseTask{ID: id, Title: id}}
		if want != "" {
			cc.Act = "merge"
		}
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
		t.Fatalf("score %+v, want only the sure match acted on: %+v", score, results)
	}
	if !slices.Equal(results[0].Acted, []string{"merge t-00000000000000a1"}) || len(results[1].Acted) != 0 {
		t.Fatalf("results %+v", results)
	}
}
