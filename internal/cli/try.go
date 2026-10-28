package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/pr"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/gitx"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/turn"
)

// tryPolicy lets a dry run read the pull request and refuses everything that
// would change it or anything else on GitHub; the dry-run turn's instructions
// are the first guard, this is the second.
func tryPolicy(command string, stored []string) turns.Policy {
	var deny [][]string
	for _, flag := range []string{"-X", "--method", "-f", "-F", "--field", "--raw-field", "--input"} {
		deny = append(deny, []string{"gh", "api", flag + "*"}, []string{"gh", "api", "*", flag + "*"})
	}
	deny = append(deny,
		[]string{"gh", "pr", "comment"}, []string{"gh", "pr", "review"}, []string{"gh", "pr", "edit"}, []string{"gh", "pr", "merge"},
		[]string{"gh", "pr", "close"}, []string{"gh", "pr", "ready"}, []string{"gh", "pr", "create"}, []string{"gh", "pr", "reopen"},
		[]string{"gh", "issue"}, []string{"gh", "release"}, []string{"gh", "workflow"}, []string{"gh", "label"}, []string{"gh", "gist"},
		[]string{"gh", "run", "rerun"}, []string{"gh", "repo"}, []string{"git", "push"}, []string{"git", "-C"}, []string{"git", "-c"},
		[]string{"git", "config"},
	)
	deny = append(deny, []string{command, "settle"}, []string{"curl"})
	allow := [][]string{
		{"gh", "pr", "view"}, {"gh", "pr", "diff"}, {"gh", "pr", "checks"}, {"gh", "pr", "list"}, {"gh", "search"}, {"gh", "api"}, {"gh", "run", "view"},
		{command, "config", "get"},
	}
	allow = append(allow, storedTurnRules(command, stored)...)
	for _, sub := range []string{"rev-parse", "diff", "log", "show", "blame", "ls-files", "status", "fetch", "merge-base", "cat-file"} {
		allow = append(allow, []string{"git", sub})
	}
	return turns.Policy{Strict: true, NoGitHubWrites: true, NoMCP: true, Allow: allow, Deny: deny, WritePaths: []string{dturn.ScratchDir}}
}

// roundPolicy lets an interactive round run the loop commands its
// procedure calls, and record its end, without a permission prompt, on top
// of the user's own permissions.
func roundPolicy(command string, stored []string) turns.Policy {
	var allow [][]string
	for _, cmd := range slices.Compact([]string{command, strings.Trim(command, "'")}) {
		allow = append(allow, storedTurnRules(cmd, stored)...)
		allow = append(allow, []string{cmd, "turn", "end"}, []string{cmd, "turn", "answer"}, []string{cmd, "config", "get"})
	}
	return turns.Policy{Allow: allow, TurnEnd: true, Drafts: true, WritePaths: []string{dturn.ScratchDir}}
}

// probePolicy lets a probe session read its turn and record it, and nothing
// else that reaches GitHub or git; the home's own hooks still run, since the
// probe proves them.
func probePolicy(command string, stored []string) turns.Policy {
	var allow [][]string
	for _, cmd := range slices.Compact([]string{command, strings.Trim(command, "'")}) {
		allow = append(allow, storedTurnRules(cmd, stored)...)
		allow = append(allow, []string{cmd, "turn", "end"}, []string{cmd, "turn", "answer"})
	}
	deny := [][]string{{"gh"}, {"git", "push"}, {"git", "commit"}, {"curl"}}
	return turns.Policy{Allow: allow, Deny: deny, TurnEnd: true, WritePaths: []string{dturn.ScratchDir}, NoGitHubWrites: true}
}

// storedTurnRules allow reading a stored turn of the stored intents only
// with --turn as the first flag, so the command always parses as the
// stored form: a rule matching the flag anywhere would also match it as
// another flag's value.
func storedTurnRules(command string, stored []string) [][]string {
	var allow [][]string
	for _, intent := range stored {
		allow = append(allow, []string{command, "instructions", intent, "--turn"})
	}
	return allow
}

// prIntents are the intents of the registry whose turns are about a real
// pull request: a round session reads their stored turns.
func prIntents(reg *dintent.Registry) []string {
	var out []string
	for _, d := range reg.All() {
		if d.Context == contextPR && d.Builds == "" {
			out = append(out, string(d.Name))
		}
	}
	return out
}

const tryUsage = "usage: profile try <pr-url> [--why <file.json>]"

