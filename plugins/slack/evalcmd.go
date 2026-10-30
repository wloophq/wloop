package slack

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wloophq/wloop/plugins/slack/eval"
	"github.com/wloophq/wloop/plugins/slack/extract"
	"github.com/wloophq/wloop/plugins/slack/host"
)

// brandName stands for the host's command in the eval usage.
const brandName = "<command>"

// evalEnv is one eval command's environment.
type evalEnv struct {
	p        *plugin
	Out, Err io.Writer
}

// eval runs the slack eval command line args, writing its report to out
// and its progress to log.
func (p *plugin) eval(ctx context.Context, args []string, out, log io.Writer) error {
	e := evalEnv{p: p, Out: out, Err: log}
	return e.slackEval(ctx, args)
}

var slackEvalUsage = "usage: " + brandName + ` slack eval export --since YYYY-MM-DD --until YYYY-MM-DD --out <dir> [--exclude <dir>]...
       ` + brandName + ` slack eval run --fixtures <dir> --out <dir> [--split dev|holdout|all] [--label <name>] [--prompt <file>]
                 [--extractor-tier fast] [--escalate-over-chars 0] [--judge-tier frontier] [--max-usd 20] [--jobs 4]
                 [--viewer-id U..] [--viewer-name ".."] [--language English] [--now RFC3339] [--bar 0.8]
       ` + brandName + ` slack eval score --out <dir> --label <name> [--bar 0.8]
       ` + brandName + ` slack eval optimize --out <dir> --label <name> --prompt <file> --new <file>`

func (e evalEnv) slackEval(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New(slackEvalUsage)
	}
	switch args[0] {
	case "export":
		return e.slackEvalExport(ctx, args[1:])
	case "run":
		return e.slackEvalRun(ctx, args[1:])
	case "score":
		return e.slackEvalScore(args[1:])
	case "optimize":
		return e.slackEvalOptimize(ctx, args[1:])
	}
	return errors.New(slackEvalUsage)
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func (e evalEnv) evalFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("slack eval "+name, flag.ContinueOnError)
	fs.SetOutput(e.Err)
	return fs
}

