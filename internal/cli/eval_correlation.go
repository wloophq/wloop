package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
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

// correlationResult is one case's verdict. Proposed is what the runtime acts on: same_work, with same_as one of
// the case's candidates; Stray is any other non-empty same_as.
type correlationResult struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	Want     string   `json:"want"`
	SameWork bool     `json:"same_work"`
	Got      string   `json:"got"`
	Proposed bool     `json:"proposed"`
	Stray    bool     `json:"stray"`
	Reason   string   `json:"reason"`
	Correct  bool     `json:"correct"`
	Model    string   `json:"model,omitempty"`
	CostUSD  *float64 `json:"cost_usd,omitempty"`
	Error    string   `json:"error,omitempty"`
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
	Asker   turns.Asker
	Cases   []correlationCase
	Prompt  string
	Schema  string
	MaxUSD  float64
	CallUSD float64
	Log     io.Writer
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
		prompt, err := casePrompt(o.Prompt, c)
		if err != nil {
			return out, s, fmt.Errorf("%s: %w", c.ID, err)
		}
		ans, err := o.Asker.Ask(ctx, turns.Ask{Tier: turns.TierFast, Prompt: prompt, Schema: o.Schema, MaxCostUSD: o.CallUSD})
		spent := o.CallUSD
		if ans.CostUSD != nil {
			spent = *ans.CostUSD
			r.CostUSD = ans.CostUSD
		}
		s.SpentUSD += spent
		if err == nil {
			err = validAnswer(o.Schema, ans.JSON)
		}
		var v struct {
			SameWork bool   `json:"same_work"`
			SameAs   string `json:"same_as"`
			Reason   string `json:"reason"`
		}
		if err == nil {
			err = json.Unmarshal(ans.JSON, &v)
		}
		if err != nil {
			r.Error = err.Error()
		} else {
			s.Answered++
		}
		r.SameWork, r.Got, r.Reason, r.Model = v.SameWork, v.SameAs, v.Reason, ans.Model
		r.Proposed = err == nil && v.SameWork && slices.ContainsFunc(c.Candidates, func(t caseTask) bool { return t.ID == v.SameAs })
		r.Stray = err == nil && !r.Proposed && v.SameAs != ""
		r.Correct = err == nil && (r.Proposed && v.SameAs == c.SameAs || !r.Proposed && c.SameAs == "")
		if r.Stray {
			s.Stray++
		}
		s.Model = cmp.Or(s.Model, ans.Model)
		switch {
		case r.Proposed && v.SameAs == c.SameAs:
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
			fmt.Fprintf(o.Log, "%s: want %q, same_work %v same_as %q, proposed %v\n", c.ID, c.SameAs, v.SameWork, v.SameAs, r.Proposed)
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
	prompt, schema, err := e.judgmentFiles(*name)
	if err != nil {
		return err
	}
	if *promptFile != "" {
		b, err := os.ReadFile(*promptFile)
		if err != nil {
			return err
		}
		prompt = string(b)
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
	results, score, runErr := runCorrelationEval(ctx, correlationEvalOptions{Asker: asker, Cases: set, Prompt: prompt, Schema: schema,
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

// judgmentFiles are the prompt and the schema the classify step of workflow names, which must read a with.
func (e *Env) judgmentFiles(name string) (prompt, schema string, err error) {
	ws := e.loadedIntents().Workflows
	i := slices.IndexFunc(ws, func(w workflow.Workflow) bool { return w.Name == name })
	if i < 0 {
		return "", "", fmt.Errorf("no workflow %s is loaded", name)
	}
	j := slices.IndexFunc(ws[i].Steps, func(st workflow.Step) bool { return st.Use == stepClassify })
	if j < 0 || ws[i].Steps[j].Args[withArg] == nil {
		return "", "", fmt.Errorf("%s has no classify step with a with to judge", name)
	}
	c := stepCall{st: ws[i].Steps[j], why: workflow.Why{Workflow: name}}
	if prompt, err = stepFile(e, c, "prompt"); err != nil {
		return "", "", err
	}
	schema, err = stepFile(e, c, "schema")
	return prompt, schema, err
}