// runTry runs a review round in a throwaway worktree and prints the review it
// would have published; nothing reaches the pull request.
func runTry(ctx context.Context, e *Env, args []string) error {
	var whyFile string
	var rest []string
	for i := 0; i < len(args); i++ {
		if args[i] != "--why" {
			rest = append(rest, args[i])
			continue
		}
		if i++; i == len(args) {
			return usageError{usage: tryUsage}
		}
		whyFile = args[i]
	}
	if len(rest) != 1 {
		return usageError{usage: tryUsage}
	}
	ref, err := pr.ParseURL(rest[0])
	if err != nil {
		return err
	}
	var why *workflow.Why
	if whyFile != "" {
		if why, err = readWhy(whyFile); err != nil {
			return err
		}
	}
	c, err := e.daemonClient(ctx)
	if err != nil {
		return err
	}
	sent, err := c.DryRunTurn(ctx, api.DryRunTurnRequest{PR: ref.URL(), Why: why})
	if err != nil {
		return err
	}
	if why != nil {
		if sent.Why == "" {
			fmt.Fprintln(e.Err, "the review instructions carry no why: its template does not render one")
		} else {
			fmt.Fprintf(e.Err, "%s\n\n", sent.Why)
		}
	}
	cfg, err := e.loadConfig()
	if err != nil {
		return err
	}
	repos, err := e.WS.Repos(ctx)
	if err != nil {
		return err
	}
	repo, err := e.resolveCheckout(ctx, cfg, repos, ref.RemoteKey(), ref, "")
	if err != nil {
		return err
	}
	if err := e.Git.FetchPR(ctx, repo.Path, ref.Number); err != nil {
		return err
	}
	parent, err := os.MkdirTemp("", brand.Name+"-try-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(parent)
	wt := filepath.Join(parent, ref.Repo)
	if err := e.Git.WorktreeAddDetached(ctx, repo.Path, wt, gitx.PRRef(ref.Number)); err != nil {
		return err
	}
	defer func() {
		if err := e.Git.WorktreeRemove(context.WithoutCancel(ctx), repo.Path, wt); err != nil {
			fmt.Fprintf(e.Err, "warning: removing %s: %v\n", wt, err)
		}
	}()
	cfg = cfg.For(ref.Owner, ref.Repo)
	s := turns.AgentSession{Worktree: wt, Home: cfg.AgentHome, Policy: tryPolicy(e.loopCommand(), prIntents(e.loadedIntents().Registry)).Denied(e.controlSurface(cfg))}
	fmt.Fprintf(e.Err, "trying a review of %s in %s; this takes a few minutes\n", ref.URL(), wt)
	h, err := e.headlessAgent(cfg.Agent)
	if err != nil {
		return err
	}
	out, runErr := h.Headless(ctx, s, sent.Prompt)
	raw, err := os.ReadFile(filepath.Join(wt, dturn.ScratchDir, "dry-run.json"))
	if err != nil {
		return fmt.Errorf("the agent wrote no review (%w); its last output:\n%s", errors.Join(runErr, err), lastLines(string(out), 30))
	}
	return printDryRun(e, raw)
}

func readWhy(path string) (*workflow.Why, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var w workflow.Why
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, fmt.Errorf("%s is not a why: %w", path, err)
	}
	return &w, nil
}

// dryRunTurn stores, in the daemon, the review turn of a profile try.
func (e *Env) dryRunTurn(ctx context.Context, r api.DryRunTurnRequest) (api.DryRunTurn, error) {
	ref, err := pr.ParseURL(r.PR)
	if err != nil {
		return api.DryRunTurn{}, err
	}
	cfg, err := e.loadConfig()
	if err != nil {
		return api.DryRunTurn{}, err
	}
	sent, err := e.storeTurn(ctx, cfg, turn.Request{Intent: turn.Intent(e.roleIntent(builtin.RoleReview)), Trigger: turn.ByHand, Unit: ref.Unit(), Plain: true, Why: r.Why})
	if err != nil {
		return api.DryRunTurn{}, err
	}
	return api.DryRunTurn{ID: sent.ID, Prompt: sent.Prompt, Why: turn.WhySection(sent.Text)}, nil
}

type dryRunReview struct {
	Event    string `json:"event"`
	Body     string `json:"body"`
	Comments []struct {
		Path      string `json:"path"`
		Line      int    `json:"line"`
		StartLine int    `json:"start_line"`
		Body      string `json:"body"`
	} `json:"comments"`
}

func printDryRun(e *Env, raw []byte) error {
	var r dryRunReview
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("the review the agent wrote is not JSON: %w\n%s", err, raw)
	}
	fmt.Fprintf(e.Out, "event: %s\n", r.Event)
	if strings.TrimSpace(r.Body) != "" {
		fmt.Fprintf(e.Out, "\nbody:\n%s\n", r.Body)
	}
	for _, c := range r.Comments {
		where := fmt.Sprintf("%s:%d", c.Path, c.Line)
		if c.StartLine > 0 {
			where = fmt.Sprintf("%s:%d-%d", c.Path, c.StartLine, c.Line)
		}
		fmt.Fprintf(e.Out, "\n%s\n%s\n", where, c.Body)
	}
	return nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
