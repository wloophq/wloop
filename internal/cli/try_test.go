package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/agent/claude"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/turns"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/turn"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

type tryCall struct {
	Session turns.AgentSession
	Prompt  string
}

type tryAgent struct {
	claude.Agent
	calls   []tryCall
	publish string
}

func (a *tryAgent) Headless(ctx context.Context, s turns.AgentSession, prompt string) ([]byte, error) {
	a.calls = append(a.calls, tryCall{Session: s, Prompt: prompt})
	if err := a.Agent.Preflight(ctx, s); err != nil {
		return nil, err
	}
	if a.publish != "" {
		os.MkdirAll(filepath.Join(s.Worktree, ".loop"), 0o700)
		os.WriteFile(filepath.Join(s.Worktree, ".loop", "dry-run.json"), []byte(a.publish), 0o600)
	}
	return []byte("the agent's last words"), nil
}

func tryEnv(t *testing.T, a *tryAgent) *testEnv {
	t.Helper()
	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
	te := newTestEnv(t, m)
	te.gh.basics = openBasics
	te.Agents = map[string]turns.Agent{"claude": a}
	return te
}

func TestTrySettingsDenyEveryWrite(t *testing.T) {
	t.Parallel()
	raw := claude.Agent{}.Settings(tryPolicy("'/bin/"+brand.Name+"'", bundledStored()).Denied(turns.Control{Commands: [][]string{{"orca"}}}))
	for _, want := range []string{"Bash(gh api * --method*)", "Bash(gh api * -X*)", "Bash(gh api * -f*)", "Bash(gh api * --input*)",
		"Bash(gh pr comment *)", "Bash(gh pr review *)", "Bash(git push *)", "Bash(orca *)", "Bash('/bin/" + brand.Name + "' settle *)", "Bash(curl *)"} {
		if !strings.Contains(raw, want) {
			t.Errorf("deny lacks %s:\n%s", want, raw)
		}
	}
	if !strings.Contains(raw, "Bash('/bin/"+brand.Name+"' instructions review --turn *)") {
		t.Errorf("the agent cannot read its instructions:\n%s", raw)
	}
}

func TestTryPromptIsADryRunTurn(t *testing.T) {
	t.Parallel()
	a := &tryAgent{publish: `{"event":"APPROVE","body":"","comments":[]}`}
	te := tryEnv(t, a)
	if code := te.run("profile", "try", r7.URL()); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if len(a.calls) != 1 || strings.HasSuffix(a.calls[0].Prompt, " Dry run.") {
		t.Fatalf("calls %+v", a.calls)
	}
	if h, _, _, _ := promptOf(t, a.calls[0].Prompt); h != "Dry-run review of acme/api#7: trying a profile, nothing is published." {
		t.Fatalf("calls %+v", a.calls)
	}
	if !strings.Contains(te.out.String(), "APPROVE") {
		t.Fatalf("out %s", te.out)
	}
}

func TestTryRunsHeadlessInTheThrowawayWorktree(t *testing.T) {
	t.Parallel()
	a := &tryAgent{publish: `{"event":"APPROVE"}`}
	te := tryEnv(t, a)
	te.cfg.Projects = []config.Project{{Match: "acme/*", AgentHome: "/home/me/.claude-p"}}
	if code := te.run("profile", "try", r7.URL()); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if len(a.calls) != 1 {
		t.Fatalf("calls %+v", a.calls)
	}
	s := a.calls[0].Session
	if s.Worktree == "" || !strings.Contains(strings.Join(te.git.writes, "\n"), "worktree add --detach "+s.Worktree+" ") {
		t.Fatalf("worktree %q, writes:\n%s", s.Worktree, strings.Join(te.git.writes, "\n"))
	}
	if s.Home != "/home/me/.claude-p" || s.Policy.TurnEnd {
		t.Fatalf("session %+v", s)
	}
}

func TestTryRemovesTheWorktree(t *testing.T) {
	t.Parallel()
	a := &tryAgent{publish: `{"event":"COMMENT","body":"b","comments":[{"path":"a.go","line":3,"body":"**note:** x"}]}`}
	te := tryEnv(t, a)
	if code := te.run("profile", "try", r7.URL()); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	writes := strings.Join(te.git.writes, "\n")
	if !strings.Contains(writes, "worktree add --detach") || !strings.Contains(writes, "worktree remove --force") {
		t.Fatalf("writes:\n%s", writes)
	}
	if out := te.out.String(); !strings.Contains(out, "a.go:3") || !strings.Contains(out, "**note:** x") {
		t.Fatalf("out %s", out)
	}
}

func TestTryWithoutDryRunOutputFails(t *testing.T) {
	t.Parallel()
	te := tryEnv(t, &tryAgent{})
	code := te.run("profile", "try", r7.URL())
	if code == 0 || !strings.Contains(te.err.String(), "the agent's last words") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if !strings.Contains(strings.Join(te.git.writes, "\n"), "worktree remove --force") {
		t.Fatal("worktree left behind after a failed try")
	}
}

