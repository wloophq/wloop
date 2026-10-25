package claude

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/usage"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/procgroup"
)

// tierModels are Claude Code's model aliases, which follow the latest model
// of each family.
var tierModels = map[turns.Tier]string{
	turns.TierFast:     "haiku",
	turns.TierBalanced: "sonnet",
	turns.TierFrontier: "opus",
}

// Model is the model tier runs on: the user's pin for it, else the alias of
// its family.
func Model(tier turns.Tier, pins map[turns.Tier]string) string {
	if m := pins[tier]; m != "" {
		return m
	}
	return tierModels[tier]
}

// Asker makes headless calls of Claude Code with claude -p.
type Asker struct {
	Bin string
	// Dir is the working directory of every call. One fixed directory,
	// because claude keeps a small directory per working directory under
	// ~/.claude/projects.
	Dir      string
	Sh       ShellFunc
	Models   map[turns.Tier]string
	UserHome string
}

var (
	_ turns.Asker       = Asker{}
	_ turns.AskingAgent = Agent{}
)

func (k Agent) Asker(dir string, models map[turns.Tier]string) (turns.Asker, error) {
	if k.AskSh == nil {
		return nil, errors.New("claude: the agent has no shell for headless calls (AskSh)")
	}
	return Asker{Bin: "claude", Dir: dir, Sh: k.AskSh, Models: models, UserHome: k.UserHome}, nil
}

