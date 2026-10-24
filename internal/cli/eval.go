package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/turn"
	"github.com/wloophq/wloop/internal/turn/eval"
)

// runEvalInstructions has the configured agent judge the instructions of
// every scenario, or of those whose name matches a --scenario glob, and
// writes the report under the state directory's eval/.
func runEvalInstructions(ctx context.Context, e *Env, args []string) error {
	scenarios, err := evalScenarios(args)
	if err != nil {
		return err
	}
	cfg, err := e.loadConfig()
	if err != nil {
		return err
	}
	a, err := e.headlessAgent(cfg.Agent)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", brand.Name+"-eval-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	var results []eval.Result
	for _, s := range scenarios {
		res, err := eval.Judge(ctx, a, dir, s, e.loadedIntents())
		if err != nil {
			return err
		}
		results = append(results, res)
	}
	report := eval.Report(results)
	path := filepath.Join(e.StateDir, "eval", "instructions-"+e.Now().Format("2006-01-02")+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
		return err
	}
	_, err = e.Out.Write([]byte(report + "\nwritten to " + path + "\n"))
	return err
}

const evalUsage = "usage: eval-instructions [--scenario <glob>]..."

// evalScenarios are the scenarios whose name matches one of the --scenario
// globs of args, every scenario without one.
func evalScenarios(args []string) ([]turn.Scenario, error) {
	fs := flag.NewFlagSet("eval-instructions", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var globs listFlag
	fs.Var(&globs, "scenario", "a glob on the scenario names (repeatable)")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return nil, usageError{usage: evalUsage}
	}
	for _, g := range globs {
		if _, err := path.Match(g, ""); err != nil {
			return nil, fmt.Errorf("--scenario %s: %w", g, err)
		}
	}
	all := turn.Scenarios()
	if len(globs) == 0 {
		return all, nil
	}
	var out []turn.Scenario
	for _, s := range all {
		if slices.ContainsFunc(globs, func(g string) bool { ok, _ := path.Match(g, s.Name); return ok }) {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no scenario matches %s", strings.Join(globs, ", "))
	}
	return out, nil
}
