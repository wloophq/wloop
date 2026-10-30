package eval

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/wloophq/wloop/plugins/slack/extract"
	"github.com/wloophq/wloop/plugins/slack/host"
)

//go:embed optimize.md
var optimizeTemplate string

// MaxPromptWords is the extractor template's cap, as extract's tests count it.
const MaxPromptWords = 5500

// OptimizeCallUSD caps the optimizer's call.
const OptimizeCallUSD = 5.0

// Placeholders are the extractor template's, each exactly once.
var Placeholders = []string{"{{VIEWER_ID}}", "{{VIEWER_NAME}}", "{{NOW}}", "{{LANGUAGE}}", "{{LEDGER}}", "{{CONTEXTS}}", "{{CONVERSATION}}"}

const optimizeSchema = `{"type": "object", "additionalProperties": false, "required": ["prompt", "changelog"],
 "properties": {"prompt": {"type": "string"}, "changelog": {"type": "array", "items": {"type": "string"}}}}`

type OptimizeOptions struct {
	Asker   host.Asker
	Out     string
	Label   string
	Current string // the template the label ran with
	New     string // where the candidate goes
	Tier    host.Tier
	// Prompts are the shipped extraction prompts, whose schema the candidate keeps.
	Prompts *extract.Prompts
}

type devReport struct {
	Severity        string      `json:"severity"`
	SameActions     bool        `json:"same_actions"`
	Causes          []RootCause `json:"causes"`
	Hints           []string    `json:"hints"`
	ReferenceErrors []string    `json:"reference_errors"`
}

// CheckTemplate refuses a template over the word cap or that drops or
// repeats a placeholder.
func CheckTemplate(t string) error {
	if n := len(strings.Fields(t)); n > MaxPromptWords {
		return fmt.Errorf("the candidate has %d words, over %d", n, MaxPromptWords)
	}
	for _, p := range Placeholders {
		if n := strings.Count(t, p); n != 1 {
			return fmt.Errorf("the candidate has %s %d times, want once", p, n)
		}
	}
	return nil
}

// Optimize asks for a better template from the label's dev judgments alone
// and writes it to New, with its changelog beside it, only when it passes
// CheckTemplate.
func Optimize(ctx context.Context, o OptimizeOptions) (changelog []string, cost float64, err error) {
	var m manifest
	if err := readJSON(manifestPath(o.Out, o.Label), &m); err != nil {
		return nil, 0, fmt.Errorf("no run of %s under %s: %w", o.Label, o.Out, err)
	}
	var dev []devReport
	for _, e := range m.Fixtures {
		if e.Split != SplitDev {
			continue
		}
		var j Judgment
		err := readJSON(filepath.Join(o.Out, o.Label, "judge", e.ID+".json"), &j)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, 0, err
		}
		dev = append(dev, devReport{Severity: j.Severity, SameActions: j.SameActions, Causes: j.LedgerRootCauses,
			Hints: j.PromptFixHints, ReferenceErrors: j.ReferenceErrors})
	}
	if len(dev) == 0 {
		return nil, 0, fmt.Errorf("no dev judgment of %s under %s", o.Label, o.Out)
	}
	reports, err := marshal(dev)
	if err != nil {
		return nil, 0, err
	}
	prompt := fill(optimizeTemplate, map[string]string{
		"WORDS":   strconv.Itoa(len(strings.Fields(o.Current))),
		"CAP":     words(MaxPromptWords),
		"TARGET":  words(MaxPromptWords - 100),
		"SCHEMA":  o.Prompts.Schema(),
		"CURRENT": o.Current,
		"REPORTS": string(reports),
	})
	tier := o.Tier
	if tier == "" {
		tier = host.TierFrontier
	}
	ans, err := o.Asker.Ask(ctx, host.Ask{Tier: tier, Prompt: prompt, Schema: optimizeSchema, MaxCostUSD: OptimizeCallUSD})
	if ans.CostUSD != nil {
		cost = *ans.CostUSD
	}
	if err != nil {
		return nil, cost, err
	}
	var out struct {
		Prompt    string   `json:"prompt"`
		Changelog []string `json:"changelog"`
	}
	if err := json.Unmarshal(ans.JSON, &out); err != nil {
		return nil, cost, fmt.Errorf("%w: %v", host.ErrUnfit, err)
	}
	if err := CheckTemplate(out.Prompt); err != nil {
		return out.Changelog, cost, fmt.Errorf("discarded, nothing written: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(o.New), 0o700); err != nil {
		return nil, cost, err
	}
	if err := os.WriteFile(o.New, []byte(out.Prompt), 0o600); err != nil {
		return nil, cost, err
	}
	return out.Changelog, cost, writeJSON(strings.TrimSuffix(o.New, filepath.Ext(o.New))+".changelog.json", out.Changelog)
}

// words writes n with a thousands comma, as the prompts do.
func words(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	return s[:len(s)-3] + "," + s[len(s)-3:]
}