func (a Asker) Ask(ctx context.Context, q turns.Ask) (turns.Answer, error) {
	if err := checkHeadless(q); err != nil {
		return turns.Answer{}, err
	}
	if err := os.MkdirAll(a.Dir, 0o700); err != nil {
		return turns.Answer{}, err
	}
	var help helpFlags
	if headless(q) {
		var err error
		if help, err = a.restricted(ctx); err != nil {
			return turns.Answer{}, err
		}
	}
	model := Model(q.Tier, a.Models)
	args := []string{"-p", "--output-format", "json", "--json-schema", q.Schema,
		"--tools", "", "--no-session-persistence", "--strict-mcp-config", "--setting-sources", "project"}
	if headless(q) {
		args = headlessArgs(args, q, help)
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	if q.MaxCostUSD > 0 {
		args = append(args, "--max-budget-usd", strconv.FormatFloat(q.MaxCostUSD, 'f', -1, 64))
	}
	defaultHome := ""
	if a.UserHome != "" {
		defaultHome = DefaultHome(a.UserHome)
	}
	out, err := a.Sh(ctx, a.Dir, homeEnv(q.Home, defaultHome), q.Prompt, a.Bin, args...)
	if errors.Is(err, exec.ErrNotFound) {
		return turns.Answer{}, fmt.Errorf("claude: %w", turns.ErrNoAgent)
	}
	if err != nil && ctx.Err() != nil {
		return turns.Answer{}, fmt.Errorf("claude: %w", ctx.Err())
	}
	if err != nil && !json.Valid(out) {
		if unavailable(err.Error()) {
			return turns.Answer{}, fmt.Errorf("%w: claude: %w", turns.ErrUnavailable, err)
		}
		return turns.Answer{}, fmt.Errorf("claude: %w", err)
	}
	ans, err := parse(out)
	ans.Model = cmp.Or(ans.Model, model)
	return ans, err
}

type envelope struct {
	IsError    bool            `json:"is_error"`
	Subtype    string          `json:"subtype"`
	Result     string          `json:"result"`
	Errors     []string        `json:"errors"`
	Structured json.RawMessage `json:"structured_output"`
	CostUSD    *float64        `json:"total_cost_usd"`
	DurationMS int64           `json:"duration_ms"`
	Usage      *struct {
		Input      int64 `json:"input_tokens"`
		Output     int64 `json:"output_tokens"`
		CacheRead  int64 `json:"cache_read_input_tokens"`
		CacheWrite int64 `json:"cache_creation_input_tokens"`
	} `json:"usage"`
	ModelUsage map[string]struct {
		Output int64 `json:"outputTokens"`
	} `json:"modelUsage"`
}

// model is the reported model that wrote the most, "" when none is.
func (e envelope) model() string {
	best, most := "", int64(-1)
	for m, u := range e.ModelUsage {
		if u.Output > most || u.Output == most && m < best {
			best, most = m, u.Output
		}
	}
	return best
}

// parse reads the JSON claude prints with --output-format json: the
// structured output, or the result text when there is none. A failed call
// still returns what it cost.
func parse(out []byte) (turns.Answer, error) {
	var e envelope
	if err := json.Unmarshal(out, &e); err != nil {
		return turns.Answer{}, fmt.Errorf("%w: claude's answer is not JSON: %v", turns.ErrUnfit, err)
	}
	ans := turns.Answer{Model: e.model(), CostUSD: e.CostUSD, Duration: time.Duration(e.DurationMS) * time.Millisecond}
	if u := e.Usage; u != nil {
		ans.Tokens = &usage.Tokens{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite}
	}
	if e.IsError || (e.Subtype != "" && e.Subtype != "success") {
		return ans, e.failure()
	}
	ans.JSON = []byte(e.Structured)
	if len(ans.JSON) == 0 || string(ans.JSON) == "null" {
		ans.JSON = []byte(e.Result)
	}
	return ans, nil
}

func (e envelope) failure() error {
	msg := e.Subtype
	if len(e.Errors) > 0 {
		msg = firstLine(e.Errors[0])
	} else if r := firstLine(e.Result); r != "" {
		msg = r
	}
	switch e.Subtype {
	case "error_max_budget_usd":
		return fmt.Errorf("%w: claude: %s", turns.ErrOverBudget, msg)
	case "error_max_structured_output_retries", "error_max_turns":
		return fmt.Errorf("%w: claude: %s", turns.ErrUnfit, msg)
	}
	if strings.Contains(msg, "Prompt is too long") {
		return fmt.Errorf("%w: claude: %s", turns.ErrTooLong, msg)
	}
	if unavailable(msg) {
		return fmt.Errorf("%w: claude: %s", turns.ErrUnavailable, msg)
	}
	return fmt.Errorf("claude: %s", msg)
}

var unavailableStatus = regexp.MustCompile(`(?i)\b(?:http|api error|status(?: code)?)\W{0,3}(?:401|429|529)\b`)

// unavailable is a Claude error no pull request gets past: a rate limit, an
// overload, no credit, no login.
func unavailable(msg string) bool {
	low := strings.ToLower(msg)
	for _, s := range []string{"rate_limit", "rate limit", "overloaded", "credit balance", "not logged in"} {
		if strings.Contains(low, s) {
			return true
		}
	}
	return unavailableStatus.MatchString(msg)
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}

// Exec is the ShellFunc of headless answers: it returns stdout even when
// claude exits non-zero, since claude reports its own errors as JSON there.
func Exec(ctx context.Context, dir string, env []string, stdin, name string, args ...string) (out []byte, err error) {
	defer meterSpawn(ctx, "ask", time.Now(), &err)
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = withoutCallingSession(os.Environ())
	for _, kv := range env {
		key, _, set := strings.Cut(kv, "=")
		cmd.Env = slices.DeleteFunc(cmd.Env, func(o string) bool { return strings.HasPrefix(o, key+"=") })
		if set {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = procgroup.Run(cmd)
	if err != nil && stderr.Len() > 0 {
		err = fmt.Errorf("%s: %w", firstLine(stderr.String()), err)
	}
	return stdout.Bytes(), err
}

// sessionEnv is what a Claude Code session that started the daemon would
// hand down to claude -p: its session and its effort. Settings such as
// CLAUDE_CODE_OAUTH_TOKEN stay.
var sessionEnv = []string{"CLAUDECODE", "CLAUDE_EFFORT", "CLAUDE_PID", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_CHILD_SESSION",
	"CLAUDE_CODE_SESSION_ATTENDED", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_EXECPATH", "CLAUDE_CODE_MESSAGING_SOCKET", "CLAUDE_CODE_MESSAGING_TOKEN"}

func withoutCallingSession(env []string) []string {
	return slices.DeleteFunc(slices.Clone(env), func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return slices.Contains(sessionEnv, k)
	})
}

func meterSpawn(ctx context.Context, op string, start time.Time, err *error) {
	meter.Add(ctx, meter.Sample{Res: meter.Agent, Op: op, Spawn: true, Err: *err != nil && ctx.Err() == nil, Dur: time.Since(start)})
}