func TestTryPolicyCoversFlagFirstAndAttachedForms(t *testing.T) {
	t.Parallel()
	raw := claude.Agent{}.Settings(tryPolicy("'/bin/"+brand.Name+"'", bundledStored()))
	for _, want := range []string{"Bash(gh api -X*)", "Bash(gh api --method*)", "Bash(gh api * --method*)", "Bash(gh api -f*)", "Bash(gh issue *)",
		"Bash(gh release *)", "Bash(gh workflow *)", "Bash(git -C *)", "Bash(git -c *)", "Bash(git config *)", "Edit(./.loop/**)", `"disableAllHooks":true`} {
		if !strings.Contains(raw, want) {
			t.Errorf("lacks %s", want)
		}
	}
	if strings.Contains(raw, `"Bash(git *)"`) || !strings.Contains(raw, "Bash(git diff *)") {
		t.Error("every git command is allowed")
	}
}

func TestRoundSessionsMayRunLoop(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.Executable = "/Apps/" + brand.Name
	raw, err := te.agentCommand(context.Background(), te.cfg, "/wt", roundOptions{trusted: true})
	if err != nil || !strings.Contains(raw, "--settings") || strings.Contains(raw, "--permission-mode") {
		t.Fatalf("%q %v", raw, err)
	}
	raw = strings.ReplaceAll(raw, `'\''`, "'")
	for _, want := range []string{"Bash('/Apps/" + brand.Name + "' instructions review --turn *)", "Bash(/Apps/" + brand.Name + " instructions review --turn *)", "Bash('/Apps/" + brand.Name + "' turn end *)", "Bash('/Apps/" + brand.Name + "' turn answer *)", "Bash('/Apps/" + brand.Name + "' config get *)", brand.ToolPrefix() + "turn_end", "Edit(./.loop/**)"} {
		if !strings.Contains(raw, want) {
			t.Errorf("lacks %s:\n%s", want, raw)
		}
	}
}

func TestRoundSessionsReadOnlyStoredTurns(t *testing.T) {
	t.Parallel()
	for name, policy := range map[string]turns.Policy{"round": roundPolicy("'/bin/"+brand.Name+"'", bundledStored()), "try": tryPolicy("'/bin/"+brand.Name+"'", bundledStored())} {
		raw := claude.Agent{}.Settings(policy)
		var want []string
		for _, intent := range []string{"act", "address", "attach", "nudge", "review", "start"} {
			want = append(want, "Bash('/bin/"+brand.Name+"' instructions "+intent+" --turn *)")
		}
		var got []string
		for _, rule := range allowRules(t, raw) {
			if strings.HasPrefix(rule, "Bash('/bin/"+brand.Name+"' instructions") {
				got = append(got, rule)
			}
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s allows %v, want %v", name, got, want)
		}
	}
}

func TestNoAllowRuleLetsARoundSessionRunATurnByHand(t *testing.T) {
	t.Parallel()
	url := "https://github.com/acme/api/pull/7"
	for name, policy := range map[string]turns.Policy{"round": roundPolicy("'/bin/"+brand.Name+"'", bundledStored()), "try": tryPolicy("'/bin/"+brand.Name+"'", bundledStored())} {
		rules := allowRules(t, claude.Agent{}.Settings(policy))
		if !slices.ContainsFunc(rules, matchesRule("'/bin/"+brand.Name+"' instructions review --turn abc123abc123 "+url)) {
			t.Errorf("%s: the round prompt's command is not allowed", name)
		}
		for _, cmd := range []string{
			"'/bin/" + brand.Name + "' instructions review --agent --turn " + url,
			"'/bin/" + brand.Name + "' instructions review --agent 'x --turn y' " + url,
			"'/bin/" + brand.Name + "' instructions review " + url + " --turn abc123abc123",
			"'/bin/" + brand.Name + "' instructions review " + url + " -- x --turn y",
		} {
			if i := slices.IndexFunc(rules, matchesRule(cmd)); i >= 0 {
				t.Errorf("%s: %q matches %s", name, cmd, rules[i])
			}
		}
	}
}

