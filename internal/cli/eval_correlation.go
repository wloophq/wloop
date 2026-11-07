package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/pkg/plugin/evalout"
)

const evalCorrelationUsage = "usage: eval-correlation --cases <dir> --out <dir> [--workflow task-correlate] [--prompt <file>] [--max-usd 2] [--call-usd 0.05]"

// correlationCase is one synthetic judgment: a task, its candidates, and what the workflows acting on the
// judgment should do: Act the verb they should run, "" for nothing, on the candidate On.
type correlationCase struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	Act        string         `json:"act"`
	On         string         `json:"on"`
	Task       caseTask       `json:"task"`
	Candidates []caseTask     `json:"candidates"`
	Facts      workflow.Facts `json:"facts,omitempty"`
}

type caseTask struct {
	ID    string   `json:"id"`
	Title string   `json:"title"`
	Notes string   `json:"notes"`
	Items []string `json:"items"`
}

// want is what c expects, as an action reads: "<verb> <task>", "" for nothing.
func (c correlationCase) want() string {
	if c.Act == "" {
		return ""
	}
	return c.Act + " " + c.On
}

// correlationResult is one case's verdict. Answer is the judgment's output as its schema names its fields;
// Acted is what the workflows acting on it would do, as their whens say over that output, each "<verb> <task>";
// Stray is an answer naming a task no workflow acts on.
type correlationResult struct {
	ID      string         `json:"id"`
	Kind    string         `json:"kind"`
	Want    string         `json:"want"`
	Answer  map[string]any `json:"answer,omitempty"`
	Acted   []string       `json:"acted,omitempty"`
	Stray   bool           `json:"stray"`
	Correct bool           `json:"correct"`
	Model   string         `json:"model,omitempty"`
	CostUSD *float64       `json:"cost_usd,omitempty"`
	Error   string         `json:"error,omitempty"`
}

// actScore counts one verb's actions: one on the wrong task, or where the case wants another or none, counts
// against its precision, and a wanted one not done against its recall.
type actScore struct {
	TP, FP, FN        int
	Precision, Recall float64
}

func (a *actScore) rates() {
	if a.TP+a.FP > 0 {
		a.Precision = float64(a.TP) / float64(a.TP+a.FP)
	}
	if a.TP+a.FN > 0 {
		a.Recall = float64(a.TP) / float64(a.TP+a.FN)
	}
}

// correlationScore counts the actions the runtime would take, any action being the positive class, and each
// verb's apart; a stray answer is no action.
type correlationScore struct {
	Cases, Answered, TN int
	Stray, Skipped      int
	actScore
	Acts     map[string]*actScore
	SpentUSD float64
	Model    string
}

func (s *correlationScore) act(verb string) *actScore {
	if s.Acts == nil {
		s.Acts = map[string]*actScore{}
	}
	if s.Acts[verb] == nil {
		s.Acts[verb] = &actScore{}
	}
	return s.Acts[verb]
}

type correlationEvalOptions struct {
	Asker    turns.Asker
	Cases    []correlationCase
	Judgment judgment
	MaxUSD   float64
	CallUSD  float64
	Log      io.Writer
}

// judgment is a classify step with a with, as the profile loads it, and the workflows that act on its output.
type judgment struct {
	// Name is the judging workflow's; With the fact its step lists the tasks of.
	Name, With     string
	Prompt, Schema string
	Acts           []acting
}

// acting is a workflow whose when reads a judgment's output: Verb the verb its step runs, Target the fact of that
// output the step names as the task it acts on.
type acting struct {
	Workflow     workflow.Workflow
	Verb, Target string
}

// acts are what j.Acts would do with the output fields of a judgment over c, each "<verb> <task>", and whether
// the output names a task any of them reads.
func (j judgment) acts(c correlationCase, fields map[string]any) ([]string, bool, error) {
	f := maps.Clone(c.Facts)
	if f == nil {
		f = workflow.Facts{}
	}
	ids := []string{}
	for _, t := range c.Candidates {
		ids = append(ids, t.ID)
	}
	f[j.With] = ids
	for k, v := range fields {
		f[builtin.ClassifyFact(j.Name, k)] = factValue(v)
	}
	trigger := workflow.Facts{"trigger.kind": string(workflow.TriggerEvent), "trigger.event.kind": builtin.EventClassified, "trigger.event.data": j.Name}
	var out []string
	named := false
	for _, a := range j.Acts {
		got, _ := f[a.Target].(string)
		named = named || got != ""
		ok, err := a.Workflow.When.Match(workflow.Vars{Facts: f, Trigger: trigger})
		if err != nil {
			return nil, named, fmt.Errorf("%s: %w", a.Workflow.Name, err)
		}
		if ok && !slices.Contains(out, a.Verb+" "+got) {
			out = append(out, a.Verb+" "+got)
		}
	}
	return out, named, nil
}

