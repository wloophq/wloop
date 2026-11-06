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

// correlationCase is one synthetic judgment: a task, its candidates, and the candidate that is the same work,
// "" for none.
type correlationCase struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	SameAs     string         `json:"same_as"`
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

// correlationResult is one case's verdict. Answer is the judgment's output as its schema names its fields;
// Proposed is what the workflow acting on it would do, as its when says over that output, and Got the task it
// would act on; Stray is an answer naming a task the runtime does not act on.
type correlationResult struct {
	ID       string         `json:"id"`
	Kind     string         `json:"kind"`
	Want     string         `json:"want"`
	Answer   map[string]any `json:"answer,omitempty"`
	Got      string         `json:"got"`
	Proposed bool           `json:"proposed"`
	Stray    bool           `json:"stray"`
	Correct  bool           `json:"correct"`
	Model    string         `json:"model,omitempty"`
	CostUSD  *float64       `json:"cost_usd,omitempty"`
	Error    string         `json:"error,omitempty"`
}

// correlationScore counts the proposals the runtime would open, "same work" being the positive class: a
// proposal of the wrong candidate counts against both precision and recall, and a stray answer is no proposal.
type correlationScore struct {
	Cases, Answered, TP, FP, FN, TN int
	Stray, Skipped                  int
	Precision, Recall               float64
	SpentUSD                        float64
	Model                           string
}

type correlationEvalOptions struct {
	Asker    turns.Asker
	Cases    []correlationCase
	Judgment judgment
	MaxUSD   float64
	CallUSD  float64
	Log      io.Writer
}

// judgment is a classify step with a with, as the profile loads it, and the workflow that acts on its output.
type judgment struct {
	// Name is the judging workflow's; With the fact its step lists the tasks of.
	Name, With     string
	Prompt, Schema string
	// Acts is the workflow whose when reads the judgment's output; Target the fact of that output its steps
	// name as the task they act on.
	Acts   workflow.Workflow
	Target string
}

// proposes says what j.Acts would do with the output fields of a judgment over c: whether its when holds, and
// the task its steps would act on.
func (j judgment) proposes(c correlationCase, fields map[string]any) (bool, string, error) {
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
	got, _ := f[j.Target].(string)
	trigger := workflow.Facts{"trigger.kind": string(workflow.TriggerEvent), "trigger.event.kind": builtin.EventClassified, "trigger.event.data": j.Name}
	ok, err := j.Acts.When.Match(workflow.Vars{Facts: f, Trigger: trigger})
	return ok, got, err
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
		if c.SameAs != "" && !slices.ContainsFunc(c.Candidates, func(t caseTask) bool { return t.ID == c.SameAs }) {
			return nil, fmt.Errorf("%s: same_as %s is not one of its candidates", e.Name(), c.SameAs)
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
		r := correlationResult{ID: c.ID, Kind: c.Kind, Want: c.SameAs}
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
		if err == nil {
			r.Proposed, r.Got, err = o.Judgment.proposes(c, r.Answer)
		}
		if err != nil {
			r.Proposed, r.Error = false, err.Error()
		} else {
			s.Answered++
		}
		r.Model = ans.Model
		r.Stray = err == nil && !r.Proposed && r.Got != ""
		r.Correct = err == nil && (r.Proposed && r.Got == c.SameAs || !r.Proposed && c.SameAs == "")
		if r.Stray {
			s.Stray++
		}
		s.Model = cmp.Or(s.Model, ans.Model)
		switch {
		case r.Proposed && r.Got == c.SameAs:
			s.TP++
		case r.Proposed:
			s.FP++
			if c.SameAs != "" {
				s.FN++
			}
		case c.SameAs != "":
			s.FN++
		case err == nil:
			s.TN++
		}
		if o.Log != nil {
			fmt.Fprintf(o.Log, "%s: want %q, answer %s, proposed %v %q\n", c.ID, c.SameAs, ans.JSON, r.Proposed, r.Got)
		}
		out = append(out, r)
	}
	if s.TP+s.FP > 0 {
		s.Precision = float64(s.TP) / float64(s.TP+s.FP)
	}
	if s.TP+s.FN > 0 {
		s.Recall = float64(s.TP) / float64(s.TP+s.FN)
	}
	return out, s, nil
}

func (s correlationScore) String() string {
	return fmt.Sprintf("%d cases, %d answered, %d skipped by the budget; precision %.2f (%d of %d proposed), recall %.2f (%d of %d same-work pairs); "+
		"%d stray answers no proposal acts on; model %s; spent $%.4f",
		s.Cases, s.Answered, s.Skipped, s.Precision, s.TP, s.TP+s.FP, s.Recall, s.TP, s.TP+s.FN, s.Stray, cmp.Or(s.Model, "unreported"), s.SpentUSD)
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
// loaded workflow whose when reads its output.
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
		reads := workflow.StepReads(w.Steps)
		k := slices.IndexFunc(reads, func(r string) bool { return strings.HasPrefix(r, prefix) })
		if k < 0 {
			continue
		}
		out.Acts, out.Target = w, reads[k]
		return out, nil
	}
	return judgment{}, fmt.Errorf("no loaded workflow acts on %s's judgment: none whose when reads %s* names one of those facts as the task its steps act on", name, prefix)
}
