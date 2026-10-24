package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wloophq/wloop/internal/core/turns"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/turn"
)

type Verdict struct {
	Missing     []string `json:"missing"`
	Superfluous []string `json:"superfluous"`
	Ambiguous   []string `json:"ambiguous"`
	Score       int      `json:"score"`
}

type Result struct {
	Scenario string
	Words    int
	Verdict  Verdict
}

const judgePrompt = `You check the instructions an agent receives for one turn of a pull request loop. You do not judge any review.
Expected, in brief: %s
Instructions:
%s
Answer with JSON only: {"missing": [...], "superfluous": [...], "ambiguous": [...], "score": 1-5}.
missing: what the expected turn needs and the text lacks. superfluous: text about other turns or situations this turn cannot be in. ambiguous: places where two readings lead to different actions. score: fit and clarity, 5 best.`

// Judge completes and renders the scenario's instructions against the
// intents of l and has the agent check them against the scenario's brief,
// with no tools.
func Judge(ctx context.Context, r turns.HeadlessAgent, dir string, s turn.Scenario, l *intentload.Loaded) (Result, error) {
	c, err := turn.Complete(s.Context, l)
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", s.Name, err)
	}
	rendered, err := turn.Render(c, l, "")
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", s.Name, err)
	}
	out, err := r.Headless(ctx, turns.AgentSession{
		Worktree: dir,
		Policy:   turns.Policy{Strict: true, NoGitHubWrites: true, NoMCP: true},
	}, fmt.Sprintf(judgePrompt, s.Expect, rendered.Text))
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", s.Name, err)
	}
	v, err := parseVerdict(out)
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", s.Name, err)
	}
	return Result{Scenario: s.Name, Words: len(strings.Fields(rendered.Text)), Verdict: v}, nil
}

// parseVerdict reads the last top-level verdict object of the output.
// Objects nested in another object or inside strings never count.
func parseVerdict(out []byte) (Verdict, error) {
	var last Verdict
	found := false
	depth, inString := 0, false
	for i := 0; i < len(out); i++ {
		c := out[i]
		if inString {
			switch c {
			case '\\':
				i++
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = depth > 0
		case '}':
			if depth > 0 {
				depth--
			}
		case '{':
			if depth > 0 {
				depth++
				continue
			}
			var v Verdict
			dec := json.NewDecoder(bytes.NewReader(out[i:]))
			dec.DisallowUnknownFields()
			if dec.Decode(&v) == nil && v.Score >= 1 && v.Score <= 5 {
				last, found = v, true
				i += int(dec.InputOffset()) - 1
				continue
			}
			depth++
		}
	}
	if !found {
		return Verdict{}, fmt.Errorf("no verdict in the judge's answer: %q", out)
	}
	return last, nil
}

func Report(results []Result) string {
	var b strings.Builder
	b.WriteString("# Instruction eval\n\n| Scenario | Words | Score |\n|---|---|---|\n")
	for _, r := range results {
		fmt.Fprintf(&b, "| %s | %d | %d |\n", r.Scenario, r.Words, r.Verdict.Score)
	}
	for _, r := range results {
		if len(r.Verdict.Missing)+len(r.Verdict.Superfluous)+len(r.Verdict.Ambiguous) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n## %s\n", r.Scenario)
		for _, g := range []struct {
			name  string
			items []string
		}{{"Missing", r.Verdict.Missing}, {"Superfluous", r.Verdict.Superfluous}, {"Ambiguous", r.Verdict.Ambiguous}} {
			if len(g.items) == 0 {
				continue
			}
			fmt.Fprintf(&b, "\n%s:\n", g.name)
			for _, it := range g.items {
				fmt.Fprintf(&b, "- %s\n", it)
			}
		}
	}
	return b.String()
}