// loadCorrelationCases reads every .json case of dir, sorted by name.
func loadCorrelationCases(dir string) ([]correlationCase, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []correlationCase
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var c correlationCase
		if err := json.Unmarshal(b, &c); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		if (c.Act == "") != (c.On == "") || c.On != "" && !slices.ContainsFunc(c.Candidates, func(t caseTask) bool { return t.ID == c.On }) {
			return nil, fmt.Errorf("%s: act %q on %q: an act names one of its candidates, and no act none", e.Name(), c.Act, c.On)
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no case in %s", dir)
	}
	return out, nil
}

// casePrompt renders c through the workflow's prompt, as classify renders a with prompt.
func casePrompt(text string, c correlationCase) (string, error) {
	m := newPromptMarks()
	view := func(t caseTask) promptTask { return m.task(task.ID(t.ID), t.Title, t.Notes, t.Items) }
	self := view(c.Task)
	d := withData{Facts: m.markedFacts(c.Facts), Task: &self, promptMarks: m}
	for _, t := range c.Candidates {
		d.Tasks = append(d.Tasks, view(t))
	}
	return renderWith(text, d)
}

// runCorrelationEval asks the judgment of each case in order, starting no call that could pass MaxUSD.
func runCorrelationEval(ctx context.Context, o correlationEvalOptions) ([]correlationResult, correlationScore, error) {
	var out []correlationResult
	s := correlationScore{Cases: len(o.Cases)}
	for _, c := range o.Cases {
		if s.SpentUSD+o.CallUSD > o.MaxUSD {
			s.Skipped = len(o.Cases) - len(out)
			break
		}
		r := correlationResult{ID: c.ID, Kind: c.Kind, Want: c.want()}
		prompt, err := casePrompt(o.Judgment.Prompt, c)
		if err != nil {
			return out, s, fmt.Errorf("%s: %w", c.ID, err)
		}
		ans, err := o.Asker.Ask(ctx, turns.Ask{Tier: turns.TierFast, Prompt: prompt, Schema: o.Judgment.Schema, MaxCostUSD: o.CallUSD})
		spent := o.CallUSD
		if ans.CostUSD != nil {
			spent = *ans.CostUSD
			r.CostUSD = ans.CostUSD
		}
		s.SpentUSD += spent
		if err == nil {
			err = validAnswer(o.Judgment.Schema, ans.JSON)
		}
		if err == nil {
			err = json.Unmarshal(ans.JSON, &r.Answer)
		}
		named := false
		if err == nil {
			r.Acted, named, err = o.Judgment.acts(c, r.Answer)
		}
		if err != nil {
			r.Acted, r.Error = nil, err.Error()
		} else {
			s.Answered++
		}
		r.Model = ans.Model
		r.Stray = err == nil && len(r.Acted) == 0 && named
		r.Correct = err == nil && (len(r.Acted) == 0 && c.Act == "" || len(r.Acted) == 1 && r.Acted[0] == c.want())
		if r.Stray {
			s.Stray++
		}
		s.Model = cmp.Or(s.Model, ans.Model)
		s.score(c, r, err == nil)
		if o.Log != nil {
			fmt.Fprintf(o.Log, "%s: want %q, answer %s, acted %q\n", c.ID, c.want(), ans.JSON, r.Acted)
		}
		out = append(out, r)
	}
	s.rates()
	for _, a := range s.Acts {
		a.rates()
	}
	return out, s, nil
}

// score counts r, the result of c, answered or not.
func (s *correlationScore) score(c correlationCase, r correlationResult, answered bool) {
	hit := false
	for _, a := range r.Acted {
		verb, _, _ := strings.Cut(a, " ")
		if a == c.want() {
			hit = true
			s.TP++
			s.act(verb).TP++
			continue
		}
		s.FP++
		s.act(verb).FP++
	}
	switch {
	case c.Act != "" && !hit:
		s.FN++
		s.act(c.Act).FN++
	case c.Act == "" && len(r.Acted) == 0 && answered:
		s.TN++
	}
}

func (s correlationScore) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d cases, %d answered, %d skipped by the budget; precision %.2f (%d of %d acted), recall %.2f (%d of %d wanted)",
		s.Cases, s.Answered, s.Skipped, s.Precision, s.TP, s.TP+s.FP, s.Recall, s.TP, s.TP+s.FN)
	for _, verb := range slices.Sorted(maps.Keys(s.Acts)) {
		a := s.Acts[verb]
		fmt.Fprintf(&b, "; %s precision %.2f (%d of %d), recall %.2f (%d of %d)", verb, a.Precision, a.TP, a.TP+a.FP, a.Recall, a.TP, a.TP+a.FN)
	}
	fmt.Fprintf(&b, "; %d stray answers no workflow acts on; model %s; spent $%.4f", s.Stray, cmp.Or(s.Model, "unreported"), s.SpentUSD)
	return b.String()
}

