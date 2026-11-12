package cli

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/agent/claude"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func denyRules(t *testing.T, settings string) []string {
	t.Helper()
	var s struct {
		Permissions struct{ Deny []string } `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(settings), &s); err != nil {
		t.Fatal(err)
	}
	return s.Permissions.Deny
}

func TestRoundPolicyDeniesManifestWriteCommands(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	wants := writeCommands(te.loadedSources())
	if len(wants) == 0 {
		t.Fatal("no bundled manifest declares write_commands")
	}
	control := te.controlSurface(te.cfg)
	if d, ok := te.loadedIntents().Registry.Get("act"); !ok || d.Session.NoSourceWrites {
		t.Fatalf("the act intent forbids source writes: %+v", d.Session)
	}
	free := policies[policyRound](te.Env, "act", control)
	for _, w := range wants {
		if slices.ContainsFunc(free.Deny, func(d []string) bool { return slices.Equal(d, w) }) {
			t.Fatalf("an intent that allows source writes denies %q", w)
		}
	}
	for _, name := range []string{"review", "address", "nudge"} {
		if d, ok := te.loadedIntents().Registry.Get(dintent.Name(name)); !ok || !d.Session.NoSourceWrites {
			t.Fatalf("the bundled %s intent allows source writes: %+v", name, d.Session)
		}
	}
	for name, p := range map[string]turns.Policy{
		"round": policies[policyRound](te.Env, "review", control),
		"probe": policies["probe"](te.Env, "review", control),
	} {
		for _, w := range wants {
			if !slices.ContainsFunc(p.Deny, func(d []string) bool { return slices.Equal(d, w) }) {
				t.Errorf("%s: denies no %q", name, strings.Join(w, " "))
			}
		}
	}

	deny := denyRules(t, claude.Agent{}.Settings(policies[policyRound](te.Env, "review", control)))
	for _, cmd := range []string{
		"gh pr review 7 --approve",
		"gh pr comment 7 --body x",
		"gh api -X POST repos/acme/api/pulls/7/reviews",
		"gh api repos/acme/api/issues/7/comments -f body=x",
		"gh api --method POST repos/acme/api/pulls/7/reviews",
		"gh api repos/acme/api/pulls/7/reviews --input review.json",
	} {
		if !slices.ContainsFunc(deny, matchesRule(cmd)) {
			t.Errorf("a review session may run %q: deny %q", cmd, deny)
		}
	}
	t.Run("every gh api write form", func(t *testing.T) {
		for _, cmd := range []string{
			"gh api -f body=x repos/acme/api/issues/7/comments",
			"gh api --input review.json repos/acme/api/pulls/7/reviews",
			"gh api -XPOST repos/acme/api/pulls/7/reviews",
			"gh api --method=POST repos/acme/api/pulls/7/reviews",
			"gh api -X=POST repos/acme/api/pulls/7/reviews",
			"gh api repos/acme/api/pulls/7/reviews -XPOST",
			"gh api repos/acme/api/pulls/7/reviews -X POST",
		} {
			if !slices.ContainsFunc(deny, matchesRule(cmd)) {
				t.Errorf("a review session may run %q", cmd)
			}
		}
	})
	for _, read := range []string{"gh pr view 7", "gh pr diff 7", "gh api repos/acme/api/pulls/7/comments"} {
		if i := slices.IndexFunc(deny, matchesRule(read)); i >= 0 {
			t.Errorf("a review session may not read with %q: %s", read, deny[i])
		}
	}
}

func TestRoundPolicyAllowsTheManifestsDraftTools(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	p := policies[policyRound](te.Env, "review", te.controlSurface(te.cfg))
	var want []string
	for _, s := range te.loadedSources() {
		for _, tool := range s.Tools {
			name := brand.ToolPrefix() + tool.Name
			if tool.Proposes != slices.Contains(p.DraftTools, name) {
				t.Errorf("%s proposes %v, allowed %v", name, tool.Proposes, !tool.Proposes)
			}
			if tool.Proposes {
				want = append(want, name)
			}
		}
	}
	if len(want) == 0 || !slices.Contains(p.DraftTools, brand.ToolPrefix()+"draft_update") {
		t.Fatalf("draft tools %q, want the manifests' %q and the loop's own", p.DraftTools, want)
	}
	if probe := policies["probe"](te.Env, "probe", te.controlSurface(te.cfg)); len(probe.DraftTools) != 0 {
		t.Errorf("a probe may draft with %q", probe.DraftTools)
	}
}