func allowRules(t *testing.T, settings string) []string {
	t.Helper()
	var s struct {
		Permissions struct{ Allow []string } `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(settings), &s); err != nil {
		t.Fatal(err)
	}
	return s.Permissions.Allow
}

// matchesRule reads a Bash rule the way Claude Code does: each * matches any
// run of characters.
func matchesRule(cmd string) func(string) bool {
	return func(rule string) bool {
		body, ok := strings.CutPrefix(rule, "Bash(")
		if !ok {
			return false
		}
		parts := strings.Split(strings.TrimSuffix(body, ")"), "*")
		for i := range parts {
			parts[i] = regexp.QuoteMeta(parts[i])
		}
		return regexp.MustCompile("^" + strings.Join(parts, ".*") + "$").MatchString(cmd)
	}
}

func TestRoundPolicyAllowsTheTurnCommands(t *testing.T) {
	t.Parallel()
	rules := allowRules(t, claude.Agent{}.Settings(roundPolicy("'/bin/"+brand.Name+"'", bundledStored())))
	for _, want := range []string{"Bash('/bin/" + brand.Name + "' turn end *)", "Bash('/bin/" + brand.Name + "' turn answer *)", brand.ToolPrefix() + "turn_end", "Edit(./.loop/**)"} {
		if !slices.Contains(rules, want) {
			t.Errorf("lacks %s: %q", want, rules)
		}
	}
}

func TestSessionPoliciesDenyEveryManagersControl(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.Managers = map[string]ManagerEntry{"zed": {Binary: "zed-cli", Control: [][]string{{"zed", "ctl"}}, ControlPaths: []string{"/zed/sock"}}, "none": {}, "orca": orcaManager(nil)}
	has := func(p turns.Policy, rule ...string) bool {
		return slices.ContainsFunc(p.Deny, func(d []string) bool { return slices.Equal(d, rule) })
	}
	for _, managers := range [][]string{{"zed", "none"}, {"none"}} {
		te.cfg.WorkspaceManagers = managers
		control := te.controlSurface(te.cfg)
		for name, p := range map[string]turns.Policy{
			"round": policies[policyRound](te.Env, "review", control),
			"probe": policies["probe"](te.Env, "ask-author", control),
			"try":   tryPolicy("p", bundledStored()).Denied(control),
		} {
			zed := managers[0] == "zed"
			if has(p, "zed-cli") != zed || has(p, "zed", "ctl") != zed || slices.Contains(p.DenyPaths, "/zed/sock") != zed || has(p, "orca") || has(p, "") {
				t.Errorf("%s under %q: deny %q, paths %q", name, managers, p.Deny, p.DenyPaths)
			}
		}
	}
}

func TestTryUsageExitsTwo(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"profile"}, {"profile", "frob"}, {"profile", "try"}, {"profile", "try", r7.URL(), "extra"}} {
		te := newTestEnv(t, &fake.Manager{})
		want := tryUsage
		if len(args) < 2 || args[1] != "try" {
			want = profileUsage
		}
		if code := te.run(args...); code != 2 || !strings.Contains(te.err.String(), want) {
			t.Errorf("%v: exit %d: %s", args, code, te.err)
		}
	}
}

// bundledStored are the bundled intents whose stored turns a round reads.
func bundledStored() []string {
	l, err := turn.Bundled()
	if err != nil {
		panic(err)
	}
	return prIntents(l.Registry)
}

var probeStored = []string{"probe"}

func TestProfileTryShowsTheWhy(t *testing.T) {
	t.Parallel()
	a := &tryAgent{publish: `{"event":"APPROVE","body":"","comments":[]}`}
	te := tryEnv(t, a)
	file := filepath.Join(t.TempDir(), "why.json")
	why := `{"Workflow":"followup","Trigger":"event","Event":{"Kind":"review.submitted","Data":"CHANGES_REQUESTED:1a2b3c"},` +
		`"Matched":[{"Name":"pr.state","Value":"OPEN"},{"Name":"pr.silent_for","Value":129600000000000},{"Name":"pr.reviewers","Value":["a","b"]}]}`
	if err := os.WriteFile(file, []byte(why), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := te.run("profile", "try", r7.URL(), "--why", file); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	text := turnText(t, te, a.calls[0].Prompt)
	for _, want := range []string{"## Why this turn started", "the workflow `followup`", "on the event `review.submitted` (`CHANGES_REQUESTED:1a2b3c`)", "- `pr.state`: `OPEN`",
		"- `pr.silent_for`: `129600000000000`", "- `pr.reviewers`: `a, b`"} {
		if !strings.Contains(text, want) {
			t.Errorf("the dry run lacks %q", want)
		}
		if !strings.Contains(te.err.String(), want) {
			t.Errorf("profile try does not show %q:\n%s", want, te.err)
		}
	}
	if strings.Contains(te.err.String(), "## Rules") {
		t.Errorf("profile try shows more than the why:\n%s", te.err)
	}
}

func TestProfileTryWithoutAWhyRendersNone(t *testing.T) {
	t.Parallel()
	a := &tryAgent{publish: `{"event":"APPROVE","body":"","comments":[]}`}
	te := tryEnv(t, a)
	if code := te.run("profile", "try", r7.URL()); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if text := turnText(t, te, a.calls[0].Prompt); strings.Contains(text, "Why this turn started") || strings.Contains(te.err.String(), "why") {
		t.Errorf("a dry run without --why renders one:\n%s", te.err)
	}
}

func TestProfileTryRefusesABadWhy(t *testing.T) {
	t.Parallel()
	te := tryEnv(t, &tryAgent{})
	file := filepath.Join(t.TempDir(), "why.json")
	os.WriteFile(file, []byte("not json"), 0o600)
	if code := te.run("profile", "try", r7.URL(), "--why", file); code == 0 || !strings.Contains(te.err.String(), "is not a why") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
}