// outsideRepository refuses a path inside a git checkout, so real Slack
// content never lands where it could be committed.
func outsideRepository(flagName, path string) error {
	if path == "" {
		return fmt.Errorf("--%s is required", flagName)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for dir, rest := abs, ""; ; dir, rest = filepath.Dir(dir), filepath.Join(filepath.Base(dir), rest) {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			abs = filepath.Join(real, rest)
			break
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	for dir := abs; ; dir = filepath.Dir(dir) {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return fmt.Errorf("--%s %s is inside the git checkout %s; write eval outputs outside any repository", flagName, abs, dir)
		}
		if filepath.Dir(dir) == dir {
			return nil
		}
	}
}

func (e evalEnv) evalNow() time.Time { return e.p.now() }

func (e evalEnv) slackEvalExport(ctx context.Context, args []string) error {
	fs := e.evalFlags("export")
	since := fs.String("since", "", "first day of activity, YYYY-MM-DD")
	until := fs.String("until", "", "last day of activity, YYYY-MM-DD")
	out := fs.String("out", "", "fixtures directory to write")
	var exclude multiFlag
	fs.Var(&exclude, "exclude", "fixtures directory whose conversations are skipped (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := outsideRepository("out", *out); err != nil {
		return err
	}
	from, err := time.ParseInLocation(time.DateOnly, *since, time.Local)
	if err != nil {
		return fmt.Errorf("--since: %w", err)
	}
	to, err := time.ParseInLocation(time.DateOnly, *until, time.Local)
	if err != nil {
		return fmt.Errorf("--until: %w", err)
	}
	db := e.p.db
	if db == nil {
		return errors.New("slack eval export: the plugin has no store")
	}
	segments := Segments{Store: db.Segments()}
	st := &Store{DB: db.Slack(), Segments: segments}
	ec := e.p.cfg.Extraction
	rep, err := eval.Export(ctx, eval.ExportOptions{
		DB: db.Slack(), Facts: st, Segments: segments,
		Since: from, Until: to.AddDate(0, 0, 1), Out: *out, Exclude: exclude, Now: e.evalNow(),
		Before: st.MessagesBefore, ContextMessages: ec.ContextN(), ContextChars: ec.ContextBudget(),
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(e.Out, "%d fixtures written to %s; %d excluded; %d DM messages without a segment\n", rep.Written, *out, rep.Excluded, rep.Unsegmented)
	return nil
}

func (e evalEnv) slackEvalRun(ctx context.Context, args []string) error {
	fs := e.evalFlags("run")
	fixtures := fs.String("fixtures", "", "fixtures directory")
	out := fs.String("out", "", "outputs directory")
	split := fs.String("split", "all", "dev, holdout or all")
	label := fs.String("label", "shipped", "name of this run's outputs")
	promptFile := fs.String("prompt", "", "extractor prompt template; default the shipped one")
	extractorTier := fs.String("extractor-tier", string(host.TierFast), "model tier of the extractor")
	escalate := fs.Int("escalate-over-chars", 0, "send an extraction whose conversation renders longer to the frontier tier; 0 never")
	judgeTier := fs.String("judge-tier", string(host.TierFrontier), "model tier of the oracle, the blind reader and the judge")
	maxUSD := fs.Float64("max-usd", 20, "stop before the run spends more")
	jobs := fs.Int("jobs", 4, "fixtures in flight")
	viewerID := fs.String("viewer-id", "", "the viewer's Slack user id; default viewer.json of the fixtures")
	viewerName := fs.String("viewer-name", "", "the viewer's name")
	language := fs.String("language", "English", "language of every text field")
	now := fs.String("now", "", "RFC3339 now of every fixture")
	bar := fs.Float64("bar", 0, "fail unless the holdout is complete and its same_actions reaches it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := outsideRepository("out", *out); err != nil {
		return err
	}
	if *split != "all" && *split != eval.SplitDev && *split != eval.SplitHoldout {
		return fmt.Errorf("--split %q: use dev, holdout or all", *split)
	}
	et, err := host.ParseTier(*extractorTier)
	if err != nil {
		return err
	}
	jt, err := host.ParseTier(*judgeTier)
	if err != nil {
		return err
	}
	if *now != "" {
		if _, err := time.Parse(time.RFC3339, *now); err != nil {
			return fmt.Errorf("--now: %w", err)
		}
	}
	set, err := eval.Load(*fixtures)
	if err != nil {
		return err
	}
	var tmpl string
	if *promptFile != "" {
		raw, err := os.ReadFile(*promptFile)
		if err != nil {
			return err
		}
		tmpl = string(raw)
	}
	prompts, err := extract.LoadPrompts(ctx, e.p.host.Profile)
	if err != nil {
		return err
	}
	rep, runErr := eval.Run(ctx, eval.Options{
		Asker: e.p.host, Set: set, Out: *out, Label: *label, Split: *split, Template: tmpl, Prompts: prompts,
		Viewer: extract.Viewer{ID: *viewerID, Name: *viewerName}, Language: *language, Now: *now,
		MaxUSD: *maxUSD, Jobs: *jobs, ExtractorTier: et, EscalateOverChars: *escalate, JudgeTier: jt, Log: e.Err,
	})
	if rep.Score != nil {
		if err := printScore(e, rep.Score); err != nil {
			return err
		}
	}
	if runErr != nil {
		return runErr
	}
	if *bar > 0 {
		return rep.Score.Bar(*bar)
	}
	return nil
}

func printScore(e evalEnv, s eval.Score) error {
	enc := json.NewEncoder(e.Out)
	enc.SetIndent("", " ")
	return enc.Encode(s)
}

func (e evalEnv) slackEvalScore(args []string) error {
	fs := e.evalFlags("score")
	out := fs.String("out", "", "outputs directory")
	label := fs.String("label", "", "the run's label")
	bar := fs.Float64("bar", 0, "fail unless the holdout is complete and its same_actions reaches it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := outsideRepository("out", *out); err != nil {
		return err
	}
	if *label == "" {
		return errors.New("--label is required")
	}
	s, err := eval.WriteScore(*out, *label)
	if err != nil {
		return err
	}
	if err := printScore(e, s); err != nil {
		return err
	}
	if *bar > 0 {
		return s.Bar(*bar)
	}
	return nil
}

func (e evalEnv) slackEvalOptimize(ctx context.Context, args []string) error {
	fs := e.evalFlags("optimize")
	out := fs.String("out", "", "outputs directory")
	label := fs.String("label", "", "the run's label")
	promptFile := fs.String("prompt", "", "the template the label ran with")
	newFile := fs.String("new", "", "where the candidate template goes")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := outsideRepository("out", *out); err != nil {
		return err
	}
	if err := outsideRepository("new", *newFile); err != nil {
		return err
	}
	if *label == "" || *promptFile == "" {
		return errors.New("--label and --prompt are required")
	}
	current, err := os.ReadFile(*promptFile)
	if err != nil {
		return err
	}
	prompts, err := extract.LoadPrompts(ctx, e.p.host.Profile)
	if err != nil {
		return err
	}
	changelog, cost, err := eval.Optimize(ctx, eval.OptimizeOptions{Asker: e.p.host, Out: *out, Label: *label, Current: string(current), New: *newFile,
		Prompts: prompts})
	fmt.Fprintf(e.Err, "optimize: $%.2f\n", cost)
	if err != nil {
		return err
	}
	fmt.Fprintf(e.Out, "%s written\n", *newFile)
	for _, c := range changelog {
		fmt.Fprintln(e.Out, "- "+c)
	}
	return nil
}