// runEvalCorrelation runs the correlation judgment over the synthetic cases with the configured agent and writes
// results.json and score.txt under --out, a directory outside any git checkout.
func runEvalCorrelation(ctx context.Context, e *Env, args []string) error {
	fs := flag.NewFlagSet("eval-correlation", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cases := fs.String("cases", "", "directory of the case files")
	out := fs.String("out", "", "directory of the results, outside any git checkout")
	name := fs.String("workflow", "task-correlate", "workflow whose prompt and schema judge")
	promptFile := fs.String("prompt", "", "prompt template; default the workflow's")
	maxUSD := fs.Float64("max-usd", 2, "stop before the run spends more")
	callUSD := fs.Float64("call-usd", 0.05, "cap of one call")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 || *cases == "" {
		return usageError{usage: evalCorrelationUsage}
	}
	if err := evalout.OutsideRepository("out", *out); err != nil {
		return err
	}
	set, err := loadCorrelationCases(*cases)
	if err != nil {
		return err
	}
	cfg, err := e.loadConfig()
	if err != nil {
		return err
	}
	j, err := e.judgment(*name)
	if err != nil {
		return err
	}
	if *promptFile != "" {
		b, err := os.ReadFile(*promptFile)
		if err != nil {
			return err
		}
		j.Prompt = string(b)
	}
	dir, err := os.MkdirTemp("", brand.Name+"-eval-correlation-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	asker, err := e.asker(cfg, dir)
	if err != nil {
		return err
	}
	results, score, runErr := runCorrelationEval(ctx, correlationEvalOptions{Asker: asker, Cases: set, Judgment: j,
		MaxUSD: *maxUSD, CallUSD: *callUSD, Log: e.Err})
	if err := writeCorrelationEval(*out, results, score); err != nil {
		return err
	}
	fmt.Fprintf(e.Out, "%s\nwritten to %s\n", score, *out)
	return runErr
}

func writeCorrelationEval(dir string, results []correlationResult, s correlationScore) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(struct {
		Score   correlationScore    `json:"score"`
		Results []correlationResult `json:"results"`
	}{s, results}, "", " ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "results.json"), append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "score.txt"), []byte(s.String()+"\n"), 0o644)
}

// judgment is the classify step of workflow name, which must read a with, its prompt and schema, and the
// loaded workflows whose whens read its output, in their load order.
func (e *Env) judgment(name string) (judgment, error) {
	ws := e.loadedIntents().Workflows
	i := slices.IndexFunc(ws, func(w workflow.Workflow) bool { return w.Name == name })
	if i < 0 {
		return judgment{}, fmt.Errorf("no workflow %s is loaded", name)
	}
	j := slices.IndexFunc(ws[i].Steps, func(st workflow.Step) bool { return st.Use == stepClassify })
	var with string
	if j >= 0 {
		with, _ = ws[i].Steps[j].Args[withArg].(string)
	}
	if with == "" {
		return judgment{}, fmt.Errorf("%s has no classify step with a with to judge", name)
	}
	out := judgment{Name: name, With: with}
	c := stepCall{st: ws[i].Steps[j], why: workflow.Why{Workflow: name}}
	var err error
	if out.Prompt, err = stepFile(e, c, "prompt"); err != nil {
		return judgment{}, err
	}
	if out.Schema, err = stepFile(e, c, "schema"); err != nil {
		return judgment{}, err
	}
	prefix := builtin.ClassifyFact(name, "")
	for _, w := range ws {
		if w.Name == name || w.When == nil || !slices.ContainsFunc(w.When.Reads(), func(r string) bool { return strings.HasPrefix(r, prefix) }) {
			continue
		}
		for _, st := range w.Steps {
			reads := workflow.StepReads([]workflow.Step{st})
			k := slices.IndexFunc(reads, func(r string) bool { return strings.HasPrefix(r, prefix) })
			if k < 0 {
				continue
			}
			verb, _ := st.Args["verb"].(string)
			out.Acts = append(out.Acts, acting{Workflow: w, Verb: cmp.Or(verb, st.Use), Target: reads[k]})
			break
		}
	}
	if len(out.Acts) == 0 {
		return judgment{}, fmt.Errorf("no loaded workflow acts on %s's judgment: none whose when reads %s* names one of those facts as the task its steps act on", name, prefix)
	}
	return out, nil
}
